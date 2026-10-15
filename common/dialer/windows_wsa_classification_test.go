package dialer

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	E "github.com/sagernet/sing/common/exceptions"
)

// Attack 5, in the form the brief asks for: the WSA constants by name, through the exact wrapping
// chain a Windows net.Dialer produces.
//
// # Why the POSIX names in the main matrix are not enough
//
// The main classification test drives syscall.ENETUNREACH and friends, and on Windows those constants
// ARE the WSA values - but that equivalence is a property of the Go syscall package that nothing in
// this repo asserts, and the reported failure arrived as a Windows errno inside a nested wrap. A
// regression that broke the wrap walk, or a build where the constants diverge, would leave the matrix
// green and the real machine misclassified.
//
// So these cases are written with the WSA names and the WSA numeric values, and they are asserted on
// every platform: the values are fixed by Windows and the classifier is platform-independent, so a
// Windows-only test would be weaker than this one, not stronger.
//
// # What the classifier must never do
//
// familyHealth records one bit per network environment with a fifteen-second lifetime: "start both
// families together next time". The asymmetry is deliberate - a false positive costs one fallback
// delay, a false negative costs a timeout - but a WRONG positive is still wrong, and the one that
// matters is ECONNREFUSED: the peer answered, so the path demonstrably works, and blaming the family
// for it would penalise a healthy path because one server was down.
func TestWindowsWSAErrorChainIsClassified(t *testing.T) {
	// The WSA values come from PRODUCTION, not from a copy written here.
	//
	// # Why this no longer restates them
	//
	// The table below used to carry its own `const wsaENETUNREACH = syscall.Errno(10051)` block and
	// then drive the classifier with it. That is a test agreeing with itself: the numbers under test
	// were declared two lines up, so an edit to `windows_socket_errors.go` that changed a WSA value
	// would leave this test passing while the classifier compared the wrong number.
	//
	// The contract is now explicit - production's constants must BE these numbers - and it is
	// asserted below, so the two halves are checked against a third thing rather than against each
	// other.
	const (
		wantWSAENETUNREACH   = syscall.Errno(10051)
		wantWSAEHOSTUNREACH  = syscall.Errno(10065)
		wantWSAEADDRNOTAVAIL = syscall.Errno(10049)
		wantWSAENETDOWN      = syscall.Errno(10050)
		wantWSAECONNREFUSED  = syscall.Errno(10061)
		wantWSAETIMEDOUT     = syscall.Errno(10060)
		wantWSAECONNRESET    = syscall.Errno(10054)
		wantWSAECONNABORTED  = syscall.Errno(10053)
		wantWSAEPIPE         = syscall.Errno(10032)
	)
	require.Equal(t, wantWSAENETUNREACH, wsaENETUNREACH)
	require.Equal(t, wantWSAEHOSTUNREACH, wsaEHOSTUNREACH)
	require.Equal(t, wantWSAEADDRNOTAVAIL, wsaEADDRNOTAVAIL)
	require.Equal(t, wantWSAENETDOWN, wsaENETDOWN)
	require.Equal(t, wantWSAECONNREFUSED, wsaECONNREFUSED)
	require.Equal(t, wantWSAETIMEDOUT, wsaETIMEDOUT)

	// The peer-lifecycle triple, added by the Phase-4 sweep of the raw comparisons in
	// protocol/anytls and protocol/shadowtls: they are the same defect class, and they were the last
	// call sites still comparing POSIX spellings against what Windows delivers.
	require.Equal(t, wantWSAECONNRESET, wsaECONNRESET)
	require.Equal(t, wantWSAECONNABORTED, wsaECONNABORTED)
	require.Equal(t, wantWSAEPIPE, wsaEPIPE)

	// # The relationship that makes this test necessary
	//
	// On WINDOWS the two spellings are distinct, and asserting that here is what stops a future
	// "cleanup" from deleting the WSA arm:
	//
	//	syscall.ENETUNREACH   0x20000043  = 67 with the application-defined error bit
	//	WSAENETUNREACH        10051       = what net/fd_windows.go actually wraps
	//
	// It is asserted in windows_wsa_syscall_spelling_test.go, because it is a claim about THIS
	// platform's syscall constants and this file compiles everywhere. The version here spelled it
	// inline and made the whole test unpassable off Windows: Linux spells ENETUNREACH 0x65, so the
	// assertion failed there, and the failure read as a product defect rather than as a test that
	// could only ever pass on one platform. Verify's race step caught it on 2026-10-15:
	//
	//	windows_wsa_classification_test.go:69: expected: 0x65, actual: 0x20000043
	//
	// Everything that does NOT depend on the host's syscall table stays in this file, so the suite
	// this file belongs to keeps its coverage on every platform.
	testCases := []struct {
		name       string
		errno      syscall.Errno
		isPathFail bool
		why        string
	}{
		{
			name:       "WSAENETUNREACH",
			errno:      wsaENETUNREACH,
			isPathFail: true,
			why: "the exact error in the report. Windows returns it from connectex() when no route " +
				"exists for the destination family, and it is the strongest local signal available",
		},
		{
			name:       "WSAEHOSTUNREACH",
			errno:      wsaEHOSTUNREACH,
			isPathFail: true,
			why: "reachability failure for one host. It may be a routing problem for that host alone, " +
				"so the caution taken is the fifteen-second lifetime rather than the classification",
		},
		{
			name:       "WSAEADDRNOTAVAIL",
			errno:      wsaEADDRNOTAVAIL,
			isPathFail: true,
			why:        "no source address could be selected, which is a property of the family on this machine",
		},
		{
			name:       "WSAENETDOWN",
			errno:      wsaENETDOWN,
			isPathFail: true,
			why:        "the interface is down, so nothing was attempted on the wire",
		},
		{
			name:       "WSAECONNREFUSED",
			errno:      wsaECONNREFUSED,
			isPathFail: false,
			why: "THE ONE THAT MATTERS. The peer actively answered, which proves the path works end to " +
				"end. Recording it would penalise a healthy family because one server was down",
		},
		{
			name:       "WSAETIMEDOUT",
			errno:      wsaETIMEDOUT,
			isPathFail: false,
			why: "a bare errno is not a net.Error, so it is not treated as a timeout - the scheduler's " +
				"own deadline check is what classifies a real stall",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// The chain a Windows dial actually produces: net.Dialer returns *net.OpError wrapping
			// *os.SyscallError wrapping the errno.
			chain := &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "connectex", Err: testCase.errno},
			}

			require.Truef(t, testCase.isPathFail == isFamilyPathFailure(chain),
				"through the production Windows wrap: %s", testCase.why)

			// And through the layers this repo adds on top.
			require.Truef(t, testCase.isPathFail == isFamilyPathFailure(E.Cause(chain, "dial 2409:8a00::1:443")),
				"through E.Cause: %s", testCase.why)
			require.Truef(t, testCase.isPathFail == isFamilyPathFailure(E.Errors(chain)),
				"through E.Errors: %s", testCase.why)
		})
	}
}

