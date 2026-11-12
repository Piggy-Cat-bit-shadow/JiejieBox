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
			return client.DialConn(ctx, destination)
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *tuic.Client) (any, error) {
		return client.DialConn(attemptCtx, destination)
	})
	if err != nil {
		return nil, err
	}
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
			return client.ListenPacket(ctx)
		}
	}
	value, err := c.race(ctx, func(attemptCtx context.Context, client *tuic.Client) (any, error) {
		return client.ListenPacket(attemptCtx)
	})
	if err != nil {
		return nil, err
	}
	return value.(net.PacketConn), nil
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
	c.clients = [2]*tuic.Client{}
	c.winner = 0
	c.established = false
	c.access.Unlock()
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
