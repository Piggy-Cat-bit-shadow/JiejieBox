//go:build with_naive_outbound

package naive

import (
	"context"
	"net"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

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
// # Lazy, bounded, and never load-bearing
//
// Most users never hit the fallback and a second engine is expensive, so it is
// created on the first H2 retry and reused afterwards. A creation failure is NOT
// cached: the next fallback tries again. For the flow whose creation failed, the
// call falls back to the per-request H2 choice on the primary client - the
// previous behaviour - so availability never depends on the second engine. The
// engine-level guarantee is what the native acceptance still has to confirm; the
// component tests pin creation, reuse, concurrency, failure and close.
// naiveH2Client is the surface the H2 retry needs from the fallback engine. It
// exists so the creation/reuse/close lifecycle can be tested without a native
// library; *cronet.NaiveClient satisfies it exactly.
type naiveH2Client interface {
	naiveDialerClient
	Close() error
	CloseAllConnections()
}

type h2FallbackClient struct {
	access sync.Mutex
	client naiveH2Client
	closed bool
	create func() (naiveH2Client, error)
}

func newH2FallbackClient(create func() (naiveH2Client, error)) *h2FallbackClient {
	return &h2FallbackClient{create: create}
}

// get returns the created client, creating it on first use. Creation is
// serialised: concurrent first fallbacks wait for one engine instead of building
// several.
func (c *h2FallbackClient) get() (naiveH2Client, error) {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		return nil, E.New("naive H2 fallback client is closed")
	}
	if c.client != nil {
		return c.client, nil
	}
	client, err := c.create()
	if err != nil {
		return nil, err
	}
	c.client = client
	return client, nil
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
		return nil
	}
	return client.Close()
}

// dialNaiveH2 performs one HTTP/2 CONNECT attempt for the opt-in fallback.
//
// The engine-level client is preferred. When it cannot be created, the primary
// client's per-request H2 choice is used and the reason is logged: that is
// strictly the previous behaviour, so the fallback still works on a host where
// the second engine cannot be built.
func dialNaiveH2(ctx context.Context, primary naiveDialerClient, h2 *h2FallbackClient, l logger.ContextLogger, destination M.Socksaddr) (net.Conn, error) {
	if h2 != nil {
		client, err := h2.get()
		if err == nil {
			return dialNaiveTunnel(ctx, client, destination)
		}
		l.DebugContext(ctx, "naive H2 fallback engine unavailable, using the per-request HTTP/2 choice: ", err)
	}
	return dialNaiveTunnelWithQUIC(ctx, primary, destination, false)
}
