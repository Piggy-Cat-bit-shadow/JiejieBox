package mieru

import (
	"bytes"
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/receivebudget"
	"github.com/sagernet/sing-box/log"

	"github.com/sagernet/sing/service"

	mieruprotocol "github.com/enfein/mieru/v3/pkg/protocol"

	"github.com/stretchr/testify/require"
)

// boxContextWithReceiveBudget builds the construction context a Network Extension box hands to an
// outbound: a service registry (which is how every policy travels) carrying the box's decision.
func boxContextWithReceiveBudget(policy receivebudget.Policy) context.Context {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	return receivebudget.NewContext(ctx, policy)
}

// TestReceiveBudgetFromTheBoxContextReachesTheClientConfig is the ownership link the whole mechanism
// hangs on: the BOX decides the budget and installs it in the construction context, and the OUTBOUND
// reads it into the SDK client config. The two ends are covered separately (box_receivebudget_test.go
// installs it, TestApplyReceiveBudgetIsWrittenOnlyWhereItCanBeHonoured writes it), so an omission
// between them leaves both tests green while no session ever gets a quota.
//
// The oracle is the value the SDK will actually read - clientConfig.ReceiveByteQuota on the runtime
// that the dial path uses - not a value the policy wrote somewhere.
func TestReceiveBudgetFromTheBoxContextReachesTheClientConfig(t *testing.T) {
	options := validOutboundOptions()
	options.Multiplexing = "off"

	created, err := NewOutbound(boxContextWithReceiveBudget(receivebudget.DefaultNetworkExtension()),
		nil, nopLogger(), "mieru-budget", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	t.Cleanup(func() { _ = outboundInstance.Close() })

	require.NotNil(t, outboundInstance.primary)
	require.NotNil(t, outboundInstance.primary.config)
	require.Equal(t, receivebudget.DefaultNetworkExtension().UnreadPayloadBytes,
		outboundInstance.primary.config.ReceiveByteQuota,
		"the box's budget must reach the SDK client config of the runtime that dials")
}

// TestReceiveBudgetDoesNotReachASharedReader keeps the second half of the contract: the value is
// written only where it can be honoured. A multiplexed profile must keep the SDK default even
// though the box installed a budget, because parking a shared reader would stall every session.
func TestReceiveBudgetDoesNotReachASharedReader(t *testing.T) {
	for _, multiplexing := range []string{"", "default", "low", "middle", "high"} {
		options := validOutboundOptions()
		options.Multiplexing = multiplexing

		created, err := NewOutbound(boxContextWithReceiveBudget(receivebudget.DefaultNetworkExtension()),
			nil, nopLogger(), "mieru-budget-"+multiplexing, options)
		require.NoError(t, err)
		outboundInstance := created.(*Outbound)
		require.Zero(t, outboundInstance.primary.config.ReceiveByteQuota,
			"multiplexing %q shares its reader and must not be parked", multiplexing)
		require.NoError(t, outboundInstance.Close())
	}
}

// TestReceiveBudgetAbsentWithoutAPolicy is the desktop contract: a box that installed no policy must
// produce exactly the client config it produced before this mechanism existed.
func TestReceiveBudgetAbsentWithoutAPolicy(t *testing.T) {
	options := validOutboundOptions()
	options.Multiplexing = "off"

	created, err := NewOutbound(service.ContextWithDefaultRegistry(context.Background()),
		nil, nopLogger(), "mieru-no-budget", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	t.Cleanup(func() { _ = outboundInstance.Close() })

	require.Zero(t, outboundInstance.primary.config.ReceiveByteQuota)
}

// TestReceiveBudgetIsReportedInTheLogLine closes the reporting half of the ownership chain: the
// budget is invisible in the outbound's JSON, so the only place an operator learns it was applied
// (or refused) is the outbound's own log line. The helper existed but nothing called it, so the
// behaviour and the sentence had no way to disagree - and no way to be true either.
//
// The oracle is the pair: the sentence must name the budget AND say that the exclusive reader pauses
// at it, and the client config must actually carry the quota. A log line that claims enforcement the
// client does not have fails here.
func TestReceiveBudgetIsReportedInTheLogLine(t *testing.T) {
	var logged bytes.Buffer
	factory, err := log.New(log.Options{DefaultWriter: &logged})
	require.NoError(t, err)
	require.NoError(t, factory.Start())
	t.Cleanup(func() { _ = factory.Close() })

	options := validOutboundOptions()
	options.Multiplexing = "off"
	created, err := NewOutbound(boxContextWithReceiveBudget(receivebudget.DefaultNetworkExtension()),
		nil, factory.NewLogger("mieru-budget"), "mieru-budget-log", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	t.Cleanup(func() { _ = outboundInstance.Close() })
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })

	require.Contains(t, logged.String(), "mieru receive budget 256 kB")
	require.Contains(t, logged.String(), "pauses at the budget")
	require.Equal(t, int64(256<<10), outboundInstance.primary.config.ReceiveByteQuota,
		"the sentence and the client config must describe the same decision")
}

// TestReceiveBudgetLogNamesTheEnforcedQuota closes the other half of the same disagreement: when the
// requested budget is below the protocol's minimum the client carries the raised value, so a log line
// that printed the request would tell an operator their 1-byte budget is in force when 64 KiB is.
//
// The oracle is the pair again, in the clamped direction: the sentence names the enforced quota AND
// says it was raised, and the client config carries exactly that quota.
func TestReceiveBudgetLogNamesTheEnforcedQuota(t *testing.T) {
	var logged bytes.Buffer
	factory, err := log.New(log.Options{DefaultWriter: &logged})
	require.NoError(t, err)
	require.NoError(t, factory.Start())
	t.Cleanup(func() { _ = factory.Close() })

	options := validOutboundOptions()
	options.Multiplexing = "off"
	created, err := NewOutbound(boxContextWithReceiveBudget(receivebudget.Policy{UnreadPayloadBytes: 1}),
		nil, factory.NewLogger("mieru-budget-raised"), "mieru-budget-raised-log", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	t.Cleanup(func() { _ = outboundInstance.Close() })
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })

	require.Contains(t, logged.String(), "mieru receive budget 64 kB",
		"the sentence must name the quota the reader enforces")
	require.Contains(t, logged.String(), "raised from",
		"a raised request must be reported as raised, not silently substituted")
	require.Equal(t, int64(mieruprotocol.MinReceiveByteQuota), outboundInstance.primary.config.ReceiveByteQuota)
}

