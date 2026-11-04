package resourceguard

import "context"

// heldPermitKey marks a context whose dial already holds an admission permit.
//
// # Why this exists
//
// Admission has to sit at the most expensive point, and for a TLS outbound there are two
// candidate points stacked on one dial: the outbound's own dial seam (AnyTLS takes one there) and
// the TLS handshake itself (shared by every TLS outbound through common/tls). Admitting at both
// would charge one handshake twice: the permit count would fall to half the configured limit,
// two queues would form where one is intended, and under saturation the outer holder would wait
// for an inner slot its own cohort is holding.
//
// The marker makes the inner gate remember that this dial is already covered. It is context
// scoped, so it follows exactly one dial down through whatever dialer adapters and retries that
// dial performs, and it cannot leak to an unrelated goroutine.
type heldPermitKey struct{}

// WithHeldPermit returns a context that tells a nested admission point that this dial already
// holds a permit. It is set by the outermost admission seam around the operation it admits.
func WithHeldPermit(ctx context.Context) context.Context {
	return context.WithValue(ctx, heldPermitKey{}, true)
}

// HasHeldPermit reports whether this dial is already admitted by an outer seam.
func HasHeldPermit(ctx context.Context) bool {
	held, _ := ctx.Value(heldPermitKey{}).(bool)
	return held
}
