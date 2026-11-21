package mieru

import (
	"testing"

	"github.com/sagernet/sing-box/common/receivebudget"
	"github.com/sagernet/sing-box/option"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"

	"github.com/stretchr/testify/require"
)

func clientConfigWithMultiplexing(t *testing.T, level mierupb.MultiplexingLevel) *mieruclient.ClientConfig {
	t.Helper()
	return &mieruclient.ClientConfig{Profile: &mierupb.ClientProfile{
		Multiplexing: &mierupb.MultiplexingConfig{Level: level.Enum()},
	}}
}

// TestApplyReceiveBudgetIsWrittenOnlyWhereItCanBeHonoured is the property the whole policy rests on:
// the budget is backpressure from pausing an EXCLUSIVE reader, so it is written only for a profile
// that multiplexes off. On a multiplexed profile the value would be refused by the SDK, and writing
// it anyway - or letting it through - would park a reader shared by every session on the connection.
func TestApplyReceiveBudgetIsWrittenOnlyWhereItCanBeHonoured(t *testing.T) {
	budget := receivebudget.Policy{UnreadPayloadBytes: 256 << 10}

	exclusive := clientConfigWithMultiplexing(t, mierupb.MultiplexingLevel_MULTIPLEXING_OFF)
	require.True(t, applyReceiveBudget(exclusive, budget),
		"an exclusive reader can be paused, so the budget reaches the client")
	require.Equal(t, int64(256<<10), exclusive.ReceiveByteQuota)

	multiplexed := clientConfigWithMultiplexing(t, mierupb.MultiplexingLevel_MULTIPLEXING_DEFAULT)
	require.False(t, applyReceiveBudget(multiplexed, budget),
		"a shared reader must never be parked for one session")
	require.Zero(t, multiplexed.ReceiveByteQuota,
		"a budget that cannot be honoured must not be written at all")
}

// TestApplyReceiveBudgetWithoutAPolicyChangesNothing covers every platform that installs no policy.
func TestApplyReceiveBudgetWithoutAPolicyChangesNothing(t *testing.T) {
	config := clientConfigWithMultiplexing(t, mierupb.MultiplexingLevel_MULTIPLEXING_OFF)
	require.False(t, applyReceiveBudget(config, receivebudget.Policy{}))
	require.Zero(t, config.ReceiveByteQuota)

	require.False(t, applyReceiveBudget(nil, receivebudget.Policy{UnreadPayloadBytes: 1 << 20}),
		"a nil config is not a reason to panic")
}

// TestReceiveBudgetVerdictMatchesTheAppliedDecision keeps the log line and the behaviour from
// disagreeing: the verdict is computed from the SAME parser the profile is built with.
func TestReceiveBudgetVerdictMatchesTheAppliedDecision(t *testing.T) {
	offCase := option.MieruOutboundOptions{Multiplexing: "off"}
	require.Contains(t, receiveBudgetVerdict(offCase), "pauses at the budget")

	for _, value := range []string{"", "default", "low", "middle", "high"} {
		testCase := option.MieruOutboundOptions{Multiplexing: value}
		require.Contains(t, receiveBudgetVerdict(testCase), "not applied",
			"multiplexing %q is not an exclusive reader", value)
	}
}
