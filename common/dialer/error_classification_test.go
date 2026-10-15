package dialer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Layer 6 of the debug round: error classification, and the wrapping that makes it hard.
//
// # What the classifier decides, and what it must never decide
//
// familyHealth records ONE bit per network environment: "this family recently failed to establish a
// connection, so start both families together next time". It is a scheduling nudge with a
// fifteen-second lifetime - not a verdict - so the cost of a false positive is bounded and the cost
// of a false negative is one extra fallback delay.
//
// That asymmetry is why the classifier refuses everything it cannot read confidently: a TLS alert or
// an HTTP 5xx means the network DELIVERED PACKETS and the peer ANSWERED, which is evidence the family
// WORKS, and recording it as a path failure would penalise a healthy path because one server
// misbehaved.
//
// # Why wrapping is the interesting part
//
// Every one of these errors reaches this function wrapped. The attempt path wraps with the address
// (`E.Cause(err, "dial ", address)`), the scheduler aggregates several with E.Errors, and the syscall
// layer wraps with *os.SyscallError and *net.OpError on the way up. A classifier that matched on the
// message - or on the concrete error type - would work in its own unit test and fail in production,
// so every case below is asserted through at least one layer of wrapping, and the wrapped forms are
// built the way production builds them.

func TestErrorClassificationThroughProductionWrapping(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		isPathFail bool
		why        string
	}{
		{
			name:       "ENETUNREACH",
			err:        syscall.ENETUNREACH,
			isPathFail: true,
			why:        "the kernel reporting that no route exists for this family is the strongest local signal there is",
		},
		{
			name:       "EHOSTUNREACH",
			err:        syscall.EHOSTUNREACH,
			isPathFail: true,
			why: "the brief asks for caution here, and the caution taken is the LIFETIME rather than the " +
				"classification: it may be a routing problem for one host, so it earns a fifteen-second " +
				"bias and never a verdict",
		},
		{
			name:       "EADDRNOTAVAIL",
			err:        syscall.EADDRNOTAVAIL,
			isPathFail: true,
			why:        "no source address could be selected, which is a property of the family on this machine",
		},
		{
			name:       "ENETDOWN",
			err:        syscall.ENETDOWN,
			isPathFail: true,
			why:        "the interface is down, so nothing was attempted on the wire",
		},
		{
			name:       "connection refused",
			err:        syscall.ECONNREFUSED,
			isPathFail: false,
			why:        "the peer actively answered: the path demonstrably works",
		},
		{
			name:       "caller context canceled",
			err:        context.Canceled,
			isPathFail: false,
			why:        "the CALLER stopped waiting, which says nothing about any family",
		},
		{
			name:       "parent deadline exceeded",
			err:        context.DeadlineExceeded,
			isPathFail: true,
			why:        "a bare deadline reaching here is an attempt's OWN timeout, which is the blackhole case the mechanism exists for",
		},
		{
			name:       "loser closed after a sibling won",
			err:        net.ErrClosed,
			isPathFail: false,
			why:        "a losing attempt is torn down by us; that is not the network failing",
		},
		{
			name:       "peer closed before answering",
			err:        io.EOF,
			isPathFail: false,
			why: "indistinguishable from a server that accepts and immediately hangs up, which is a " +
				"server problem and not a broken family",
		},
		{
			name:       "TLS alert",
			err:        errors.New("remote error: tls: handshake failure"),
			isPathFail: false,
			why:        "the network carried a complete handshake; the peer rejected it",
		},
		{
			name:       "certificate failure",
			err:        errors.New("x509: certificate signed by unknown authority"),
			isPathFail: false,
			why:        "an application-layer verdict about an identity, not about a path",
		},
		{
			name:       "HTTP 503",
			err:        errors.New("unexpected status: 503 Service Unavailable"),
			isPathFail: false,
			why:        "the request was carried end to end and answered",
		},
		{
			name:       "QUIC handshake failure",
			err:        errors.New("CRYPTO_ERROR 0x128 (remote): tls: bad certificate"),
			isPathFail: false,
			why: "a handshake that completed far enough to produce a remote CRYPTO_ERROR proves the " +
				"datagrams flowed; the executor decides what QUIC success means, and this is not a path failure",
		},
		{
			name:       "DNS NODATA reaching a dial error path",
			err:        errors.New("no such host"),
			isPathFail: false,
			why:        "a name with no answer is a fact about the NAME; it must never be recorded as a family failure",
		},
		{
			name:       "unclassified error",
			err:        errors.New("something we have never seen"),
			isPathFail: false,
			why:        "the deliberate fail-closed default: no state is recorded",
		},
		{
			name:       "nil",
			err:        nil,
			isPathFail: false,
			why:        "no error is not a failure",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Bare form.
			require.Equalf(t, testCase.isPathFail, isFamilyPathFailure(testCase.err), "bare: %s", testCase.why)

			if testCase.err == nil {
				return
			}
			// The production wrapping chain: the syscall error wrapped by the OS layer, then by the
			// dial, then attributed to an address by the scheduler.
			syscallWrapped := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: testCase.err}}
			require.Equalf(t, testCase.isPathFail, isFamilyPathFailure(syscallWrapped),
				"through *net.OpError -> *os.SyscallError: %s", testCase.why)

			addressWrapped := E.Cause(syscallWrapped, "dial 2001:db8::1")
			require.Equalf(t, testCase.isPathFail, isFamilyPathFailure(addressWrapped),
				"through E.Cause: %s", testCase.why)

			aggregated := E.Errors(addressWrapped, E.New("dial 192.0.2.1: connection refused"))
			require.Equalf(t, testCase.isPathFail, isFamilyPathFailure(aggregated),
				"through E.Errors (first error decides): %s", testCase.why)

			deeplyWrapped := fmt.Errorf("attempt failed: %w", aggregated)
			require.Equalf(t, testCase.isPathFail, isFamilyPathFailure(deeplyWrapped),
				"through fmt.Errorf %%w: %s", testCase.why)
		})
	}
}

