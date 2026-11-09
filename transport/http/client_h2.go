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

func (c *Client) acquireHTTP2(ctx context.Context) (*http2ClientConn, net.Conn, error) {
	if clientConn, conn, reserved := c.reserveHTTP2Conn(); reserved {
		return clientConn, conn, nil
	}
	// One dial at a time - but NOT while holding the pool lock.
	//
	// # Why the dial must not own the pool lock
	//
	// ResetConnections runs on every network change and has to clear this pool. While the dial
	// held http2Access for its whole TLS handshake, a transition waited for a handshake that
	// belonged to the network being left - up to the TLS timeout - before it could start tearing
	// the old network down. The dial is serialised by its own mutex instead, and its result is
	// only published if the network it was made on is still the current one.
	c.http2DialAccess.Lock()
	defer c.http2DialAccess.Unlock()
	// Another setup may have finished a dial while this one waited for the slot.
	if clientConn, conn, reserved := c.reserveHTTP2Conn(); reserved {
		return clientConn, conn, nil
	}
	networkEpoch := c.networkEpoch.Load()
	conn, err := c.tlsDialer.DialTLSContext(ctx, c.server)
	if err != nil {
		return nil, nil, err
	}
	if !c.isCurrentNetwork(networkEpoch) {
		// This connection belongs to the network being left. Do not put it in the new pool and do
		// not spend a round trip on it: the setup fails and the next attempt dials on the network
		// that is current by then.
		conn.Close()
		return nil, nil, errHTTP2DialSuperseded
	}
	if conn.ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		c.observeHTTP2Unsupported(networkEpoch)
		return nil, conn, nil
	}
	clientConn, err := newHTTP2ClientConn(c.http2Transport, conn)
	if err != nil {
		conn.Close()
		return nil, nil, E.Cause(err, "create HTTP/2 connection")
	}
	clientConn.reserveNewRequest()
	if !c.publishHTTP2Conn(networkEpoch, clientConn, conn) {
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

// publishHTTP2Conn installs a freshly dialled connection unless the network it was dialled on is
// no longer current.
//
// The check and the append are one transaction against ResetConnections: it takes the same
// capability lock around its epoch bump and its pool clear, so either this connection is cleared
// by that reset or this connection never enters the pool.
func (c *Client) publishHTTP2Conn(networkEpoch uint64, clientConn *http2ClientConn, conn net.Conn) bool {
	c.capabilityAccess.Lock()
	defer c.capabilityAccess.Unlock()
	if c.networkEpoch.Load() != networkEpoch {
		conn.Close()
		return false
	}
	c.http2Access.Lock()
	c.http2Conns = append(c.http2Conns, clientConn)
	c.http2Access.Unlock()
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
