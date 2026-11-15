package interrupt

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing/common/x/list"
)

// Group owns the two kinds of work a group switch must be able to end:
//
//   - established connections, added with NewConn/NewPacketConn/Add. A switch closes the ones the
//     interrupt_exist_connections setting covers.
//   - dials that have not produced a connection yet, begun with BeginDial. A switch cancels them
//     whatever that setting says: they are not established traffic, their member is the one being
//     switched away from, and a dial stuck on a dead node is exactly what makes a switch appear to
//     do nothing while the old request waits for its own timeout (or forever, when it has none).
//
// The two are separate on purpose. "Interrupt existing connections = false" is a promise about
// flows that already carry traffic, not a promise to keep a half-finished handshake alive.
type Group struct {
	access      sync.Mutex
	connections list.List[*groupConnItem]
	dials       map[uint64]context.CancelFunc
	nextDial    uint64
	// revision counts switches. A dial records the revision it selected its member under, and
	// BeginDial refuses a revision that is already stale, so a dial that began in the instant of a
	// switch cannot silently proceed on the old member.
	revision uint64
}

type groupConnItem struct {
	conn       io.Closer
	isExternal bool
}

func NewGroup() *Group {
	return &Group{}
}

// Revision reports the current selection revision. A caller reads it BEFORE selecting a member and
// passes it to BeginDial; a switch that happened in between makes BeginDial report stale.
func (g *Group) Revision() uint64 {
	g.access.Lock()
	defer g.access.Unlock()
	return g.revision
}

// BeginDial derives the setup context for one dial. The caller MUST call release exactly once when
// the dial returns; after that point the connection's lifetime is its own, not the setup's.
//
// The context inherits the caller's cancellation, and Interrupt cancels it while the dial is in
// flight. stale reports that the selection revision the caller selected under is no longer current:
// the caller must release and re-select rather than dial the member it already chose.
func (g *Group) BeginDial(ctx context.Context, revision uint64) (dialCtx context.Context, release func(), stale bool) {
	g.access.Lock()
	if revision != g.revision {
		g.access.Unlock()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled, func() {}, true
	}
	if g.dials == nil {
		g.dials = make(map[uint64]context.CancelFunc)
	}
	dialCtx, cancel := context.WithCancel(ctx)
	g.nextDial++
	id := g.nextDial
	g.dials[id] = cancel
	g.access.Unlock()

	var once sync.Once
	return dialCtx, func() {
		once.Do(func() {
			g.access.Lock()
			delete(g.dials, id)
			g.access.Unlock()
			// Releasing the setup never cancels the connection that was established: the context
			// this cancels was only ever the dial's, and no conn is derived from it.
			cancel()
		})
	}, false
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

// Interrupt applies one switch: every pending dial is cancelled (they belong to the member being
// switched away from and have produced no connection), and the established connections the setting
// covers are closed.
func (g *Group) Interrupt(interruptExternalConnections bool) {
	g.access.Lock()
	g.revision++
	var cancels []context.CancelFunc
	for id, cancel := range g.dials {
		cancels = append(cancels, cancel)
		delete(g.dials, id)
	}
	var closers []io.Closer
	for element := g.connections.Front(); element != nil; {
		nextElement := element.Next()
		if !element.Value.isExternal || interruptExternalConnections {
			closers = append(closers, element.Value.conn)
			g.connections.Remove(element)
		}
		element = nextElement
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
