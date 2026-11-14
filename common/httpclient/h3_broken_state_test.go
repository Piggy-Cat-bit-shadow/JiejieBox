//go:build with_quic

package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagernet/sing-box/common/resourceguard"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// A local admission refusal must not advance the HTTP/3 broken ladder.
//
// # Why this is not cosmetic
//
// The fallback transport remembers a failed authority and demotes it to HTTP/2 for at least five
// minutes (doubling up to 48 h). That memory exists for failures that say something about the PATH.
// An admission refusal says only that this process had no slot, no budget or no time; the peer was
// never contacted. Recording it would let one memory-pressure episode demote a healthy authority and
// would make the next diagnosis count self-inflicted refusals as H3 path failures - the same class of
// mistake as the round-2 URLTest health fix, one layer lower.
func TestLocalAdmissionRejectionDoesNotMarkTheAuthorityBroken(t *testing.T) {
	transport := &http3FallbackTransport{broken: make(map[string]http3BrokenEntry)}
	request := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	const authority = "example.com:443"

	localRejections := map[string]error{
		"queue full":      E.Cause(resourceguard.ErrQueueFull, "h3 admission"),
		"wait timeout":    resourceguard.ErrWaitTimeout,
		"rate limited":    resourceguard.ErrRateLimited,
		"caller deadline": context.DeadlineExceeded,
		"caller cancel":   context.Canceled,
	}
	attempt := uint64(0)
	for name, rejection := range localRejections {
		attempt++
		transport.recordH3AttemptFailure(request, authority, attempt, rejection)
		require.False(t, transport.h3Broken(authority),
			"%s must not count as an HTTP/3 path failure", name)
	}

	// A genuine failure still marks the authority: the guard above must not disable the memory.
	attempt++
	transport.recordH3AttemptFailure(request, authority, attempt, errors.New("quic: handshake failed"))
	require.True(t, transport.h3Broken(authority),
		"a real handshake failure must still advance the broken ladder")
}
