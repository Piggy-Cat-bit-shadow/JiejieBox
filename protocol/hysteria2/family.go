package hysteria2

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
// One Hysteria2 client is one family's view of the server. When the server is a domain (so both
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
	"github.com/sagernet/sing-quic/hysteria2"
	M "github.com/sagernet/sing/common/metadata"
)

// errNoFamilyClient means neither family produced a usable dialer/client; the caller sees one error
// instead of a silent single-family behaviour.
var errNoFamilyClient = errors.New("hysteria2: no per-family client could be created")

// errStaleFamilyRace means a first-creation race produced a session after the network generation had
// already moved on. The session belonged to the path that just ended, so it is discarded instead of
// published; the next demand races on the current generation, which is a retryable condition.
var errStaleFamilyRace = errors.New("hysteria2: the network changed while the first session was being established")

// familyCarrier owns the one or two Hysteria2 clients of one outbound.
type familyCarrier struct {
	access    sync.Mutex
	clients   [2]*hysteria2.Client
	newClient func(family int) (*hysteria2.Client, error)
	families  int
	winner    int // 0 none, 1 or 2; protected by access
	// generation counts client REPLACEMENTS: every CloseWithError (network change, listener reset,
	// shutdown) bumps it, and a first-creation race records the value it started under. A race is
	// allowed to retire only the exact client instance it started with, in the generation it
	// started in; anything else belongs to a newer generation and must be left alone.
	generation  uint64
	established bool
	// Residency accounting (audit Q15). One object is one established session; activeFlows is
	// the Active/Idle distinction and terminated is the lifetime session-release count.
	creating   atomic.Int64
	terminated atomic.Uint64
	lastDemand atomic.Int64
}

func newFamilyCarrier(newClient func(family int) (*hysteria2.Client, error)) *familyCarrier {
	return &familyCarrier{newClient: newClient}
}

// clientLocked returns the client for a family, creating it on demand. The caller holds access.
func (c *familyCarrier) clientLocked(family int) (*hysteria2.Client, error) {
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

func (c *familyCarrier) peek(family int) *hysteria2.Client {
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
			c.noteDemand()
			return conn, nil
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *hysteria2.Client) (any, error) {
		return client.DialConn(attemptCtx, destination)
	})
	if err != nil {
		return nil, err
	}
	c.noteDemand()
	return value.(net.Conn), nil
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
			c.noteDemand()
			return conn, nil
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *hysteria2.Client) (any, error) {
		return client.ListenPacket(attemptCtx)
	})
	if err != nil {
		return nil, err
	}
	c.noteDemand()
	return value.(net.PacketConn), nil
}

// familyCandidate is one family's client at the moment a race started. It carries the client
// INSTANCE so the cleanup can compare it with what the slot holds now, instead of nulling the slot by
// index - the identity is what keeps an old race from removing a newer generation's client.
type familyCandidate struct {
	family int
	client *hysteria2.Client
}

type familyResult struct {
	family int
	value  any
	err    error
}

// race runs the creation on both families and returns the first success.
func (c *familyCarrier) race(ctx context.Context, attempt func(context.Context, *hysteria2.Client) (any, error)) (any, error) {
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.creating.Add(1)
	defer c.creating.Add(-1)

	candidates := make([]familyCandidate, 0, 2)
	c.access.Lock()
	families := c.families
	if families < 1 {
		families = 1
	}
	generation := c.generation
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

	// settled holds the families whose result has already been READ. A failed family is settled the
	// moment its result is read, so its client must be retired here - not by retireLosers, which
	// can only wait for results that have not arrived yet. Passing the read count on (remaining)
	// instead used to make retireLosers wait for an already-consumed result: the first failed
	// family was never retired.
	var lastErr error
	settled := make([]int, 0, len(candidates)-1)
	for read := 0; read < len(candidates); read++ {
		result := <-results
		if result.err != nil {
			lastErr = result.err
			settled = append(settled, result.family)
			continue
		}
		c.access.Lock()
		if c.generation != generation {
			// The network moved while this race was running: CloseWithError has already replaced
			// the clients. Publishing this winner would present a session built on the previous
			// network as the established one, so the result is discarded like any late creation.
			c.access.Unlock()
			cancel()
			c.terminated.Add(1)
			closeRaceValue(result.value)
			go c.retireLosers(results, generation, 0, candidates, settled, len(candidates)-read-1)
			return nil, errStaleFamilyRace
		}
		// The first success wins. The context is cancelled for the loser, and every loser is closed
		// after its result arrives: its socket must not survive the race.
		c.winner = result.family
		c.established = true
		c.access.Unlock()
		cancel()
		go c.retireLosers(results, generation, result.family, candidates, settled, len(candidates)-read-1)
		return result.value, nil
	}
	return nil, lastErr
}

