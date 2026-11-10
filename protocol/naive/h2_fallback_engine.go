//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

// errH2FallbackClosed is a TERMINATING error: the owner is shutting down, so the
// retry must stop. It is deliberately distinct from a creation failure, which is
// a soft condition the flow may survive on the primary engine.
var errH2FallbackClosed = E.New("naive H2 fallback client is closed")

// naiveH2Client is the surface the H2 retry needs from the fallback engine. It
// exists so the creation/reuse/close lifecycle can be tested without a native
// library; *cronet.NaiveClient satisfies it exactly.
type naiveH2Client interface {
	naiveDialerClient
	Close() error
	CloseAllConnections()
}

// h2FallbackClient owns the optional engine-level HTTP/2-only client.
//
// # Why a second engine is the only hard guarantee
//
// The pinned Cronet exposes QUIC enable/disable at ENGINE level only
// (EngineParams.SetEnableQuic); UrlRequestParams has no per-request switch. An
// H3-first engine therefore cannot be told "for this request, do not use QUIC":
// Chromium may still follow a cached Alt-Svc and speak QUIC again. A client built
// with QUIC:false has its OWN engine with QUIC disabled and HTTP/2 enabled, which
// is the only construction that guarantees the retry is HTTP/2.
//
// # Single flight that the waiter can leave
//
// The creation runs OUTSIDE the lock, and a concurrent fallback waits on a
// channel that is closed when creation finishes: it selects on its own context,
// so a cancelled CONNECT leaves the wait instead of queueing behind a native
// engine start it no longer needs. Only one creation flies at a time.
//
// # Closing while creating
//
// Close is final: a result that finishes after Close is a RETIRED result. It is
// never published and its engine is closed exactly once (the late resource is
// released here). Every waiter, and every later caller, gets
// errH2FallbackClosed, which the dial path treats as terminating - it must not
// dispatch a CONNECT on the primary engine after shutdown.
//
// # Failure is soft
//
// A creation failure is not cached: the next fallback tries again. For the flow
// whose creation failed, the dial path falls back to the primary engine's
// per-request H2 choice - the previous behaviour - which can still follow a
// cached Alt-Svc. That branch is a BEST-EFFORT fallback, not the engine-level
// guarantee.
type h2FallbackClient struct {
	access   sync.Mutex
	client   naiveH2Client
	closed   bool
	creating chan struct{}
	create   func() (naiveH2Client, error)
}

func newH2FallbackClient(create func() (naiveH2Client, error)) *h2FallbackClient {
	return &h2FallbackClient{create: create}
}

// get waits without a caller context. It exists for component callers and tests;
// the dial path uses getContext.
func (c *h2FallbackClient) get() (naiveH2Client, error) {
	return c.getContext(context.Background())
}

// getContext returns the created client, creating it on first use. The wait for
// another caller's creation honours ctx.
func (c *h2FallbackClient) getContext(ctx context.Context) (naiveH2Client, error) {
	for {
		c.access.Lock()
		if c.closed {
			c.access.Unlock()
			return nil, errH2FallbackClosed
		}
		if c.client != nil {
			client := c.client
			c.access.Unlock()
			return client, nil
		}
		if c.creating != nil {
			creating := c.creating
			c.access.Unlock()
			select {
			case <-creating:
				// Create finished, failed or was retired: re-examine the state.
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		creating := make(chan struct{})
		c.creating = creating
		c.access.Unlock()

		// The native engine start is slow and must not hold the lock: otherwise a
		// cancelled waiter (and Close, and peek) queue behind it.
		client, err := c.create()

		c.access.Lock()
		retired := c.closed
		if err == nil && !retired {
			c.client = client
		}
		c.creating = nil
		close(creating)
		c.access.Unlock()

		if err != nil {
			// Soft failure, not cached: the next fallback tries again.
			return nil, err
		}
		if retired {
			// Late resource: released here, exactly once, and never published.
			if client != nil {
				_ = client.Close()
			}
			return nil, errH2FallbackClosed
		}
		return client, nil
	}
}

// peek returns the created client, if any, without creating one.
func (c *h2FallbackClient) peek() naiveH2Client {
	c.access.Lock()
	defer c.access.Unlock()
	return c.client
}

func (c *h2FallbackClient) Close() error {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	client := c.client
	c.client = nil
	if client == nil {
		// A creation in flight will see `closed` and release its late engine.
		return nil
	}
	return client.Close()
}

// dialNaiveH2 performs one HTTP/2 CONNECT attempt for the opt-in fallback.
//
//   - engine-level client available  -> use it (the hard guarantee);
//   - owner closed                   -> terminating error, NO primary dispatch;
//   - caller cancelled               -> its own error, NO primary dispatch;
//   - creation failed                -> BEST-EFFORT: the primary engine's
//     per-request H2 choice (previous behaviour; may follow Alt-Svc).
func dialNaiveH2(ctx context.Context, primary naiveDialerClient, h2 *h2FallbackClient, l logger.ContextLogger, destination M.Socksaddr) (net.Conn, error) {
	if h2 != nil {
		client, err := h2.getContext(ctx)
		switch {
		case err == nil:
			return dialNaiveTunnel(ctx, client, destination)
		case errors.Is(err, errH2FallbackClosed):
			return nil, err
		case ctx.Err() != nil:
			return nil, ctx.Err()
		default:
			l.DebugContext(ctx, "naive H2 fallback engine unavailable, best-effort per-request HTTP/2 choice: ", err)
		}
	}
	return dialNaiveTunnelWithQUIC(ctx, primary, destination, false)
}
