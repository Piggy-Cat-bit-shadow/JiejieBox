package adapter

// DialPath — the execution-time record of WHERE a dial actually went.
//
// # The gap this closes
//
// A DNS query event can name the transport that answered it, but a transport dials through a
// detour, and a detour may be an outbound group whose member is chosen at the instant of the dial.
// Reading that group's current selection AFTER the exchange would report a path the exchange never
// took: a selector that moved between the two reads makes the history attribute an answer to a node
// that never carried it. The only honest source is the dial itself, so the decision points record
// into a recorder carried by the context and the caller reads what actually happened.
//
// # Who records
//
//   - the DNS transport dialer boundaries its own dial (`BeginDial`) and names the detour it dials
//     through, or marks a direct dial when there is no detour;
//   - every outbound group records the member it hands the connection to (`Record`), so a chain of
//     nested groups appears in decision order and the last recorded member is the leaf;
//   - a connection pool that reuses an already established connection publishes that connection's
//     recorded chain (`Replay`), because "this exchange travelled the path the connection was
//     dialled on" is a fact about the exchange even though nothing was dialled for it.
//
// # Cost
//
// Nothing is allocated and nothing is recorded unless a caller installed a recorder: every lookup
// answers nil for a context without one, and every method on a nil *DialPath is a no-op. The DNS
// client installs one only while an event subscriber exists, so an unobserved process pays one
// context lookup per dial and nothing else.
//
// # What it deliberately does not do
//
// It is not a general request tracer. It records tags and the selection revision a group reports,
// hops are capped, and it does not follow a detour declared INSIDE an outbound's own dialer: that
// object dials with its own dialer, outside the boundary this recorder is opened at. A projection
// that claimed to cover it would be inventing hops it never saw.

import (
	"context"
	"sync"
)

// DialPathStep is one decision point on the path: the object a dial entered, and, when the object
// that chose it reports one, the selection revision it was chosen at.
type DialPathStep struct {
	// Tag is the outbound tag that took over the dial.
	Tag string
	// Revision is the selection revision of the object that chose Tag. It is 0 with HasRevision
	// false when that object has no revision, which is "unknown", not "revision zero".
	Revision    uint64
	HasRevision bool
}

// DialPathReason is the fixed vocabulary explaining why a dial path projection is incomplete. The
// values are stable strings because they travel through the event document to consumers.
type DialPathReason string

const (
	// DialPathReasonNone means nothing is missing.
	DialPathReasonNone DialPathReason = ""
	// DialPathReasonNoConnection means this exchange neither established nor reused a connection:
	// a cache hit, a joined in-flight query, or a locally produced verdict.
	DialPathReasonNoConnection DialPathReason = "no-connection"
	// DialPathReasonDirect means the dial went out directly: no configured outbound detour carried
	// it, so there is no outbound to name.
	DialPathReasonDirect DialPathReason = "direct-dial"
	// DialPathReasonReplayUnknown means the exchange reused a pooled connection whose dial path was
	// not recorded when it was established (a transport whose pool predates this projection).
	DialPathReasonReplayUnknown DialPathReason = "reused-connection-untracked"
	// DialPathReasonUnobserved means a dial ran but no decision point reported a hop. It is an
	// honest "the boundary was crossed and nothing filled it in", never a silent empty path.
	DialPathReasonUnobserved DialPathReason = "dial-unobserved"
	// DialPathReasonTransportFanOut means a DNS transport group asked several member transports and
	// no single member can be named as the one that answered.
	DialPathReasonTransportFanOut DialPathReason = "transport-fan-out"
)

// maxDialPathSteps bounds a recorded chain.
//
// The bound exists for the same reason every other diagnostic here has one: the recorder is reached
// from a request path, and a pathological configuration (or a loop the graph validator did not
// catch) must not be able to grow an event. A real chain is a detour plus a handful of nested
// groups; eight is well past that, and reaching it is reported rather than silently truncated.
const maxDialPathSteps = 8

// DialPath is one exchange's dial record. A nil *DialPath is valid and records nothing.
type DialPath struct {
	access sync.Mutex

	// dials counts connection-establishment attempts made under this record.
	dials int
	// replayed reports that an existing connection's chain was published into this record.
	replayed bool
	// direct reports that at least one dial went out without a configured outbound.
	direct bool

	steps     []DialPathStep
	truncated bool

	// transportGroup/transportMember name the member of a DNS transport group that ran the
	// exchange. It is a separate fact from the outbound chain: the group chooses a TRANSPORT, and
	// that transport then dials through its own outbound path.
	transportGroup  string
	transportMember string
	transportFanOut bool
}

// DialPathResult is an immutable read of one record.
type DialPathResult struct {
	Dials           int
	Steps           []DialPathStep
	Truncated       bool
	Replayed        bool
	Direct          bool
	TransportGroup  string
	TransportMember string
	TransportFanOut bool
}

// NewDialPath returns an empty recorder.
func NewDialPath() *DialPath {
	return &DialPath{}
}

type dialPathContextKey struct{}

// WithDialPath returns a context carrying path. A nil path returns ctx unchanged, so a caller can
// write the wrapper unconditionally.
func WithDialPath(ctx context.Context, path *DialPath) context.Context {
	if path == nil {
		return ctx
	}
	return context.WithValue(ctx, dialPathContextKey{}, path)
}

// DialPathFromContext returns the recorder installed on ctx, or nil.
func DialPathFromContext(ctx context.Context) *DialPath {
	if ctx == nil {
		return nil
	}
	path, _ := ctx.Value(dialPathContextKey{}).(*DialPath)
	return path
}

