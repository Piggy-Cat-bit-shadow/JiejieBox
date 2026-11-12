package tuic

// Family failover for the QUIC carrier (audit Q05).
//
// # The measured failure
//
// On a dual-stack path the first family can be UDP-writable but silent: the socket accepts the
// Initial packets and nothing ever answers. sing-quic's client commits to the single address its
// dialer returned, so the handshake times out with dials=1 and writes=6 even though the other family
// is healthy. The audit's counterexample is exactly that shape.
//
// # What this file does
//
// One TUIC client is one family's view of the server. When the server is a domain (so both
// families are resolvable) and no realm overrides the address, the outbound builds TWO clients, each
// with a dialer restricted to one family, and races their first creation:
//
//   - both attempts share the caller's total context; no second budget is invented;
//   - the first successful creation wins, the shared context is cancelled, and the losing client is
//     closed, which releases its socket and any half-open session;
//   - no business stream is created by the loser: `DialConn`/`ListenPacket` return only after the
//     session is established;
//   - once a family has won, later dials use it directly. A network change clears the winner (the
//     outbound's CloseWithError does), and the next demand races again.
//
// The cost is deliberate and paid once per outage: a second handshake in parallel on the first
// demand after a network change. If the first family is healthy, the loser is the second family's
// handshake, closed as soon as the winner is known.
//
// # What it does not do
//
// It does not retry a failed established session here: that is the listener's job (InterfaceUpdated
// closes both clients), and retrying application-level errors would turn a target refusal into a
// second handshake. It also does not apply to realm mode, where the server address comes from the
// realm controller rather than from this outbound's two-family resolution.

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-quic/tuic"
	M "github.com/sagernet/sing/common/metadata"
)

// errNoFamilyClient means neither family produced a usable dialer/client; the caller sees one error
// instead of a silent single-family behaviour.
var errNoFamilyClient = errors.New("tuic: no per-family client could be created")

// familyCarrier owns the one or two TUIC clients of one outbound.
type familyCarrier struct {
	access      sync.Mutex
	clients     [2]*tuic.Client
	newClient   func(family int) (*tuic.Client, error)
	families    int
	winner      int // 0 none, 1 or 2; protected by access
	established bool
	// Residency accounting (audit Q15). One object is one established session; activeFlows is
	// the Active/Idle distinction and terminated is the lifetime session-release count.
	creating     atomic.Int64
	activeFlows  atomic.Int64
	terminated   atomic.Uint64
	lastActivity atomic.Int64
}

func newFamilyCarrier(newClient func(family int) (*tuic.Client, error)) *familyCarrier {
	return &familyCarrier{newClient: newClient}
}

// clientLocked returns the client for a family, creating it on demand. The caller holds access.
func (c *familyCarrier) clientLocked(family int) (*tuic.Client, error) {
	if c.clients[family-1] != nil {
		return c.clients[family-1], nil
	}
	client, err := c.newClient(family)
	if err != nil {
		return nil, err
	}
	c.clients[family-1] = client
	return client, nil
}

func (c *familyCarrier) peek(family int) *tuic.Client {
	c.access.Lock()
	defer c.access.Unlock()
	return c.clients[family-1]
}

// DialConn opens a stream on the winning family, or races both families for the first creation.
func (c *familyCarrier) DialConn(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	c.access.Lock()
	established := c.established
	winner := c.winner
	c.access.Unlock()
	if established && winner != 0 {
		if client := c.peek(winner); client != nil {
			conn, err := client.DialConn(ctx, destination)
			if err != nil {
				return nil, err
			}
			return c.trackConn(conn), nil
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *tuic.Client) (any, error) {
		return client.DialConn(attemptCtx, destination)
	})
	if err != nil {
		return nil, err
	}
	return c.trackConn(value.(net.Conn)), nil
}

// ListenPacket is DialConn for a UDP session.
func (c *familyCarrier) ListenPacket(ctx context.Context) (net.PacketConn, error) {
	c.access.Lock()
	established := c.established
	winner := c.winner
	c.access.Unlock()
	if established && winner != 0 {
		if client := c.peek(winner); client != nil {
			conn, err := client.ListenPacket(ctx)
			if err != nil {
				return nil, err
			}
			return c.trackPacketConn(conn), nil
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *tuic.Client) (any, error) {
		return client.ListenPacket(attemptCtx)
	})
	if err != nil {
		return nil, err
	}
	return c.trackPacketConn(value.(net.PacketConn)), nil
}

type familyResult struct {
	family int
	value  any
	err    error
}

// race runs the creation on both families and returns the first success.
func (c *familyCarrier) race(ctx context.Context, attempt func(context.Context, *tuic.Client) (any, error)) (any, error) {
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.creating.Add(1)
	defer c.creating.Add(-1)

	type familyCandidate struct {
		family int
		client *tuic.Client
	}
	candidates := make([]familyCandidate, 0, 2)
	c.access.Lock()
	families := c.families
	if families < 1 {
		families = 1
	}
	for family := 1; family <= families; family++ {
		client, err := c.clientLocked(family)
		if err != nil {
			continue
		}
		candidates = append(candidates, familyCandidate{family: family, client: client})
	}
	c.access.Unlock()
	if len(candidates) == 0 {
		return nil, errNoFamilyClient
	}

	results := make(chan familyResult, len(candidates))
	for _, candidate := range candidates {
		go func(candidate familyCandidate) {
			value, err := attempt(raceCtx, candidate.client)
			results <- familyResult{family: candidate.family, value: value, err: err}
		}(candidate)
	}

	var lastErr error
	for range candidates {
		result := <-results
		if result.err != nil {
			lastErr = result.err
			continue
		}
		// The first success wins. The context is cancelled for the loser, and the loser is closed
		// below after its result arrives: its socket must not survive the race.
		cancel()
		c.access.Lock()
		c.winner = result.family
		c.established = true
		c.access.Unlock()
		go c.retireLosers(results, len(candidates)-1, result.family)
		return result.value, nil
	}
	return nil, lastErr
}

