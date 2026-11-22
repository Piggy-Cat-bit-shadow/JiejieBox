//go:build unix

package rule

// N3 閳?the derived-artifact cache, exercised on a host that can actually map.
//
// These tests run the REAL `LocalRuleSet.reloadFile` path: the same function a config's `rule_set`
// uses. `mmapRuleSetPlatform` is flipped on so the Unix host runs the code the iOS build runs; the
// flag is only read when a local rule-set file is constructed, and these tests are sequential.
//
// What is asserted everywhere: a warm load serves exactly the same matches as the source, a warm
// load does NOT convert the source again (allocation falls, the store lease is reused), a failure
// of the derived path falls back to the source instead of failing, and Collect never removes a
// version a live mapping still leases.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

func derivedTestContext(t *testing.T) (context.Context, string) {
	t.Helper()
	directory := t.TempDir()
	return filemanager.WithDefault(context.Background(), directory, directory, -1, -1), directory
}

// enableDerivedRuleSetPlatform makes the Unix test host take the path the iOS product build takes.
func enableDerivedRuleSetPlatform(t *testing.T) {
	t.Helper()
	previous := mmapRuleSetPlatform
	mmapRuleSetPlatform = true
	t.Cleanup(func() { mmapRuleSetPlatform = previous })
}

func newDerivedRuleSet(t *testing.T, ctx context.Context, tag string, path string, format string) *LocalRuleSet {
	t.Helper()
	ruleSet, err := NewLocalRuleSet(ctx, logger.NOP(), tag, option.RuleSet{
		Type:         C.RuleSetTypeLocal,
		Format:       format,
		LocalOptions: option.LocalRuleSet{Path: path},
	})
	require.NoError(t, err, "load local rule-set ", path)
	t.Cleanup(func() { _ = ruleSet.Close() })
	return ruleSet
}

func requireDomainMatch(t *testing.T, ruleSet adapter.RuleSet, domain string, want bool) {
	t.Helper()
	require.Equal(t, want, ruleSet.Match(&adapter.InboundContext{Domain: domain}), "domain %s", domain)
}

func requireAddressMatch(t *testing.T, ruleSet adapter.RuleSet, address string, want bool) {
	t.Helper()
	require.Equal(t, want, ruleSet.Match(&adapter.InboundContext{
		Destination: M.ParseSocksaddrHostPort(address, 443),
	}), "address %s", address)
}

func derivedFileDigest(t *testing.T, path string) (string, int64) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), int64(len(content))
}

// derivedVersions lists the physical version directories of one store key. It is the test's view of
// what Collect decided to keep.
func derivedVersions(t *testing.T, base string, key string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(base, derivedStoreDirectory, key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v-") {
			versions = append(versions, entry.Name())
		}
	}
	return versions
}