// TestReceiveBudgetBelowTheProtocolMinimumDoesNotPoisonTheOutbound covers the value the SDK refuses:
// receivebudget.Policy accepts any positive byte count, but mieru rejects a positive quota below
// protocol.MinReceiveByteQuota (64 KiB) in Client.Store. Writing such a value unchanged turns a
// memory-tuning number into an outbound that cannot be constructed at all, so the boundary has to be
// handled where the value is written.
//
// The oracle is not "no error": the outbound must build AND the quota must be one the protocol can
// enforce, so the bound is real rather than silently absent.
func TestReceiveBudgetBelowTheProtocolMinimumDoesNotPoisonTheOutbound(t *testing.T) {
	for _, requested := range []int64{1, 4096, int64(mieruprotocol.MinReceiveByteQuota) - 1} {
		options := validOutboundOptions()
		options.Multiplexing = "off"

		created, err := NewOutbound(boxContextWithReceiveBudget(receivebudget.Policy{UnreadPayloadBytes: requested}),
			nil, nopLogger(), "mieru-small-budget", options)
		require.NoError(t, err, "a budget below the protocol minimum must not fail the outbound")
		outboundInstance := created.(*Outbound)
		require.Equal(t, int64(mieruprotocol.MinReceiveByteQuota), outboundInstance.primary.config.ReceiveByteQuota,
			"the enforced quota must be the smallest one the protocol can honour, not the unusable value")
		require.NoError(t, outboundInstance.Close())
	}

	options := validOutboundOptions()
	options.Multiplexing = "off"
	created, err := NewOutbound(boxContextWithReceiveBudget(
		receivebudget.Policy{UnreadPayloadBytes: int64(mieruprotocol.MinReceiveByteQuota)}), nil, nopLogger(), "mieru-min-budget", options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = created.(*Outbound).Close() })
	require.Equal(t, int64(mieruprotocol.MinReceiveByteQuota), created.(*Outbound).primary.config.ReceiveByteQuota,
		"the minimum itself is accepted unchanged")
}
