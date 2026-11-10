package libbox

// The production query path, end to end.
//
// The other DNS-history tests record events directly, which is the right instrument for byte and
// pagination contracts but proves nothing about the path a real query takes. This one drives a
// lookup through the box's own DNS router - a `direct` outbound with `domain_resolver` set, dialling
// a name that a controlled `hosts` server answers - and reads the resulting event back through the
// gomobile binding. It is the chain the review asked for: query -> event bus -> ring -> libbox.
//
// The dial itself fails on purpose: the hosts server answers 127.0.0.9, where nothing listens. The
// DNS query still happened, and its event is what the history must retain.

import (
	"context"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

const realQueryDNSConfig = `{"log":{"level":"error"},
	"dns":{"servers":[{"type":"hosts","tag":"local","predefined":{"history.test":["127.0.0.9"]}}],"final":"local"},
	"outbounds":[{"type":"direct","tag":"direct","domain_resolver":"local"}],
	"route":{"final":"direct"}}`

// dialThroughBox asks the box's direct outbound to reach a domain, which makes the box resolve it
// through its own DNS router. The dial is expected to fail; the resolution is the point.
func dialThroughBox(t *testing.T, server *CommandServer, host string) {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	outbound, loaded := instance.Box().Outbound().Outbound("direct")
	require.True(t, loaded, "the config declares a direct outbound")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, dialErr := outbound.DialContext(ctx, "tcp", M.Socksaddr{Fqdn: host, Port: 9})
	if conn != nil {
		_ = conn.Close()
	}
	// 127.0.0.9:9 has no listener, so the dial is refused. The assertion is deliberately on the
	// dial: a lookup that never happened because the outbound rejected the destination would leave
	// the history empty for a reason that has nothing to do with the history.
	require.Error(t, dialErr, "the hosts answer is a loopback address with no listener")
}

// TestTheRealQueryPathReachesTheHistory is the production-path proof.
func TestTheRealQueryPathReachesTheHistory(t *testing.T) {
	server := startBoxTestCommandServer(t, realQueryDNSConfig)

	// Before enabling, a query must not fail the box and must not leave a window behind.
	dialThroughBox(t, server, "history.test")
	_, err := server.DNSHistoryQuery("")
	require.Error(t, err, "history is off until asked for")

	enabled, err := server.DNSHistoryEnable(`{"maxEntries":32,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	require.Contains(t, enabled.Value, `"enabled":true`)

	dialThroughBox(t, server, "history.test")

	var found bool
	require.Eventually(t, func() bool {
		boxed, queryErr := server.DNSHistoryQuery(`{"limit":8}`)
		if queryErr != nil {
			return false
		}
		result := decodeDNSHistoryQuery(t, boxed.Value)
		for _, record := range result.Records {
			if record.Name == "history.test." || record.Name == "history.test" {
				found = true
			}
		}
		return found
	}, 5*time.Second, 10*time.Millisecond,
		"the event produced by the box's own DNS router must reach the ring and the binding")

	// The event carries the attribution the history is for: a transport, a source and a sequence.
	boxed, err := server.DNSHistoryQuery(`{"limit":8}`)
	require.NoError(t, err)
	result := decodeDNSHistoryQuery(t, boxed.Value)
	require.NotEmpty(t, result.Records)
	require.GreaterOrEqual(t, result.Stats.Entries, 1)
	var named bool
	for _, record := range result.Records {
		if record.Name != "history.test." && record.Name != "history.test" {
			continue
		}
		named = true
		require.NotZero(t, record.Sequence)
		require.NotEmpty(t, record.Source)
	}
	require.True(t, named)

	// Disabling detaches: the same query path keeps working but is no longer recorded.
	require.NoError(t, server.DNSHistoryDisable())
	_, err = server.DNSHistoryQuery("")
	require.Error(t, err)
	dialThroughBox(t, server, "history.test")
	_, err = server.DNSHistoryQuery("")
	require.Error(t, err, "a disabled history stays disabled after more traffic")
}

// TestTwoBoxesKeepSeparateHistories: the ring belongs to the instance, not to the process.
func TestTwoBoxesKeepSeparateHistories(t *testing.T) {
	first := startBoxTestCommandServer(t, realQueryDNSConfig)
	_, err := first.DNSHistoryEnable(`{"maxEntries":8,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	dialThroughBox(t, first, "history.test")
	require.Eventually(t, func() bool {
		boxed, queryErr := first.DNSHistoryQuery(`{}`)
		return queryErr == nil && decodeDNSHistoryQuery(t, boxed.Value).Stats.Entries >= 1
	}, 5*time.Second, 10*time.Millisecond)

	second := startBoxTestCommandServer(t, realQueryDNSConfig)
	_, err = second.DNSHistoryEnable(`{"maxEntries":8,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	boxed, err := second.DNSHistoryQuery(`{}`)
	require.NoError(t, err)
	require.Empty(t, decodeDNSHistoryQuery(t, boxed.Value).Records,
		"a fresh box starts with an empty window; the other instance's records are not visible")
}
