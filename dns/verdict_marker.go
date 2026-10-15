package dns

import (
	"errors"
)

// ErrLocalVerdict marks an RcodeError that this router PRODUCED from a rule action rather than
// received from an upstream server.
//
// # Why the distinction has to exist
//
// Both are RcodeErrors and both must keep behaving as such: a caller asks "did this name resolve",
// and "no" is spelled RcodeNameError either way. What differs is whether the answer is a FAULT.
//
//	an upstream NXDOMAIN      the lookup was attempted and the name genuinely does not exist, or
//	                          the server failed us - something an operator may need to act on
//	a local action verdict    the configuration said "do not resolve this name", which is the
//	                          feature working exactly as configured
//
// Without the marker the router cannot tell them apart, and it logged both at ERROR level. For the
// rule this fork added for its own most common case that means a dialer lookup of an adblocked name
// printed `lookup failed for <domain>` for every connection that touched it. The module documenting
// the action reasons explicitly about not making log I/O the new cost centre for blocked queries;
// an ERROR line per blocked dial is exactly that cost, and it also trains an operator to ignore a
// line that should mean something.
//
// # Why it is a marker on the error rather than a new error value
//
// Callers already match the rcode - `errors.Is(err, RcodeError(mDNS.RcodeNameError))` and friends -
// and that must not change for a verdict that is still, precisely, that rcode. So the error's
// MESSAGE is left exactly as it was (the rcode's own string) and only its identity is widened: it
// answers `Is` for the rcode it carries and for this sentinel.
//
// Changing the message instead would have been the smaller diff and the worse change: error text
// reaches logs and, on some paths, a user-visible refusal, and "NXDOMAIN: resolved by a local rule"
// is not what a caller of a DNS lookup should read.
var ErrLocalVerdict = errors.New("resolved by a local rule")

// localVerdictError is an RcodeError produced by a rule action.
type localVerdictError struct {
	rcode RcodeError
}

// Error returns the rcode's own string, so every existing log line and error message is unchanged.
func (e localVerdictError) Error() string { return e.rcode.Error() }

// Is answers both facts this error carries: the rcode it stands for, and that a LOCAL rule produced
// it.
//
// The rcode arm is deliberately explicit rather than relying on Unwrap, so both facts are visible in
// one place and a later change to Unwrap cannot silently drop the first.
func (e localVerdictError) Is(target error) bool {
	if errors.Is(target, ErrLocalVerdict) {
		return true
	}
	rcode, isRcode := target.(RcodeError)
	return isRcode && rcode == e.rcode
}

// Unwrap exposes the rcode, so errors.As/errors.Is find it through any wrapper this value is later
// placed inside - E.Cause and E.Errors both walk the chain.
func (e localVerdictError) Unwrap() error { return e.rcode }

// LocalVerdictRcode marks an rcode as produced by a local rule action.
//
// It is the single place the marker is attached, so "which errors are verdicts" has one answer
// rather than one per call site.
func LocalVerdictRcode(rcode RcodeError) error {
	return localVerdictError{rcode: rcode}
}

// IsLocalVerdict reports whether an error is a rule action's verdict rather than an upstream answer
// or a failure.
//
// # Why it is not a bare errors.Is
//
// The dialer path does not hand the router one error. A dual-stack lookup runs both families and
// aggregates their outcomes, so what arrives here is a multi-error whose parts are the per-family
// results. `errors.Is` would answer "yes" for that whenever ANY part was a verdict, which would
// demote a genuine failure to Debug as soon as it shared an error tree with a blocked family - the
// one way this marker could hide information instead of clarifying it.
//
// So the question asked is the stronger one: does this error consist ENTIRELY of verdicts? A pure
// verdict (one family, or both families blocked) is a configured answer; anything with a non-verdict
// part is not, and stays a failure.
//
// It walks the tree itself rather than using errors.Is/As because it needs to see every leaf, which
// those deliberately stop short of.
func IsLocalVerdict(err error) bool {
	return allLocalVerdict(err)
}

// allLocalVerdict reports whether err is a verdict or is COMPOSED ONLY of verdicts.
func allLocalVerdict(err error) bool {
	switch value := err.(type) {
	case nil:
		return false
	case localVerdictError:
		// The verdict itself. It is the leaf this walk is looking for, and it is matched by type
		// rather than by errors.Is so that its own wrapped rcode is not mistaken for a part that
		// failed the test below.
		return true
	case interface{ Unwrap() []error }:
		parts := value.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !allLocalVerdict(part) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return allLocalVerdict(value.Unwrap())
	default:
		// A plain error, and therefore not a verdict.
		return false
	}
}

// The marker must not disturb the rcode contract, so the fact callers depend on is asserted here
// rather than left to the two call sites that use it.
var _ error = LocalVerdictRcode(RcodeNameError)
