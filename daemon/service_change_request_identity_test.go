package daemon

// C04/C06 — the apply request's own identity.
//
// The audit's point (Q25) is that a REVISION is not a request identity: an attempt that applies the
// same document has the same target revision as the one before it, and two of the outcomes a caller
// must tell apart - `recovered` and `interrupted` - return no error at all. Without a token the
// caller cannot say "the instance now running is the one MY reload produced".
//
// These tests drive the real entry with a token and assert that every outcome - applied, unchanged,
// failed, cancelled - echoes that token and names the instance that is running afterwards.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestASameRevisionRequestStillIdentifiesItself(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	first, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-1", minimalConfig, nil)
	require.NoError(t, err)
	require.Equal(t, "reload-1", first.RequestID, "the report must echo the token it was called with")
	require.Equal(t, ServiceChangeStatusStarted, first.Status)
	require.NotEmpty(t, first.InstanceID, "the report must name the instance that is running")
	require.Equal(t, service.currentInstanceID(), first.InstanceID,
		"the named instance is the one the connection-event stream identifies")

	second, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-2", minimalConfig, nil)
	require.NoError(t, err)
	require.Equal(t, "reload-2", second.RequestID)
	require.Equal(t, first.ActiveRevision, second.ActiveRevision,
		"the same document gives the same revision, which is why a revision cannot be the request identity")
	require.Equal(t, service.currentInstanceID(), second.InstanceID,
		"the named instance is the one running after THIS attempt")
	// The identity is the ONLY place this difference is visible. MEASURED on this host: re-applying
	// the same document reports `restarted` (the log section is not live-applicable) and replaces the
	// Box, so the revision is identical while the instance is new - precisely the pair a caller
	// cannot tell apart from a revision alone. `unchanged` is the one status that must keep it.
	if second.Status == ServiceChangeStatusUnchanged {
		require.Equal(t, first.InstanceID, second.InstanceID,
			"an unchanged document must not replace the instance")
	} else {
		require.NotEqual(t, first.InstanceID, second.InstanceID,
			"any status that is not 'unchanged' rebuilt the Box; the identity must change with it")
	}

	// The report is stored as the last attempt too, and it keeps the token that produced it.
	stored := service.LastServiceChange()
	require.NotNil(t, stored)
	require.Equal(t, "reload-2", stored.RequestID)
}

// TestARestartRequestNamesTheNewInstance is the join a client needs: after a reload that rebuilt the
// Box, the report's instance identity is the NEW one, so the batches that follow belong to the
// instance this request produced.
func TestARestartRequestNamesTheNewInstance(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	first, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-before", minimalConfig, nil)
	require.NoError(t, err)

	second, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-after", ruleConfig, nil)
	require.NoError(t, err)
	require.Equal(t, "reload-after", second.RequestID)
	require.NotEmpty(t, second.InstanceID)
	require.NotEqual(t, first.InstanceID, second.InstanceID,
		"a rebuild replaces the instance; the identity must change with it")
	require.Equal(t, service.currentInstanceID(), second.InstanceID)
}

// TestAFailedRequestEchoesItsToken: a failure that leaves nothing running still answers with the
// token, so a client cannot mistake a late failure for a later request.
func TestAFailedRequestEchoesItsToken(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	report, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-bad", `{"log":`, nil)
	require.Error(t, err)
	require.NotNil(t, report, "a failed attempt still describes itself")
	require.Equal(t, "reload-bad", report.RequestID)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.Empty(t, report.InstanceID, "nothing is running after a failed first start")

	// With a service running, the refusal names the instance that SURVIVED it - the caller needs to
	// know which instance it is still talking to.
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))
	running := service.currentInstanceID()
	refused, err := service.ApplyServiceChangeWithRequest(context.Background(), "reload-refused", `{"log":`, nil)
	require.Error(t, err)
	require.Equal(t, "reload-refused", refused.RequestID)
	require.Equal(t, running, refused.InstanceID)
}

// TestACancelledRequestEchoesItsToken covers the outcome that returns an error before anything was
// attempted: the caller must still be able to match the answer to its request.
func TestACancelledRequestEchoesItsToken(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := service.ApplyServiceChangeWithRequest(cancelled, "reload-cancelled", minimalConfig, nil)
	require.Error(t, err)
	require.NotNil(t, report)
	require.Equal(t, "reload-cancelled", report.RequestID)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.Empty(t, report.InstanceID)
	require.Contains(t, report.Reason, "cancelled")
}

// TestTheOlderEntrySendsNoToken keeps the compatibility contract: every pre-existing embedder calls
// the token-less entry, and an empty token must never look like a match.
func TestTheOlderEntrySendsNoToken(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	report, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)
	require.Empty(t, report.RequestID)
	require.NotEmpty(t, report.InstanceID, "the instance identity is independent of the token")
}