// awaitDerivedState spins GC so runtime cleanups (which release mappings and their store leases)
// run, then checks the condition. Cleanups are asynchronous by contract, so this polls instead of
// assuming one collection is enough.
func awaitDerivedState(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		runtime.GC()
		runtime.GC()
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDerivedRuleSetWarmReloadSkipsTheConversion is the core claim: the second load of the same
// file maps the stored conversion and allocates a small fraction of what converting it costs.
func TestDerivedRuleSetWarmReloadSkipsTheConversion(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "cold-warm.srs")
	writeCensusDomainRuleSet(t, path, 10_000, 0)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ruleSet := newDerivedRuleSet(t, ctx, "cold-warm", path, C.RuleSetFormatBinary)
	runtime.ReadMemStats(&after)
	coldAllocated := after.TotalAlloc - before.TotalAlloc

	requireDomainMatch(t, ruleSet, censusDomainAt(9_999, 0), true)
	requireDomainMatch(t, ruleSet, censusDomainAt(10_000, 0), false)

	hits, misses, publishes, failures := derivedCacheDiagnostics()
	require.Zero(t, hits, "the first load cannot hit")
	require.EqualValues(t, 1, misses, "the first load must miss exactly once")
	require.EqualValues(t, 1, publishes, "the first load must publish its conversion")
	require.Zero(t, failures)

	digest, size := derivedFileDigest(t, path)
	source := localDerivedSource("cold-warm", path, C.RuleSetFormatBinary, digest, size)
	require.Len(t, derivedVersions(t, base, source.key), 1, "exactly one physical version after a cold load")

	runtime.GC()
	runtime.ReadMemStats(&before)
	require.NoError(t, ruleSet.reloadFile(path))
	runtime.ReadMemStats(&after)
	warmAllocated := after.TotalAlloc - before.TotalAlloc

	hits, misses, publishes, failures = derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the reload must be served from the derived artifact")
	require.EqualValues(t, 1, misses, "a hit must not add a conversion")
	require.EqualValues(t, 1, publishes, "a hit must not publish again")
	require.Zero(t, failures)

	requireDomainMatch(t, ruleSet, censusDomainAt(9_999, 0), true)
	requireDomainMatch(t, ruleSet, censusDomainAt(10_000, 0), false)
	t.Logf("10k domains: cold load allocated %d bytes, warm reload allocated %d bytes (%.1f%%)",
		coldAllocated, warmAllocated, 100*float64(warmAllocated)/float64(coldAllocated))
	// The bound is deliberately loose. At 10k domains the warm path is dominated by the artifact
	// store's own fixed cost (one directory listing, two manifest reads, two sequential
	// verification passes with io.Copy's 32 KiB buffer), not by the conversion being skipped; the
	// conversion's share grows with the list and is what the 100k/1M measurement isolates. What
	// this asserts is that the conversion is NOT paid: half of a cold load could not fit if
	// srs.Read or WriteMmap had run again.
	require.Less(t, warmAllocated, coldAllocated/2,
		"a warm reload must not pay the conversion again: cold=%d warm=%d", coldAllocated, warmAllocated)
}

// TestDerivedRuleSetWarmMatchesTheSourceExactly compares a warm-mapped rule-set with a reference
// built straight from the source, over rules of every family that has a compiled representation.
func TestDerivedRuleSetWarmMatchesTheSourceExactly(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, _ := derivedTestContext(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "mixed.srs")
	source := mixedDerivedFixture()
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, srs.Write(file, source, C.RuleSetVersionCurrent))
	require.NoError(t, file.Close())

	ruleSet := newDerivedRuleSet(t, ctx, "mixed", path, C.RuleSetFormatBinary)
	require.NoError(t, ruleSet.reloadFile(path)) // warm
	probes := mixedDerivedProbes()
	for _, probe := range probes {
		require.Equal(t, probe.want, ruleSet.Match(probe.context()), "probe %s", probe.name)
	}
}

type derivedProbe struct {
	name     string
	domain   string
	address  string
	sourceIP string
	port     uint16
	want     bool
}

func (p derivedProbe) context() *adapter.InboundContext {
	matchContext := &adapter.InboundContext{}
	if p.domain != "" {
		matchContext.Domain = p.domain
	}
	if p.address != "" {
		matchContext.Destination = M.ParseSocksaddrHostPort(p.address, p.port)
	} else if p.port != 0 {
		matchContext.Destination = M.Socksaddr{Port: p.port}
	}
	if p.sourceIP != "" {
		matchContext.Source = M.ParseSocksaddrHostPort(p.sourceIP, 12345)
	}
	return matchContext
}

