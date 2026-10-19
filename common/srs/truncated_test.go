package srs

import (
	"bytes"
	"net/netip"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// testRuleSetPayload builds the smallest valid binary rule set: one IP-CIDR rule.
func testRuleSetPayload(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	require.NoError(t, Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("10.0.0.0/8"))),
				},
			},
		}},
	}, C.RuleSetVersionCurrent))
	return payload.Bytes()
}

// TestReadRejectsTruncatedStream pins the parser's end-of-stream check.
//
// `Read` stops after the declared number of rules. The zlib trailer (Adler-32) follows the
// compressed data, so a file cut by a few bytes still contains every rule and used to parse
// successfully. Only the checksum verification at end-of-stream can tell the difference.
func TestReadRejectsTruncatedStream(t *testing.T) {
	payload := testRuleSetPayload(t)
	_, err := Read(bytes.NewReader(payload), false)
	require.NoError(t, err, "the complete payload must parse")

	for _, cut := range []int{1, 2, 4} {
		_, err = Read(bytes.NewReader(payload[:len(payload)-cut]), false)
		require.Error(t, err, "a stream missing its last %d byte(s) must be rejected", cut)
	}
}

// TestReadAllowsTrailingBytesAfterStream pins that the new end-of-stream check does not over-read:
// zlib ends at its own trailer, and a container may carry bytes after it. The parser must verify the
// stream without requiring those bytes to be absent or consuming them.
func TestReadAllowsTrailingBytesAfterStream(t *testing.T) {
	payload := testRuleSetPayload(t)
	trailing := append(append([]byte(nil), payload...), []byte("container-metadata")...)

	ruleSet, err := Read(bytes.NewReader(trailing), false)
	require.NoError(t, err)
	require.Len(t, ruleSet.Options.Rules, 1)
}

// TestReadRejectsCorruptChecksum is the same branch with a complete-length stream whose trailer
// is wrong: the rules are intact, but the stream is not the one that was written.
func TestReadRejectsCorruptChecksum(t *testing.T) {
	payload := testRuleSetPayload(t)
	corrupted := append([]byte(nil), payload...)
	corrupted[len(corrupted)-1] ^= 0xFF

	_, err := Read(bytes.NewReader(corrupted), false)
	require.Error(t, err)
}
