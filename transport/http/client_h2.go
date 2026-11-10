package http

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/pipe"

	"golang.org/x/net/http2"
)

var errHTTP2DialSuperseded = E.New("the network changed while the HTTP/2 connection was being dialled")

// http2DialGate is the per-generation single flight that serialises HTTP/2 dials.
//
// It is a channel rather than a sync.Mutex because the wait must honour the WAITER's context: a
// tunnel setup with a 3 s window must not spend it queued behind somebody else's 15 s handshake.
// A mutex cannot be abandoned, so the setup would overrun its own window and the caller would be
// gone long before it returned.
//
// The generation is part of the gate so a NEW network never waits at a retired one: the old dial
// belongs to the network being left and is going to be discarded anyway.
type http2DialGate struct {
	generation uint64
	done       chan struct{}
}

// acquireHTTP2DialSlot takes the dial slot for this generation.
//
// A slot left behind by an OLDER generation never blocks this call: the older dial is being
// abandoned (its result will be refused by publishHTTP2Conn), so this generation replaces the slot
// and dials now. A slot from a NEWER generation means this call is stale and reports superseded.
// Each release only clears the slot while it is still its own, so the retired dial cannot clear the
// new generation's slot when it finally returns.
func (c *Client) acquireHTTP2DialSlot(ctx context.Context, generation uint64) (func(), error) {
	newSlot := func() func() {
		gate := &http2DialGate{generation: generation, done: make(chan struct{})}
		c.http2DialGate = gate
		return func() {
			c.http2GateAccess.Lock()
			if c.http2DialGate == gate {
				c.http2DialGate = nil
			}
			close(gate.done)
			c.http2GateAccess.Unlock()
		}
	}
	for {
		c.http2GateAccess.Lock()
		gate := c.http2DialGate
		if gate == nil {
			release := newSlot()
			c.http2GateAccess.Unlock()
			return release, nil
		}
		if gate.generation != generation {
			if gate.generation > generation {
				c.http2GateAccess.Unlock()
				return nil, errHTTP2DialSuperseded
			}
			release := newSlot()
			c.http2GateAccess.Unlock()
			return release, nil
		}
		waiting := gate.done
		c.http2GateAccess.Unlock()
		select {
		case <-waiting:
			// The dial finished; go around and try to take the slot, or use its connection.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) acquireHTTP2(ctx context.Context) (*http2ClientConn, net.Conn, error) {
	if c.closed.Load() {
		return nil, nil, net.ErrClosed
	}
	if clientConn, conn, reserved := c.reserveHTTP2Conn(); reserved {
		return clientConn, conn, nil
	}
	// One dial at a time - but NOT while holding the pool lock, and not across generations.
	//
	// # Why the dial must not own the pool lock
	//
	// ResetConnections runs on every network change and has to clear this pool. While the dial
	// held http2Access for its whole TLS handshake, a transition waited for a handshake that
	// belonged to the network being left - up to the TLS timeout - before it could start tearing
	// the old network down. The dial is serialised by its own gate instead, and its result is
	// only published if the network it was made on is still the current one.
	generation := c.networkEpoch.Load()
	releaseSlot, err := c.acquireHTTP2DialSlot(ctx, generation)
	if err != nil {
		return nil, nil, err
	}
	// Another setup may have finished a dial while this one waited for the slot.
	if clientConn, conn, reserved := c.reserveHTTP2Conn(); reserved {
		releaseSlot()
		return clientConn, conn, nil
	}
	if c.closed.Load() {
		releaseSlot()
		return nil, nil, net.ErrClosed
	}
	conn, err := c.tlsDialer.DialTLSContext(ctx, c.server)
	if err != nil {
		releaseSlot()
		if c.closed.Load() {
			return nil, nil, net.ErrClosed
		}
		return nil, nil, err
	}
	if c.closed.Load() {
		// Close() returned while this dial was running: the connection must not be used or
		// pooled, and the caller must learn the client is gone.
		releaseSlot()
		conn.Close()
		return nil, nil, net.ErrClosed
	}
	if !c.isCurrentNetwork(generation) {
		// This connection belongs to the network being left. Do not put it in the new pool and do
		// not spend a round trip on it: the setup fails and the next attempt dials on the network
		// that is current by then.
		releaseSlot()
		conn.Close()
		return nil, nil, errHTTP2DialSuperseded
	}
	if conn.ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		c.observeHTTP2Unsupported(generation)
		releaseSlot()
		return nil, conn, nil
	}
	clientConn, err := newHTTP2ClientConn(c.http2Transport, conn)
	if err != nil {
		releaseSlot()
		conn.Close()
		return nil, nil, E.Cause(err, "create HTTP/2 connection")
	}
	clientConn.reserveNewRequest()
	published := c.publishHTTP2Conn(generation, clientConn, conn)
	releaseSlot()
	if !published {
		// Close() and a network change both land here; the caller needs to know which, because a
		// closed client is not retried and a retired generation is.
		if c.closed.Load() {
			return nil, nil, net.ErrClosed
		}
		return nil, nil, errHTTP2DialSuperseded
	}
	return clientConn, nil, nil
}

// reserveHTTP2Conn takes a request slot on an existing connection, if one is available.
func (c *Client) reserveHTTP2Conn() (*http2ClientConn, net.Conn, bool) {
	c.http2Access.Lock()
	defer c.http2Access.Unlock()
	c.http2Conns = slices.DeleteFunc(c.http2Conns, func(it *http2ClientConn) bool { return it.isClosed() })
	for _, clientConn := range c.http2Conns {
		if clientConn.reserveNewRequest() {
			return clientConn, nil, true
		}
	}
	return nil, nil, false
}

// isCurrentNetwork reports whether the network an attempt started on is still the current one.
func (c *Client) isCurrentNetwork(networkEpoch uint64) bool {
	c.capabilityAccess.Lock()
	defer c.capabilityAccess.Unlock()
	return c.networkEpoch.Load() == networkEpoch
}

// publishHTTP2Conn installs a freshly dialled connection unless the client is closed or the network
// it was dialled on is no longer current.
//
// The closed/epoch checks and the append happen under the SAME pool lock as Close and
// ResetConnections' clear, instead of checking closed before taking the lock. With the old order a
// publisher could pass the check, lose the CPU, let Close() return after clearing the pool, and
// then append its connection to an already-torn-down pool - the review reproduced exactly that
// interleaving. Now either the append happens before the clear (and the clear closes it) or the
// check sees the closed client / new epoch and refuses.
func (c *Client) publishHTTP2Conn(networkEpoch uint64, clientConn *http2ClientConn, conn net.Conn) bool {
	c.http2Access.Lock()
	defer c.http2Access.Unlock()
	if c.closed.Load() || c.networkEpoch.Load() != networkEpoch {
		conn.Close()
		return false
	}
	c.http2Conns = append(c.http2Conns, clientConn)
	return true
}

// observeHTTP2Unsupported records the ALPN answer as a capability of the network it was observed
// on. The store used to happen unconditionally, so a dial that finished after a network change
// could mark the NEW network as "no HTTP/2" on the strength of the old one's server.
func (c *Client) observeHTTP2Unsupported(networkEpoch uint64) {
	if c.disableVersionFallback {
		return
	}
	c.capabilityAccess.Lock()
	defer c.capabilityAccess.Unlock()
	if c.networkEpoch.Load() != networkEpoch {
		return
	}
	c.http2Unsupported.Store(true)
}

func (c *Client) connectHTTP2(ctx context.Context, clientConn *http2ClientConn, destination M.Socksaddr) (net.Conn, error) {
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: destination.String()},
		Host:   destination.String(),
		Header: buildRequestHeader(c.headers, c.authorization, false),
	}
	streamConn, err := c.roundTripHTTP2(ctx, clientConn, request, destination)
	if err != nil {
		return nil, err
	}
	return newClientTunnelConn(streamConn), nil
}