func mixedDerivedFixture() option.PlainRuleSet {
	return option.PlainRuleSet{Rules: []option.HeadlessRule{
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				Domain:       badoption.Listable[string]{"exact.example"},
				DomainSuffix: badoption.Listable[string]{".suffix.example"},
				DomainKeyword: badoption.Listable[string]{
					"keyword",
				},
			},
		},
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				DomainRegex: badoption.Listable[string]{`^regex-[0-9]+\.example$`},
			},
		},
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					commonPrefix("10.0.0.0/8"),
					commonPrefix("2001:db8::/32"),
				},
			},
		},
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				SourceIPCIDR: badoption.Listable[*badoption.Prefixable]{
					commonPrefix("192.168.0.0/16"),
				},
				Port: badoption.Listable[uint16]{8443},
			},
		},
		{
			Type: C.RuleTypeLogical,
			LogicalOptions: option.LogicalHeadlessRule{
				Mode: C.LogicalTypeAnd,
				Rules: []option.HeadlessRule{
					{
						Type: C.RuleTypeDefault,
						DefaultOptions: option.DefaultHeadlessRule{
							DomainSuffix: badoption.Listable[string]{"logical.example"},
						},
					},
					{
						Type: C.RuleTypeDefault,
						DefaultOptions: option.DefaultHeadlessRule{
							IPCIDR: badoption.Listable[*badoption.Prefixable]{
								commonPrefix("172.16.0.0/12"),
							},
						},
					},
				},
			},
		},
	}}
}

func mixedDerivedProbes() []derivedProbe {
	return []derivedProbe{
		{name: "exact hit", domain: "exact.example", want: true},
		{name: "exact miss", domain: "other.example", want: false},
		{name: "suffix hit", domain: "deep.suffix.example", want: true},
		{name: "suffix root miss", domain: "suffix.example", want: false},
		{name: "keyword hit", domain: "a-keyword-z.example", want: true},
		{name: "regex hit", domain: "regex-42.example", want: true},
		{name: "regex miss", domain: "regex-x.example", want: false},
		{name: "ipv4 hit", address: "10.255.1.1", port: 80, want: true},
		{name: "ipv4 miss", address: "11.0.0.1", port: 80, want: false},
		{name: "ipv6 hit", address: "2001:db8::1", port: 80, want: true},
		{name: "source ip and port hit", address: "8.8.8.8", sourceIP: "192.168.1.1", port: 8443, want: true},
		{name: "source ip hit port miss", address: "8.8.8.8", sourceIP: "192.168.1.1", port: 8443 + 1, want: false},
		{name: "logical hit", domain: "x.logical.example", address: "172.20.0.1", port: 443, want: true},
		{name: "logical second branch miss", domain: "x.logical.example", address: "8.8.8.8", port: 443, want: false},
	}
}

func commonPrefix(prefix string) *badoption.Prefixable {
	value := badoption.Prefixable(netip.MustParsePrefix(prefix))
	return &value
}

// TestDerivedRuleSetSameContentTwoTags proves the conversion is shared by SOURCE, not by tag: two
// tags on one file convert once, and both serve the same matches.
func TestDerivedRuleSetSameContentTwoTags(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "shared.srs")
	writeCensusDomainRuleSet(t, path, 1_000, 0)

	first := newDerivedRuleSet(t, ctx, "tag-one", path, C.RuleSetFormatBinary)
	second := newDerivedRuleSet(t, ctx, "tag-two", path, C.RuleSetFormatBinary)

	hits, misses, publishes, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the second tag must reuse the first tag's conversion")
	require.EqualValues(t, 1, misses)
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)

	digest, size := derivedFileDigest(t, path)
	key := localDerivedSource("tag-one", path, C.RuleSetFormatBinary, digest, size).key
	require.Equal(t, key, localDerivedSource("tag-two", path, C.RuleSetFormatBinary, digest, size).key,
		"the tag must not be part of the cache identity")
	require.Len(t, derivedVersions(t, base, key), 1)

	requireDomainMatch(t, first, censusDomainAt(999, 0), true)
	requireDomainMatch(t, second, censusDomainAt(999, 0), true)
	requireDomainMatch(t, second, censusDomainAt(1_000, 0), false)
}