// BeginDial starts a new connection-establishment attempt: the previous attempt's steps no longer
// describe the connection this attempt is about to make. The last attempt is the one that carries
// the exchange, so earlier ones are dropped rather than accumulated.
//
// It is called at the boundary that owns the dial (the DNS transport dialer), not by every object
// on the way: an outbound group does not establish a connection, it hands the existing attempt on.
func (p *DialPath) BeginDial() {
	if p == nil {
		return
	}
	p.access.Lock()
	p.dials++
	p.steps = p.steps[:0]
	p.truncated = false
	p.access.Unlock()
}

// MarkDirect records that the attempt went out without a configured outbound detour.
func (p *DialPath) MarkDirect() {
	if p == nil {
		return
	}
	p.access.Lock()
	p.direct = true
	p.access.Unlock()
}

// Record appends the object a dial was handed to.
func (p *DialPath) Record(tag string, revision uint64, hasRevision bool) {
	if p == nil || tag == "" {
		return
	}
	p.access.Lock()
	defer p.access.Unlock()
	if len(p.steps) >= maxDialPathSteps {
		p.truncated = true
		return
	}
	p.steps = append(p.steps, DialPathStep{Tag: tag, Revision: revision, HasRevision: hasRevision})
}

// Replay publishes the chain of a connection this exchange is REUSING.
//
// It is not a dial: no attempt is counted, because the connection was established by an earlier
// exchange and the attribution fact is "this exchange travelled that connection's path". A recorded
// attempt made later in the same exchange (a retry on a fresh socket) supersedes it through
// BeginDial.
func (p *DialPath) Replay(steps []DialPathStep) {
	if p == nil {
		return
	}
	p.access.Lock()
	defer p.access.Unlock()
	p.replayed = true
	p.steps = append(p.steps[:0], steps...)
	if len(p.steps) > maxDialPathSteps {
		p.steps = p.steps[:maxDialPathSteps]
		p.truncated = true
	}
}

// RecordTransportMember names the member of a DNS transport group that ran the exchange.
func (p *DialPath) RecordTransportMember(group string, member string) {
	if p == nil {
		return
	}
	p.access.Lock()
	p.transportGroup = group
	p.transportMember = member
	p.access.Unlock()
}

// MarkTransportFanOut records that a DNS transport group split the query across member transports,
// so no single member can be named.
func (p *DialPath) MarkTransportFanOut() {
	if p == nil {
		return
	}
	p.access.Lock()
	p.transportFanOut = true
	p.access.Unlock()
}

// Snapshot copies the current chain, for a connection that must carry its own attribution after the
// exchange that dialled it has ended.
func (p *DialPath) Snapshot() []DialPathStep {
	if p == nil {
		return nil
	}
	p.access.Lock()
	defer p.access.Unlock()
	if len(p.steps) == 0 {
		return nil
	}
	return append([]DialPathStep(nil), p.steps...)
}

// Result returns an immutable copy of the record.
func (p *DialPath) Result() DialPathResult {
	if p == nil {
		return DialPathResult{}
	}
	p.access.Lock()
	defer p.access.Unlock()
	return DialPathResult{
		Dials:           p.dials,
		Steps:           append([]DialPathStep(nil), p.steps...),
		Truncated:       p.truncated,
		Replayed:        p.replayed,
		Direct:          p.direct,
		TransportGroup:  p.transportGroup,
		TransportMember: p.transportMember,
		TransportFanOut: p.transportFanOut,
	}
}

// Leaf returns the last object on the chain: the one that actually carried the connection.
func (r DialPathResult) Leaf() (DialPathStep, bool) {
	if len(r.Steps) == 0 {
		return DialPathStep{}, false
	}
	return r.Steps[len(r.Steps)-1], true
}

// Reason reports why the projection is incomplete, or DialPathReasonNone.
func (r DialPathResult) Reason() DialPathReason {
	if len(r.Steps) > 0 {
		return DialPathReasonNone
	}
	switch {
	case r.Dials == 0 && !r.Replayed:
		return DialPathReasonNoConnection
	case r.Direct:
		return DialPathReasonDirect
	case r.Replayed:
		return DialPathReasonReplayUnknown
	default:
		return DialPathReasonUnobserved
	}
}

// ReplayDialPath publishes a connection's recorded chain into the recorder carried by ctx, if any.
//
// It is the pool's way of saying "this exchange reused a connection that was dialled on that path":
// the connection outlives the exchange, so the path is read from the connection and written into the
// exchange's own record.
func ReplayDialPath(ctx context.Context, steps []DialPathStep) {
	if path := DialPathFromContext(ctx); path != nil {
		path.Replay(steps)
	}
}

// RecordDialStep records one group's choice into the recorder carried by ctx, if any.
//
// It takes the group itself so the step carries the group's selection revision: the revision is a
// property of the DECISION, and only the object that made it can report it. It is nil-safe on both
// sides, so a decision point can call it unconditionally.
func RecordDialStep(ctx context.Context, group Outbound, member Outbound) {
	path := DialPathFromContext(ctx)
	if path == nil || group == nil || member == nil {
		return
	}
	revision, hasRevision := uint64(0), false
	if reporter, isReporter := group.(DialPathRevisionReporter); isReporter {
		revision, hasRevision = reporter.DialPathRevision(), true
	}
	path.Record(member.Tag(), revision, hasRevision)
}

// DialPathRevisionReporter is implemented by a group whose selection has a revision.
//
// It is a separate interface rather than a type assertion on a method named Revision: an outbound
// implementing some unrelated Revision() must not be read as a selection generation, and a bogus
// number in an attribution field is worse than an absent one.
type DialPathRevisionReporter interface {
	DialPathRevision() uint64
}
