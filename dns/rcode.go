package dns

import (
	"errors"

	mDNS "github.com/miekg/dns"
)

const (
	RcodeSuccess       RcodeError = mDNS.RcodeSuccess
	RcodeServerFailure RcodeError = mDNS.RcodeServerFailure
	RcodeFormatError   RcodeError = mDNS.RcodeFormatError
	RcodeNameError     RcodeError = mDNS.RcodeNameError
	RcodeRefused       RcodeError = mDNS.RcodeRefused
)

type RcodeError int

func (e RcodeError) Error() string {
	return mDNS.RcodeToString[int(e)]
}

// IsNXDomain reports whether err is a DNS NXDOMAIN verdict, including a wrapped or aggregated one.
//
// # Why this is not errors.Is alone
//
// A lookup can return a multi-error: the dual-stack path joins the A and AAAA outcomes, and the
// dialer wraps that in its own context. `errors.Is(err, RcodeNameError)` is true when ANY part is
// NXDOMAIN, which would demote a genuine failure as soon as it shared an error tree with a normal
// negative answer - one family NXDOMAIN and the other SERVFAIL/timeout is still a failure an
// operator has to see. The predicate is therefore the stronger one: every leaf must be the
// NXDOMAIN rcode. A pure negative answer, one family or both, is the normal case this exists for.
//
// The check is on the error TYPE (RcodeError) at the leaf, never on the text "NXDOMAIN": a
// transport or an unrelated error that happens to spell that word is not a DNS verdict.
func IsNXDomain(err error) bool {
	return allNXDomain(err)
}

func allNXDomain(err error) bool {
	if err == nil {
		return false
	}
	switch value := err.(type) {
	case interface{ Unwrap() []error }:
		parts := value.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !allNXDomain(part) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		if next := value.Unwrap(); next != nil {
			return allNXDomain(next)
		}
	}
	var rcode RcodeError
	return errors.As(err, &rcode) && rcode == RcodeNameError
}