func (c *Client) roundTripHTTP2(ctx context.Context, clientConn *http2ClientConn, request *http.Request, destination M.Socksaddr) (*clientStreamConn, error) {
	pipeReader, pipeWriter := pipe.Pipe()
	streamCtx, cancel := context.WithCancel(context.Background())
	request.Body = pipeReader
	request = request.WithContext(streamCtx)
	stop := context.AfterFunc(ctx, cancel)
	response, err := clientConn.roundTrip(request)
	stopped := stop()
	if err == nil && !stopped {
		response.Body.Close()
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		pipeWriter.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// golang.org/x/net/http2 has no exported sentinel for a peer that lacks
		// SETTINGS_ENABLE_CONNECT_PROTOCOL; it returns errExtendedConnectNotSupported
		// ("net/http: extended connect not supported by peer") from RoundTrip.
		if strings.Contains(err.Error(), "extended connect not supported") {
			return nil, E.Cause1(errExtendedConnectUnsupported, err)
		}
		return nil, E.Cause(err, "HTTP/2 CONNECT")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		cancel()
		pipeWriter.Close()
		return nil, statusError(response)
	}
	return &clientStreamConn{
		reader:     response.Body,
		writer:     pipeWriter,
		cancel:     cancel,
		localAddr:  M.Socksaddr{},
		remoteAddr: destination,
	}, nil
}

func (c *Client) connectAndClose(ctx context.Context, conn net.Conn, destination M.Socksaddr) (net.Conn, error) {
	result, err := c.connect(ctx, conn, destination)
	if err != nil {
		conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return result, nil
}
