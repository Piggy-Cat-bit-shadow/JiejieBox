package trafficclass

import "context"

// classKey carries the resolved class of the flow a dial belongs to.
//
// # Why the class travels in the context
//
// The class is decided once, at the route layer, from the inbound metadata. Everything below that
// point - the outbound's dial, its admission seam, its retry - only receives a context, so a
// context value is the one channel that reaches all of them without threading a new parameter
// through every Dialer implementation.
//
// It is a value on the dial's own context, so it cannot leak to another flow: the dial either
// inherits it from the connection's context or it does not have one.
type classKey struct{}

// WithClass returns a context that reports the flow's resolved class.
func WithClass(ctx context.Context, class Class) context.Context {
	return context.WithValue(ctx, classKey{}, class)
}

// ClassFromContext reports the flow's resolved class, and whether one was set at all.
//
// The second return value exists so a consumer can distinguish "unclassified" from "explicitly
// default": the admission policy treats both the same today, but a diagnostic that printed
// "default" for a flow nobody classified would be lying about what it knows.
func ClassFromContext(ctx context.Context) (Class, bool) {
	class, loaded := ctx.Value(classKey{}).(Class)
	return class, loaded
}
