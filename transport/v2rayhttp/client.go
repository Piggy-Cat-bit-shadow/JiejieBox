package v2rayhttp

import (
	"context"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/badhttp"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"golang.org/x/net/http2"
)

var _ adapter.V2RayMultiplexClientTransport = (*Client)(nil)

type Client struct {
	ctx        context.Context
	dialer     N.Dialer
	serverAddr M.Socksaddr
	transport  common.TypedValue[http.RoundTripper]
	http2      bool
	requestURL url.URL
	host       []string
	method     string
	headers    http.Header
	closeIdle  atomic.Bool
	// pool owns the HTTP/2 generation. It is nil for the HTTP/1 transport, which has no pooled
	// connection to lend.
	pool *h2Pool
	// newTransport builds the next HTTP/2 generation with the same dialer and TLS configuration.
	newTransport func() *http2.Transport
	// generation is this pool's view of the network generation. A nil coordinator yields a nil
	// registration and every check is a no-op, which is the previous behaviour for embedders.
	coordinator        *runtimecoord.Coordinator
	registration       *runtimecoord.Registration
	removeRegistration func()
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayHTTPOptions, tlsConfig tls.Config) (adapter.V2RayClientTransport, error) {
	var (
		transport http.RoundTripper
		// newHTTP2 is set only when the transport multiplexes onto pooled HTTP/2 connections, which
		// is exactly the case a network generation must be checked at the hand-out.
		newHTTP2 func() *http2.Transport
	)
	if tlsConfig == nil {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
			},
		}
	} else {
		if len(tlsConfig.NextProtos()) == 0 {
			tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
		}
		tlsDialer := tls.NewDialer(dialer, tlsConfig)
		newHTTP2 = func() *http2.Transport {
			return &http2.Transport{
				ReadIdleTimeout: time.Duration(options.IdleTimeout),
				PingTimeout:     time.Duration(options.PingTimeout),
				DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.STDConfig) (net.Conn, error) {
					return tlsDialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
				},
			}
		}
		transport = newHTTP2()
	}
	if options.Method == "" {
		options.Method = http.MethodPut
	}
	var requestURL url.URL
	if tlsConfig == nil {
		requestURL.Scheme = "http"
	} else {
		requestURL.Scheme = "https"
	}
	requestURL.Host = serverAddr.String()
	requestURL.Path = options.Path
	err := badhttp.URLSetPath(&requestURL, options.Path)
	if err != nil {
		return nil, E.Cause(err, "parse path")
	}
	if !strings.HasPrefix(requestURL.Path, "/") {
		requestURL.Path = "/" + requestURL.Path
	}
	client := &Client{
		ctx:        ctx,
		dialer:     dialer,
		serverAddr: serverAddr,
		requestURL: requestURL,
		host:       options.Host,
		method:     options.Method,
		headers:    options.Headers.Build(),
		http2:      tlsConfig != nil,
	}
	client.transport.Store(transport)
	if newHTTP2 != nil {
		client.newTransport = newHTTP2
		client.pool = newH2Pool(transport.(*http2.Transport))
	}
	// The generation the pool rebuilds itself against. A transport built in a test or by an
	// embedder without a coordinator gets no registration and keeps its previous behaviour.
	client.coordinator = service.FromContext[*runtimecoord.Coordinator](ctx)
	client.registration, client.removeRegistration = generationRegistration(
		client.coordinator, "v2rayhttp/"+serverAddr.String())
	return client, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	if !c.http2 {
		return c.dialHTTP(ctx)
	} else {
		return c.dialHTTP2(ctx)
	}
}

func (c *Client) dialHTTP(ctx context.Context) (net.Conn, error) {
	conn, err := c.dialer.DialContext(ctx, N.NetworkTCP, c.serverAddr)
	if err != nil {
		return nil, err
	}

	request := &http.Request{
		Method: c.method,
		URL:    &c.requestURL,
		Header: c.headers.Clone(),
	}
	switch hostLen := len(c.host); hostLen {
	case 0:
		if c.serverAddr.IsIPv6() {
			request.Host = "[" + c.serverAddr.AddrString() + "]"
		} else {
			request.Host = c.serverAddr.AddrString()
		}
	case 1:
		request.Host = c.host[0]
	default:
		request.Host = c.host[rand.Intn(hostLen)]
	}

	return NewHTTP1Conn(conn, request), nil
}