// TestCallerTeardownIsNeverAFamilyFailure is the fourth classification the brief names, and the one
// that cost the fast-fallback path its effect the first time this code was written.
//
// "The caller stopped waiting" and "the network failed" are different facts, and a rule that only
// asked "did the attempt return an error" would blame the network for our own teardown. That is why
// the scheduler, not the classifier, owns this decision: only the scheduler knows whether the parent
// operation is still running.
func TestCallerTeardownIsNeverAFamilyFailure(t *testing.T) {
	// Through the same wrapping chain, because a teardown arrives wrapped too.
	for name, err := range map[string]error{
		"context.Canceled": context.Canceled,
		"net.ErrClosed":    net.ErrClosed,
	} {
		chain := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: err}}
		require.Falsef(t, isFamilyPathFailure(chain), "%s must not be recorded as a path failure", name)
		require.Falsef(t, isFamilyPathFailure(E.Cause(chain, "dial 2409:8a00::1")),
			"%s must not be recorded through E.Cause either", name)

		// And a sentinel teardown error is still matchable by the caller, which is what makes the
		// distinction usable rather than merely stated.
		require.Truef(t, errors.Is(chain, err), "%s must remain matchable through the wrap", name)
	}

	// # Deadline errors are the case the classifier deliberately does NOT own
	//
	// The tempting assertion - "a deadline is caller teardown, so record nothing" - is wrong, and this
	// is the second thing this round's attack corrected in its own tests rather than in the code.
	//
	// The same context.DeadlineExceeded value is produced by an attempt's OWN deadline, which is
	// genuine silence on the path, and by the parent operation's deadline, which is caller teardown.
	// The classifier cannot tell them apart because the error does not carry that information.
	//
	// So the classifier records a deadline as a path failure and the SCHEDULER decides, at the point
	// where it knows the answer, by checking whether the parent context is still alive. That guard is
	// asserted by TestSchedulerDoesNotRecordCallerCancellationUntilTheParentIsGone, which drives a real
	// parent cancellation; this test only pins the half the classifier owns.
	require.True(t, isFamilyPathFailure(&net.OpError{
		Op: "dial", Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: context.DeadlineExceeded},
	}), "a deadline IS a path signal at this layer: it means nothing answered")
	require.True(t, isFamilyPathFailure(&net.OpError{
		Op: "dial", Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: os.ErrDeadlineExceeded},
	}), "and an I/O deadline is the blackhole case this mechanism exists for")
}
