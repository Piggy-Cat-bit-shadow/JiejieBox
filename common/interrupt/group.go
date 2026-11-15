package interrupt

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing/common/x/list"
)

// Group owns the two kinds of work a group selection change must be able to end:
//
//   - established connections, added with NewConn/NewPacketConn/Add. A switch closes the ones the
//     interrupt_exist_connections setting covers.
//   - dials that have not produced a connection yet, begun with BeginDial. A SELECTION change
//     (InterruptSelection) cancels them whatever that setting says: they are not established
//     traffic, their member is the one being switched away from, and a dial stuck on a dead node is
//     exactly what makes a switch appear to do nothing while the old request waits for its own
//     timeout (or forever, when it has none).
//
// The hand-off from a pending dial to an established connection is ATOMIC: Dial.CompleteConn and
// Dial.CompletePacketConn remove the pending entry and register the connection under one lock, so a
// switch either observes the dial as pending (and cancels it before it can be delivered) or observes
// the connection as established (and applies the setting to it). There is no window in which the
// switch has scanned the established list and the connection is then appended behind it.
//
// A provider republication that replaces the object behind the SAME selected tag is not a selection
// change (plain Interrupt): the dial's lease keeps the old object open, and cancelling the dial
// would turn a routine subscription refresh into a failed connection. That is why the entry points
// are separate.
//
// The established-connection half is the same for both: "interrupt_exist_connections = false" is a
// promise about flows that already carry traffic, not a promise to keep a half-finished handshake
// alive.
type Group struct {
	access      sync.Mutex
	connections list.List[*groupConnItem]
	dials       map[uint64]*Dial
	nextDial    uint64
	// revision counts selection changes. A dial records the revision it selected its member under,
	// and BeginDial refuses a revision that is already stale, so a dial that began in the instant of
	// a switch cannot silently proceed on the old member.
	revision uint64
}

type groupConnItem struct {
	conn       io.Closer
	isExternal bool
}

// Dial is one in-flight setup. Its Context bounds the dial; CompleteConn/CompletePacketConn is the
// only way to hand the resulting connection to the group. Every path must call Release exactly once
// (a defer is the natural shape); after a successful completion Release only cancels the now-dead
// setup context.
type Dial struct {
	group  *Group
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
	// finished is guarded by group.access. Complete and Release both set it; whichever wins does
	// the detach, and the loser becomes a no-op.
	finished bool
}

func NewGroup() *Group {
	return &Group{}
}

// Revision reports the current selection revision. A caller reads it BEFORE selecting a member and
// passes it to BeginDial; a selection change that happened in between makes BeginDial report stale.
func (g *Group) Revision() uint64 {
	g.access.Lock()
	defer g.access.Unlock()
	return g.revision
}

// BeginDial derives the setup context for one dial. stale reports that the revision the caller
// selected under is no longer current: the caller must re-select instead of dialing the member it
// already chose. A non-stale result is always a *Dial that must be released.
func (g *Group) BeginDial(ctx context.Context, revision uint64) (dial *Dial, stale bool) {
	g.access.Lock()
	if revision != g.revision {
		g.access.Unlock()
		return nil, true
	}
	if g.dials == nil {
		g.dials = make(map[uint64]*Dial)
	}
	dialCtx, cancel := context.WithCancel(ctx)
	g.nextDial++
	created := &Dial{
		group:  g,
		id:     g.nextDial,
		ctx:    dialCtx,
		cancel: cancel,
	}
	g.dials[created.id] = created
	g.access.Unlock()
	return created, false
}

// Context is the context the member's dial must run with.
func (d *Dial) Context() context.Context {
	if d == nil {
		return context.Background()
	}
	return d.ctx
}

// Release drops the pending entry (if it is still there) and cancels the setup context. It is
// idempotent and must be called on every path, including after a successful completion.
func (d *Dial) Release() {
	if d == nil {
		return
	}
	g := d.group
	g.access.Lock()
	if !d.finished {
		d.finished = true
		if g.dials[d.id] == d {
			delete(g.dials, d.id)
		}
	}
	g.access.Unlock()
	d.cancel()
}