func (c *Client) dialHTTP2(ctx context.Context) (net.Conn, error) {
	// The hand-out is where "this pool belongs to a previous network" must be answered: the
	// listener phase has not run yet, and http2.Transport would otherwise reuse a connection whose
	// path just ended. Retiring the transport removes the whole previous generation from new
	// streams; connections that still carry a stream finish on the path they started on.
	if c.generationStale() {
		c.retireGeneration()
	}
	// The generation this creation is being built FOR. A dial that starts before a network change
	// and finishes after it produces a result bound to the path that just ended; publishing it as
	// current would hand every later demand a session the audit called stale. The comparison is one
	// number taken twice from the same clock the dialer compares against.
	startGeneration := c.currentGeneration()

	pipeInReader, pipeInWriter := io.Pipe()
	requestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	request := &http.Request{
		Method: c.method,
		Body:   pipeInReader,
		URL:    &c.requestURL,
		Header: c.headers.Clone(),
	}
	request = request.WithContext(requestCtx)
	switch hostLen := len(c.host); hostLen {
	case 0:
		// https://github.com/v2fly/v2ray-core/blob/master/transport/internet/http/config.go#L13
		request.Host = "www.example.com"
	case 1:
		request.Host = c.host[0]
	default:
		request.Host = c.host[rand.Intn(hostLen)]
	}
	conn := NewLateHTTPConn(pipeInWriter, cancel)
	keepSession := adapter.KeepSessionFromContext(ctx)
	conn.onClose = func() {
		if c.closeIdle.Load() && !keepSession {
			c.CloseIdleConnections()
		}
	}
	handshakeTimeout := C.TCPTimeout
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		handshakeTimeout = time.Until(deadline)
	}
	var handshakeTimedOut atomic.Bool
	handshakeTimer := time.AfterFunc(handshakeTimeout, func() {
		handshakeTimedOut.Store(true)
		cancel()
	})
	generation := c.pool.begin()
	go func() {
		response, err := generation.transport.RoundTrip(request)
		handshakeTimer.Stop()
		if err != nil {
			c.pool.finish(generation)
			if handshakeTimedOut.Load() {
				err = os.ErrDeadlineExceeded
			}
			conn.Setup(nil, err)
		} else if response.StatusCode != 200 {
			response.Body.Close()
			c.pool.finish(generation)
			conn.Setup(nil, E.New("v2ray-http: unexpected status: ", response.Status))
		} else if c.currentGeneration() != startGeneration {
			// The result belongs to a generation that ended while it was in flight. Discard it:
			// acknowledging it would claim the pool is current on evidence from the old network.
			response.Body.Close()
			c.pool.finish(generation)
			conn.Setup(nil, staleGenerationError{})
		} else {
			c.acknowledgeGeneration()
			conn.Setup(&generationBody{ReadCloser: response.Body, release: func() { c.pool.finish(generation) }}, nil)
		}
	}()
	return conn, nil
}

func (c *Client) MultiplexEnabled() bool {
	return c.http2
}

func (c *Client) SetKeepIdleConnections(keep bool) {
	c.closeIdle.Store(!keep)
	if !keep {
		c.CloseIdleConnections()
	}
}

func (c *Client) CloseIdleConnections() {
	if c.pool != nil {
		c.pool.closeIdle()
		return
	}
	CloseIdleConnections(c.transport.Load())
}

func (c *Client) Close() error {
	if c.pool != nil {
		c.pool.closeAll()
	} else {
		c.transport.Store(ResetTransport(c.transport.Load()))
	}
	// Unregister last: a torn-down pool must never be reached by a later generation sweep.
	if c.removeRegistration != nil {
		c.removeRegistration()
		c.removeRegistration = nil
	}
	return nil
}
