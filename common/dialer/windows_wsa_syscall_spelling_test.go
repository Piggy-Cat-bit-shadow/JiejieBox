//go:build windows

package dialer

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWindowsSyscallSpellingsDifferFromTheWSAOnes asserts, on WINDOWS ONLY, the fact that makes the
// whole WSA arm necessary.
//
// # The relationship
//
//	syscall.ENETUNREACH   0x20000043  = 67 with the application-defined error bit
//	WSAENETUNREACH        10051       = what net/fd_windows.go actually wraps
//
// They are distinct numbers, so a classifier that only knew the syscall constants matched nothing at
// all on Windows. That is the defect `common/dialer/windows_socket_errors.go` exists to fix, and this
// assertion is what forces a future "cleanup" that removes the WSA arm to delete it deliberately.
//
// # Why it is a separate, platform-tagged file
//
// It is a claim about THIS platform's syscall constants, and it cannot be true anywhere else. It used
// to live inline in `windows_wsa_classification_test.go`, which has no build tag - so on Linux the
// same assertion compared 0x65 against 0x20000000|67 and failed, taking the whole WSA classification
// suite with it. Verify's race step reported it on 2026-10-15:
//
//	windows_wsa_classification_test.go:69: expected: 0x65, actual: 0x20000043
//
// That failure was the test being unportable, not the product being wrong: the classifier under test
// is one implementation on every platform, and only this one assertion is about the host.
//
// The tag matters in the other direction too. A test that exists but is executed by nothing is worse
// than no test, so this file must not be the ONLY place the WSA contract is checked: the numeric
// contract (production's constants must BE 10051, 10065, ...) is asserted untagged in
// `windows_wsa_classification_test.go`, where it runs everywhere. This file adds the host-specific
// half on the one host where it is meaningful.
func TestWindowsSyscallSpellingsDifferFromTheWSAOnes(t *testing.T) {
	require.NotEqual(t, uintptr(wsaENETUNREACH), uintptr(syscall.ENETUNREACH),
		"the two spellings are different numbers on Windows; an implementation that assumes they are "+
			"equal is matching a value that never reaches it")
	require.NotEqual(t, uintptr(wsaECONNREFUSED), uintptr(syscall.ECONNREFUSED),
		"including the one that must be EXCLUDED: a missed refusal blames a healthy family for a "+
			"server being down")

	// What the syscall constants actually are, so the distinction is documented rather than asserted
	// as a bare inequality. These are the values on Windows only - Linux spells ENETUNREACH as 0x65 -
	// which is the whole reason this file is tagged.
	require.Equal(t, uintptr(syscall.ENETUNREACH), uintptr(0x20000000|67))
	require.Equal(t, uintptr(67), uintptr(syscall.ENETUNREACH)&0xFF)

	// Every value this file's sibling asserts numerically must also be DISTINCT from its POSIX
	// spelling here, or the WSA arm would be redundant for that one condition. Stated as a loop so a
	// new set entry is covered by adding it in one place.
	for _, testCase := range []struct {
		name  string
		wsa   syscall.Errno
		posix syscall.Errno
	}{
		{"ENETUNREACH", wsaENETUNREACH, syscall.ENETUNREACH},
		{"EHOSTUNREACH", wsaEHOSTUNREACH, syscall.EHOSTUNREACH},
		{"EADDRNOTAVAIL", wsaEADDRNOTAVAIL, syscall.EADDRNOTAVAIL},
		{"ENETDOWN", wsaENETDOWN, syscall.ENETDOWN},
		{"ECONNREFUSED", wsaECONNREFUSED, syscall.ECONNREFUSED},
		{"ETIMEDOUT", wsaETIMEDOUT, syscall.ETIMEDOUT},
		{"ECONNRESET", wsaECONNRESET, syscall.ECONNRESET},
		{"ECONNABORTED", wsaECONNABORTED, syscall.ECONNABORTED},
		// No EPIPE row. `wsaEPIPE = 10032` was removed because Winsock defines no such code and the
		// number is unassigned, so the row asserted that two numbers differ - which they do, trivially,
		// for a code that can never occur.
	} {
		require.NotEqual(t, uintptr(testCase.wsa), uintptr(testCase.posix),
			"%s: the WSA value and the syscall value are the same number on this platform, so the "+
				"WSA arm of the classifier is redundant for it - either the platform changed or the "+
				"constant is wrong", testCase.name)
	}
}