// retireLosers waits for the losing attempts, closes any connection they produced, and closes their
// clients so no socket from the losing family survives the race.
func (c *familyCarrier) retireLosers(results <-chan familyResult, remaining int, winner int) {
	for index := 0; index < remaining; index++ {
		result := <-results
		if result.value != nil {
			c.terminated.Add(1)
			switch value := result.value.(type) {
			case net.Conn:
				_ = value.Close()
			case net.PacketConn:
				_ = value.Close()
			}
		}
		c.access.Lock()
		if result.family != winner {
			if client := c.clients[result.family-1]; client != nil {
				_ = client.CloseWithError(net.ErrClosed)
				c.clients[result.family-1] = nil
			}
		}
		c.access.Unlock()
	}
}

// CloseWithError closes both clients: the network-change and shutdown path. The next demand creates
// fresh clients and races again.
func (c *familyCarrier) CloseWithError(err error) error {
	c.access.Lock()
	clients := c.clients
	wasEstablished := c.established
	c.clients = [2]*tuic.Client{}
	c.winner = 0
	c.established = false
	c.access.Unlock()
	if wasEstablished {
		c.terminated.Add(1)
	}
	var lastErr error
	for _, client := range clients {
		if client == nil {
			continue
		}
		if closeErr := client.CloseWithError(err); closeErr != nil {
			lastErr = closeErr
		}
	}
	return lastErr
}

// SetKeepIdleConnections forwards the idle policy to every live client.
func (c *familyCarrier) SetKeepIdleConnections(keep bool) {
	c.access.Lock()
	clients := c.clients
	c.access.Unlock()
	for _, client := range clients {
		if client != nil {
			client.SetKeepIdleConnections(keep)
		}
	}
}

// CloseIdleConnections forwards the trim action to every live client.
func (c *familyCarrier) CloseIdleConnections() {
	c.access.Lock()
	clients := c.clients
	c.access.Unlock()
	for _, client := range clients {
		if client != nil {
			client.CloseIdleConnections()
		}
	}
}

// markActivity records payload movement on an established stream or packet session.
func (c *familyCarrier) markActivity() {
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *familyCarrier) activeWithin(window time.Duration) bool {
	last := c.lastActivity.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < window
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter (audit Q15).
//
// One object is one established TUIC session. Creating is a first-session race in flight;
// Active/Idle is whether a tracked stream or UDP session moved bytes in the last 60 s; Terminated
// counts sessions ended by CloseWithError or by losing a race after establishing. Streams are the
// Active/Idle distinction inside the one session, not separate resident objects.
func (c *familyCarrier) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	c.access.Lock()
	established := c.established
	c.access.Unlock()
	ledger := adapter.ResidencyLedger{
		Answered:   true,
		Terminated: c.terminated.Load(),
		Note: "TUIC sessions; Active/Idle uses a 60 s last-payload window, and streams are " +
			"the Active/Idle distinction inside one session, not separate objects",
	}
	switch {
	case c.creating.Load() > 0:
		ledger.Creating = 1
	case !established:
		return ledger
	case c.activeFlows.Load() > 0 && c.activeWithin(time.Minute):
		ledger.Active = 1
	default:
		ledger.Idle = 1
	}
	return ledger
}

// trackedConn counts an established session's stream as Active while it is open, and records
// payload movement for the Active/Idle distinction. It forwards net.Conn unchanged otherwise.
type trackedConn struct {
	net.Conn
	carrier *familyCarrier
	once    sync.Once
}

func (c *familyCarrier) trackConn(conn net.Conn) net.Conn {
	if c == nil || conn == nil {
		return conn
	}
	c.activeFlows.Add(1)
	return &trackedConn{Conn: conn, carrier: c}
}

func (t *trackedConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if n > 0 {
		t.carrier.markActivity()
	}
	return n, err
}

func (t *trackedConn) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	if n > 0 {
		t.carrier.markActivity()
	}
	return n, err
}

func (t *trackedConn) Close() error {
	err := t.Conn.Close()
	t.once.Do(func() { t.carrier.activeFlows.Add(-1) })
	return err
}

// trackedPacketConn is trackedConn for a UDP session.
type trackedPacketConn struct {
	net.PacketConn
	carrier *familyCarrier
	once    sync.Once
}

func (c *familyCarrier) trackPacketConn(conn net.PacketConn) net.PacketConn {
	if c == nil || conn == nil {
		return conn
	}
	c.activeFlows.Add(1)
	return &trackedPacketConn{PacketConn: conn, carrier: c}
}

func (t *trackedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := t.PacketConn.ReadFrom(p)
	if n > 0 {
		t.carrier.markActivity()
	}
	return n, addr, err
}

func (t *trackedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, err := t.PacketConn.WriteTo(p, addr)
	if n > 0 {
		t.carrier.markActivity()
	}
	return n, err
}

func (t *trackedPacketConn) Close() error {
	err := t.PacketConn.Close()
	t.once.Do(func() { t.carrier.activeFlows.Add(-1) })
	return err
}
