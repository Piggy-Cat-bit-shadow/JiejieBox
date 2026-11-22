//go:build unix

package srs

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestMmapMatchesSource(t *testing.T) {
	t.Parallel()
	source := option.PlainRuleSet{Rules: []option.HeadlessRule{
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				Domain:       badoption.Listable[string]{"example.com"},
				DomainSuffix: badoption.Listable[string]{".example.org"},
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("10.0.0.0/8"))),
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("2001:db8::/32"))),
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("192.0.2.1/32"))),
				},
			},
		},
		{
			Type: C.RuleTypeLogical,
			LogicalOptions: option.LogicalHeadlessRule{
				Mode: C.LogicalTypeOr,
				Rules: []option.HeadlessRule{{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultHeadlessRule{
						AdGuardDomain: badoption.Listable[string]{"||ads.example.net^"},
						SourceIPCIDR:  badoption.Listable[*badoption.Prefixable]{common.Ptr(badoption.Prefixable(netip.MustParsePrefix("172.16.0.0/12")))},
					},
				}},
			},
		},
	}}
	var binary bytes.Buffer
	require.NoError(t, Write(&binary, source, C.RuleSetVersionCurrent))
	parsed, err := Read(bytes.NewReader(binary.Bytes()), false)
	require.NoError(t, err)

	mmapPath := filepath.Join(t.TempDir(), "rule-set.mmap")
	mmapFile, err := os.Create(mmapPath)
	require.NoError(t, err)
	defer mmapFile.Close()
	require.NoError(t, WriteMmap(mmapFile, parsed))
	_, err = mmapFile.Seek(0, io.SeekStart)
	require.NoError(t, err)
	mmap, err := ReadMmap(mmapFile)
	require.NoError(t, err)
	_, err = os.Stat(mmapPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, parsed.Version, mmap.Version)
	require.Len(t, mmap.Options.Rules, 2)

	first := mmap.Options.Rules[0].DefaultOptions
	require.True(t, first.DomainMatcher.Match("example.com"))
	require.True(t, first.DomainMatcher.Match("www.example.org"))
	require.False(t, first.DomainMatcher.Match("example.org"))
	require.False(t, first.DomainMatcher.Match("example.net"))
	require.True(t, first.IPSet.Contains(netip.MustParseAddr("10.255.0.1")))
	require.True(t, first.IPSet.Contains(netip.MustParseAddr("192.0.2.1")))
	require.False(t, first.IPSet.Contains(netip.MustParseAddr("192.0.2.2")))
	require.True(t, first.IPSet.Contains(netip.MustParseAddr("2001:db8::1")))
	require.False(t, first.IPSet.Contains(netip.MustParseAddr("2001:db9::1")))

	second := mmap.Options.Rules[1].LogicalOptions.Rules[0].DefaultOptions
	require.True(t, second.AdGuardDomainMatcher.Match("ads.example.net"))
	require.False(t, second.AdGuardDomainMatcher.Match("example.net"))
	require.True(t, second.SourceIPSet.Contains(netip.MustParseAddr("172.31.255.255")))
	require.False(t, second.SourceIPSet.Contains(netip.MustParseAddr("172.32.0.0")))
}

// writeMmapFixture encodes one small rule-set as SRSM into a persistent file.
func writeMmapFixture(t *testing.T, path string) option.PlainRuleSetCompat {
	t.Helper()
	source := option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{
			Domain:       badoption.Listable[string]{"persistent.example"},
			DomainSuffix: badoption.Listable[string]{".persistent-suffix.example"},
			IPCIDR: badoption.Listable[*badoption.Prefixable]{
				common.Ptr(badoption.Prefixable(netip.MustParsePrefix("198.51.100.0/24"))),
			},
		},
	}}}
	var encoded bytes.Buffer
	require.NoError(t, Write(&encoded, source, C.RuleSetVersionCurrent))
	parsed, err := Read(bytes.NewReader(encoded.Bytes()), false)
	require.NoError(t, err)
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, WriteMmap(file, parsed))
	require.NoError(t, file.Close())
	return parsed
}

// TestReadMmapPersistentKeepsTheNameAndReleasesTheLease pins the difference between the transient
// reader and the persisted one: the name must survive the mapping (the artifact store and every
// other reader refer to it), and the caller's release hook must run when the last mapping user is
// collected - that hook is the artifact lease.
func TestReadMmapPersistentKeepsTheNameAndReleasesTheLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "derived.srsm")
	writeMmapFixture(t, path)

	file, err := os.Open(path)
	require.NoError(t, err)
	var released atomic.Bool
	mapped, err := ReadMmapPersistent(file, func() { released.Store(true) })
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = os.Stat(path)
	require.NoError(t, err, "a persisted mapping must not unlink its backing file")
	require.False(t, released.Load())

	first := mapped.Options.Rules[0].DefaultOptions
	require.True(t, first.DomainMatcher.Match("persistent.example"))
	require.False(t, first.DomainMatcher.Match("persistent.example.net"))
	require.True(t, first.IPSet.Contains(netip.MustParseAddr("198.51.100.7")))

	mapped = option.PlainRuleSetCompat{}
	first = option.DefaultHeadlessRule{}
	deadline := time.Now().Add(10 * time.Second)
	for !released.Load() && time.Now().Before(deadline) {
		runtime.GC()
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, released.Load(), "the release hook must run when the last mapping user is collected")
	_, err = os.Stat(path)
	require.NoError(t, err, "collection of a mapping must not remove the persisted file")
}

// TestReadMmapRejectsAnotherLayoutFingerprint covers the artifact travelling to a build whose
// dependency layout differs: the manifest can be compatible (same platform) while the arrays are
// not, so the file itself must refuse.
func TestReadMmapRejectsAnotherLayoutFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fingerprint.srsm")
	writeMmapFixture(t, path)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	copy(content[mmapFingerprintOffset:], "0000000000000000")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	_, err = ReadMmapPersistent(file, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fingerprint")
}

// TestReadMmapRejectsAnotherFormatVersion covers a derived file from another format generation.
func TestReadMmapRejectsAnotherFormatVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version.srsm")
	writeMmapFixture(t, path)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	binary.NativeEndian.PutUint32(content[4:8], 1)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	_, err = ReadMmapPersistent(file, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")
}

// TestReadMmapRejectsAnOverflowingBlobTable covers a CRC-valid-but-nonsense header: the blob count
// is multiplied by the entry size before any range check, so an unchecked value wraps and passes a
// naive bounds test. It must be rejected, not panic.
func TestReadMmapRejectsAnOverflowingBlobTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overflow.srsm")
	writeMmapFixture(t, path)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	binary.NativeEndian.PutUint64(content[32:40], ^uint64(0))
	require.NoError(t, os.WriteFile(path, content, 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	_, err = ReadMmapPersistent(file, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "blob table")
}
