package dialer

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every Winsock code this package matches must be a code that exists.
//
// # The defect this guards against
//
// `wsaEPIPE = 10032` sat in the peer-lifecycle set alongside `wsaECONNRESET` and `wsaECONNABORTED`,
// documented as completing "the peer-lifecycle triple". Winsock defines no WSAEPIPE, and 10032 falls in
// the unassigned gap between WSAEMFILE (10024) and WSAEWOULDBLOCK (10035), so the entry could never
// match a real error.
//
// A constant like that is not merely dead. It is a claim: anyone maintaining these tables reads the set
// as the enumeration of cases that are handled, and one fabricated member means the table looks complete
// while a real code is missing. The value was also asserted by a test, so the fabricated number had a
// green check next to it.
//
// # Why the range check rather than an exact list
//
// Winsock error codes are 10000-11999 in practice, and the SDK leaves 10025-10034 unassigned. A check
// against the unassigned gap catches the shape that occurred - a plausible-looking low number chosen
// because it was near its neighbours - without pinning a list that a legitimate new entry would have to
// edit. POSIX spellings are excluded from the check by range: on Windows `syscall.ENETUNREACH` is
// 0x20000043 (the application-defined bit), far outside the Winsock band, and those are matched
// deliberately because both spellings appear in the wild.
func TestPeerLifecycleErrnosAreNotFabricated(t *testing.T) {
	const (
		winsockLow = 10000
		// The unassigned gap in winsock2.h: WSABASEERR+24 .. WSABASEERR+34.
		unassignedLow  = 10025
		unassignedHigh = 10034
	)

	sets := map[string][]syscall.Errno{
		"PathFailureErrnos": PathFailureErrnos,
		"PeerGoneErrnos":    PeerGoneErrnos,
		"TimeoutErrnos":     TimeoutErrnos,
	}

	checked := 0
	for name, errnos := range sets {
		for _, errno := range errnos {
			value := int(errno)
			if value < winsockLow || value >= winsockLow+2000 {
				// A POSIX spelling, or a plain errno; not a Winsock code.
				continue
			}
			checked++
			require.Falsef(t, value >= unassignedLow && value <= unassignedHigh,
				"%s contains %d, which lies in the Winsock unassigned gap %d..%d: no Windows call "+
					"returns it, so the entry advertises a case that cannot occur",
				name, value, unassignedLow, unassignedHigh)
		}
	}
	require.NotZero(t, checked,
		"the check must actually examine Winsock codes, or it passes without looking at anything")
}