// CompleteConn atomically ends the pending setup and registers the connection as established. If a
// selection change won the race, the connection is closed and the caller gets the cancellation; it
// must not be delivered to the user. If the caller's own setup context ended, the same applies.
func (d *Dial) CompleteConn(conn net.Conn, isExternal bool) (net.Conn, error) {
	if d == nil {
		_ = conn.Close()
		return nil, context.Canceled
	}
	g := d.group
	g.access.Lock()
	if d.finished || g.dials[d.id] != d {
		d.finished = true
		g.access.Unlock()
		_ = conn.Close()
		d.cancel()
		return nil, causeOrCanceled(d.ctx)
	}
	if d.ctx.Err() != nil {
		d.finished = true
		delete(g.dials, d.id)
		g.access.Unlock()
		_ = conn.Close()
		d.cancel()
		return nil, causeOrCanceled(d.ctx)
	}
	d.finished = true
	delete(g.dials, d.id)
	item := g.connections.PushBack(&groupConnItem{conn: conn, isExternal: isExternal})
	g.access.Unlock()
	// The connection is not derived from the setup context; cancelling it here keeps one
	// registration from outliving the dial.
	d.cancel()
	return &Conn{Conn: conn, group: g, element: item}, nil
}

// CompletePacketConn is CompleteConn for a packet connection.
func (d *Dial) CompletePacketConn(conn net.PacketConn, isExternal bool) (net.PacketConn, error) {
	if d == nil {
		_ = conn.Close()
		return nil, context.Canceled
	}
	g := d.group
	g.access.Lock()
	if d.finished || g.dials[d.id] != d {
		d.finished = true
		g.access.Unlock()
		_ = conn.Close()
		d.cancel()
		return nil, causeOrCanceled(d.ctx)
	}
	if d.ctx.Err() != nil {
		d.finished = true
		delete(g.dials, d.id)
		g.access.Unlock()
		_ = conn.Close()
		d.cancel()
		return nil, causeOrCanceled(d.ctx)
	}
	d.finished = true
	delete(g.dials, d.id)
	item := g.connections.PushBack(&groupConnItem{conn: conn, isExternal: isExternal})
	g.access.Unlock()
	d.cancel()
	return newPacketConn(g, conn, item), nil
}

func causeOrCanceled(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return context.Canceled
}

func (g *Group) Add(closer io.Closer, isExternal bool) (remove func()) {
	g.access.Lock()
	defer g.access.Unlock()
	element := g.connections.PushBack(&groupConnItem{closer, isExternal})
	return func() {
		g.access.Lock()
		defer g.access.Unlock()
		g.connections.Remove(element)
	}
}

func (g *Group) NewConn(conn net.Conn, isExternal bool) net.Conn {
	g.access.Lock()
	defer g.access.Unlock()
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &Conn{Conn: conn, group: g, element: item}
}

func (g *Group) NewPacketConn(conn net.PacketConn, isExternal bool) net.PacketConn {
	g.access.Lock()
	defer g.access.Unlock()
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return newPacketConn(g, conn, item)
}

// Interrupt applies a non-selection switch: it closes the established connections the setting
// covers, but it leaves in-flight setup alone. It is the provider-republication path: the selected
// TAG did not move, the old object is retained by the dial's lease until the dial returns, and
// cancelling that dial would turn a routine subscription refresh into a failed connection.
func (g *Group) Interrupt(interruptExternalConnections bool) {
	g.interrupt(interruptExternalConnections, false, true)
}

// InterruptSelection applies a SELECTION change: every pending dial is cancelled (they belong to the
// member the user or the latency test switched away from and have produced no connection), and the
// established connections the setting covers are closed.
func (g *Group) InterruptSelection(interruptExternalConnections bool) {
	g.interrupt(interruptExternalConnections, true, true)
}

// InvalidateSetup cancels every pending dial and advances the revision WITHOUT touching established
// connections. It is for a selection that is being cleared (a failed member, a group generation
// being retired): new dials must stop using the old choice, but a single target failure must not cut
// flows that are still carrying traffic.
func (g *Group) InvalidateSetup() {
	g.interrupt(false, true, false)
}

func (g *Group) interrupt(interruptExternalConnections bool, selectionChanged bool, closeConnections bool) {
	g.access.Lock()
	var cancels []context.CancelFunc
	if selectionChanged {
		g.revision++
		for id, dial := range g.dials {
			delete(g.dials, id)
			dial.finished = true
			cancels = append(cancels, dial.cancel)
		}
	}
	var closers []io.Closer
	if closeConnections {
		for element := g.connections.Front(); element != nil; {
			nextElement := element.Next()
			if !element.Value.isExternal || interruptExternalConnections {
				closers = append(closers, element.Value.conn)
				g.connections.Remove(element)
			}
			element = nextElement
		}
	}
	g.access.Unlock()
	// Cancelling a context runs its callbacks and wakes blocked dials; do it outside the lock.
	for _, cancel := range cancels {
		cancel()
	}
	for _, closer := range closers {
		closer.Close()
	}
}