// closeRaceValue closes the connection or packet session a losing attempt produced, once.
func closeRaceValue(value any) {
	switch value := value.(type) {
	case net.Conn:
		_ = value.Close()
	case net.PacketConn:
		_ = value.Close()
	}
}

// retireLosers settles EVERY result of one race against the generation the race started in.
//
// settled are the families already read as failures, and unread is how many results are still in
// flight. Both halves are retired the same way, so the accounting cannot depend on which attempt
// returned first: a family that failed before the winner and a family that was still trying when the
// winner arrived are both losers.
func (c *familyCarrier) retireLosers(results <-chan familyResult, generation uint64, winner int, candidates []familyCandidate, settled []int, unread int) {
	byFamily := make(map[int]*hysteria2.Client, len(candidates))
	for _, candidate := range candidates {
		byFamily[candidate.family] = candidate.client
	}
	for _, family := range settled {
		c.retireFamily(generation, family, byFamily[family])
	}
	for index := 0; index < unread; index++ {
		result := <-results
		if result.value != nil {
			c.terminated.Add(1)
			closeRaceValue(result.value)
		}
		if result.family == winner {
			continue
		}
		c.retireFamily(generation, result.family, byFamily[result.family])
	}
}

// retireFamily closes the client of one losing family, but only while it is still the instance this
// race started with, in the generation it started in.
//
// The two checks are the fix for the audit's first counterexample. Clearing clients[family-1] by
// index alone meant a loser that returned AFTER a network change removed the client the NEW
// generation had just installed - the old race's cleanup closed the fresh generation's family client.
// A client pointer is never re-installed (clientLocked creates a fresh one), so comparing the
// instance is exact, and the generation comparison states the same fact for the whole carrier.
//
// The slot is cleared under the lock and the close runs outside it: CloseWithError can block on the
// QUIC transport, and the slot being nil already forbids every later path from handing the client out.
func (c *familyCarrier) retireFamily(generation uint64, family int, client *hysteria2.Client) {
	if client == nil {
		return
	}
	c.access.Lock()
	if c.generation != generation || c.clients[family-1] != client {
		c.access.Unlock()
		return
	}
	c.clients[family-1] = nil
	c.access.Unlock()
	_ = client.CloseWithError(net.ErrClosed)
}

// CloseWithError closes both clients: the network-change and shutdown path. The next demand creates
// fresh clients and races again.
func (c *familyCarrier) CloseWithError(err error) error {
	c.access.Lock()
	clients := c.clients
	wasEstablished := c.established
	c.clients = [2]*hysteria2.Client{}
	c.winner = 0
	c.established = false
	// Every replacement starts a new generation: a race that started before this call must not
	// retire anything this call allows the next demand to install.
	c.generation++
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

// noteDemand records a DEMAND for a stream or packet session, not payload movement.
func (c *familyCarrier) noteDemand() {
	c.lastDemand.Store(time.Now().UnixNano())
}

func (c *familyCarrier) activeWithin(window time.Duration) bool {
	last := c.lastDemand.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < window
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter (audit Q15).
//
// One object is one established Hysteria2 session, and the unit says so: it is a SESSION, not a
// connection and not a stream. Creating is a first-session race in flight; Active/Idle is whether a
// stream or UDP session was DEMANDED in the last 60 s - a demand, not payload, because the sessions
// are handed out as the library's own conn types and wrapping them to observe bytes would hide the
// optional interfaces the router reads. Terminated counts sessions ended by CloseWithError or by
// losing a race after establishing. Streams are the Active/Idle distinction inside the one session,
// not separate resident objects, and the carrier's client generations are not a second object set:
// replacing the clients closes the session it carried.
func (c *familyCarrier) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	c.access.Lock()
	established := c.established
	c.access.Unlock()
	ledger := adapter.ResidencyLedger{
		Unit:       adapter.ResidencyUnitSession,
		Answered:   true,
		Exact:      true,
		Terminated: c.terminated.Load(),
		Note: "Hysteria2 sessions (one established session per outbound, unit=session); Active/Idle " +
			"uses a 60 s last-DEMAND window, not a payload window, and streams are the Active/Idle " +
			"distinction inside one session, not separate objects",
	}
	switch {
	case c.creating.Load() > 0:
		ledger.Creating = 1
	case !established:
		return ledger
	case c.activeWithin(time.Minute):
		ledger.Active = 1
	default:
		ledger.Idle = 1
	}
	return ledger
}