// TestTimeoutIsClassifiedByTypeNotByMessage pins the reason the classifier uses net.Error rather than
// a string.
//
// The same DeadlineExceeded value is produced by an attempt's own deadline - a genuine path signal -
// and by the parent operation's deadline - caller cancellation, which must record nothing. The
// classifier therefore cannot decide from the error alone; the SCHEDULER decides by checking whether
// the parent context is still alive, at the point where it knows the answer. This test pins the half
// the classifier owns: a timeout-typed error is a path failure, so the scheduler's guard is the only
// thing standing between a caller's deadline and a poisoned family.
func TestTimeoutIsClassifiedByTypeNotByMessage(t *testing.T) {
	require.True(t, isFamilyPathFailure(&net.OpError{Op: "dial", Err: timeoutBlackhole{}}),
		"a timeout-typed error is the blackhole case the mechanism exists for")

	// An error whose message SAYS timeout but whose type does not is not a timeout. Matching text
	// would classify it, and text is exactly what a wrapped protocol error can contain by accident.
	require.False(t, isFamilyPathFailure(errors.New("i/o timeout")),
		"a bare error string is not a net.Error, so it is not treated as a timeout")
}

// TestSchedulerDoesNotRecordCallerCancellationUntilTheParentIsGone is the other half, at the level
// that can actually see it.
//
// The scheduler records a failure only while the PARENT operation is still running. A losing attempt
// unwinds with context.Canceled once the winner has been chosen, and recording that would blame the
// network for our own teardown - which is what a naive "the attempt returned an error" rule does, and
// what made the fast-fallback path inert in production the first time.
func TestSchedulerDoesNotRecordCallerCancellationUntilTheParentIsGone(t *testing.T) {
	health := newFamilyHealth()
	const environment = 1

	// Winner path: IPv6 blackholes, IPv4 wins - but only AFTER a delay longer than the fallback
	// interval, so IPv6 has genuinely been outstanding long enough to count as stalled rather than
	// merely not-yet-finished. A candidate that started a millisecond ago has not stalled, and the
	// scheduler's minimum-pending rule is what keeps a fast healthy race from manufacturing a penalty
	// out of nothing.
	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{address6: true}
	inner.connectDelay = 60 * time.Millisecond
	scheduler := &candidateScheduler{
		fallbackDelay:      20 * time.Millisecond,
		health:             health,
		networkEnvironment: environment,
	}
	_, winner, err := scheduler.dial(context.Background(),
		planCandidates([]netip.Addr{address6, address4}, netip.Addr{}, 0),
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return inner.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.Equal(t, address4, winner)

	// The recorded state is the SOFT one, so the next connection starts both families together. It is
	// NOT a "failure" the way a kernel path error is, which is why the two are recorded through
	// different entry points - and the IPv6 attempt unwound with context.Canceled, which records
	// nothing on its own.
	require.True(t, health.fallbackImmediately(environment, familyIPv6, familyIPv4),
		"the stall must be learned, so the next connection does not pay the delay again")
}