// TestDerivedRuleSetSourceChangeReplacesTheGeneration covers a changed file: the new bytes must be
// converted, the old mapping must keep serving until it is dropped, and only then may Collect
// remove the old version.
func TestDerivedRuleSetSourceChangeReplacesTheGeneration(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "changing.srs")
	writeCensusDomainRuleSet(t, path, 1_000, 0)
	ruleSet := newDerivedRuleSet(t, ctx, "changing", path, C.RuleSetFormatBinary)
	// Serve the first generation from the PERSISTED artifact: that is the product's steady state
	// after the first reload, and it is the mapping whose lease must protect the version.
	require.NoError(t, ruleSet.reloadFile(path))
	hits, _, _, _ := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the second load of the same file must be warm")
	// Hold the first generation the way a matcher in flight does: a live reference to the rules.
	oldRules := ruleSet.currentRules()
	require.NotEmpty(t, oldRules)

	writeCensusDomainRuleSet(t, path, 1_000, 1)
	require.NoError(t, ruleSet.reloadFile(path), "the changed source must be converted")

	requireDomainMatch(t, ruleSet, censusDomainAt(999, 1), true)
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), false)
	require.True(t, matchAnyHeadlessRule(oldRules, &adapter.InboundContext{Domain: censusDomainAt(999, 0)}),
		"the old generation must keep matching while it is referenced")
	digest, size := derivedFileDigest(t, path)
	key := localDerivedSource("changing", path, C.RuleSetFormatBinary, digest, size).key
	require.Len(t, derivedVersions(t, base, key), 2,
		"the old generation is still mapped, so Collect must keep it next to the new one")

	// The old generation is now unreachable; its mapping cleanup releases the lease, and then the
	// collector may remove it.
	oldRules = nil
	_ = oldRules
	require.True(t, awaitDerivedState(t, 10*time.Second, func() bool {
		require.NoError(t, ruleSet.derivedCache.store.Collect(key))
		return len(derivedVersions(t, base, key)) == 1
	}), "Collect must remove the unleased old generation, and stay at one version")
}

// TestDerivedRuleSetCorruptArtifactFallsBackAndRepairs covers a damaged persisted file: the load
// must succeed from the source, the corrupt version must be removed rather than served, and a
// later load must hit again.
func TestDerivedRuleSetCorruptArtifactFallsBackAndRepairs(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "corrupt.srs")
	writeCensusDomainRuleSet(t, path, 1_000, 0)
	ruleSet := newDerivedRuleSet(t, ctx, "corrupt", path, C.RuleSetFormatBinary)

	digest, size := derivedFileDigest(t, path)
	key := localDerivedSource("corrupt", path, C.RuleSetFormatBinary, digest, size).key
	version, err := ruleSet.derivedCache.store.Load(key)
	require.NoError(t, err)
	corruptPath := version.Path()
	corruptManifest := version.Manifest()
	version.Release()

	garbage := bytes.Repeat([]byte{'x'}, int(corruptManifest.Size))
	require.NoError(t, os.WriteFile(corruptPath, garbage, 0o644))

	require.NoError(t, ruleSet.reloadFile(path), "a corrupt cache must fall back to the source")
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), true)
	require.Empty(t, derivedVersions(t, base, key),
		"the corrupt version must be removed by Collect, not kept as the newest")

	// The artifact was removed, so the repair publishes it again; only the load after that hits.
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), true)
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), true)
	hits, _, _, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the repaired conversion must be served on the next load")
	require.Zero(t, failures)
	require.Len(t, derivedVersions(t, base, key), 1)
}

// TestDerivedRuleSetOldLayoutFallsBack covers a derived artifact written by another SRSM layout:
// the bytes are verified by the store, so only the reader's own version/fingerprint check can
// reject them. It must be a miss, not a wrong interpretation.
func TestDerivedRuleSetOldLayoutFallsBack(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "old-layout.srs")
	writeCensusDomainRuleSet(t, path, 1_000, 0)
	ruleSet := newDerivedRuleSet(t, ctx, "old-layout", path, C.RuleSetFormatBinary)

	digest, size := derivedFileDigest(t, path)
	source := localDerivedSource("old-layout", path, C.RuleSetFormatBinary, digest, size)
	key := source.key
	newest, err := ruleSet.derivedCache.store.Load(key)
	require.NoError(t, err)
	encoded, err := os.ReadFile(newest.Path())
	require.NoError(t, err)
	newest.Release()
	binary.NativeEndian.PutUint32(encoded[4:8], 1) // a layout generation this build does not read
	manifest := ruleSet.derivedCache.derivedManifest(source, C.RuleSetVersionCurrent, 0)
	manifest.Created = time.Now().UnixNano()
	patched, err := ruleSet.derivedCache.store.Commit(key, manifest, bytes.NewReader(encoded))
	require.NoError(t, err)
	patched.Release()

	require.NoError(t, ruleSet.reloadFile(path), "an old layout must fall back to the source")
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), true)
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, censusDomainAt(999, 0), true)
	hits, _, _, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits)
	require.Zero(t, failures)
	require.Len(t, derivedVersions(t, base, key), 1, "the rejected layout must be discarded")
}

// TestDerivedRuleSetInvalidSourceKeepsTheOriginalError pins the failure contract: an invalid source
// is still a load error with the parser's own message, not a silent empty rule-set.
func TestDerivedRuleSetInvalidSourceKeepsTheOriginalError(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, _ := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "invalid.srs")
	writeCensusDomainRuleSet(t, path, 100, 0)
	ruleSet := newDerivedRuleSet(t, ctx, "invalid", path, C.RuleSetFormatBinary)

	require.NoError(t, os.WriteFile(path, []byte("this is not a binary rule-set"), 0o600))
	err := ruleSet.reloadFile(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid sing-box rule-set file",
		"the parser's own error must survive the cache: %v", err)
	// The previously published generation keeps serving.
	requireDomainMatch(t, ruleSet, censusDomainAt(99, 0), true)
}

// TestDerivedRuleSetDisabledPlatformDoesNotWrite proves the flag really gates the whole path: where
// the product does not map rule-sets, no store directory is created and no source is hashed for a
// cache that could not be used.
func TestDerivedRuleSetDisabledPlatformDoesNotWrite(t *testing.T) {
	previous := mmapRuleSetPlatform
	mmapRuleSetPlatform = false
	t.Cleanup(func() { mmapRuleSetPlatform = previous })
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "heap.srs")
	writeCensusDomainRuleSet(t, path, 100, 0)
	ruleSet := newDerivedRuleSet(t, ctx, "heap", path, C.RuleSetFormatBinary)
	require.Nil(t, ruleSet.derivedCache)
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, censusDomainAt(99, 0), true)
	_, err := os.Stat(filepath.Join(base, derivedStoreDirectory))
	require.ErrorIs(t, err, os.ErrNotExist, "no artifact store may be created when the path is off")
}

// TestDerivedRuleSetCrashResidueIsCollected covers a crash during a publish: the leftover temp
// directory and a version without a readable manifest must both be removed by the next collect.
func TestDerivedRuleSetCrashResidueIsCollected(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "crash.srs")
	writeCensusDomainRuleSet(t, path, 100, 0)
	digest, size := derivedFileDigest(t, path)
	key := localDerivedSource("crash", path, C.RuleSetFormatBinary, digest, size).key

	keyDir := filepath.Join(base, derivedStoreDirectory, key)
	require.NoError(t, os.MkdirAll(filepath.Join(keyDir, ".tmp-crashed"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(keyDir, "v-deadbeefdeadbeef"), 0o755))

	_ = newDerivedRuleSet(t, ctx, "crash", path, C.RuleSetFormatBinary)
	entries, err := os.ReadDir(keyDir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.True(t, strings.HasPrefix(entry.Name(), "v-"), "unexpected residue %s", entry.Name())
	}
	require.Len(t, entries, 1)
}
