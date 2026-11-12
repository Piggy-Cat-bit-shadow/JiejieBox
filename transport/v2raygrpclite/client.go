package v2raygrpclite

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"golang.org/x/net/http2"
)

var _ adapter.V2RayMultiplexClientTransport = (*Client)(nil)

var defaultClientHeader = http.Header{
	"Content-Type": []string{"application/grpc"},
	"User-Agent":   []string{"grpc-go/1.48.0"},
	"TE":           []string{"trailers"},
}

type Client struct {
	ctx        context.Context
	serverAddr M.Socksaddr
	// transport is the CURRENT generation's transport. It is kept as a field because the pool and
	// this client are always created together; the pool is what owns the retiring.
	transport *http2.Transport
	options   option.V2RayGRPCOptions
	url       *url.URL
	host      string
	closeIdle atomic.Bool
	// pool owns the HTTP/2 generation. See generation.go: it is v2rayhttp's pool, not a copy.
	pool *v2rayhttp.GenerationPool
	// newTransport builds the next generation with the same dialer and TLS configuration.
	newTransport func() *http2.Transport
	// A nil coordinator yields a nil registration and every check is a no-op, which is the previous
	// behaviour for embedders.
	coordinator        *runtimecoord.Coordinator
	registration       *runtimecoord.Registration
	removeRegistration func()
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayGRPCOptions, tlsConfig tls.Config) adapter.V2RayClientTransport {
	var host string
	if tlsConfig != nil && tlsConfig.ServerName() != "" {
		host = M.ParseSocksaddrHostPort(tlsConfig.ServerName(), serverAddr.Port).String()
	} else {
		host = serverAddr.String()
	}
	clientPath, clientRawPath := serviceURLPath(options.ServiceName)
	var tlsDialer tls.Dialer
	if tlsConfig != nil {
		if len(tlsConfig.NextProtos()) == 0 {
			tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
		}
		tlsDialer = tls.NewDialer(dialer, tlsConfig)
	}
	// newHTTP2 is the generation constructor: the pool rebuilds a generation with the same dialer and
	// TLS configuration, so a retired generation can be replaced without re-reading options. The dial
	// function belongs INSIDE this constructor - a generation built without it would dial the server
	// with the library's default TLS configuration and fail exactly where the previous network was
	// replaced.
	newHTTP2 := func() *http2.Transport {
		transport := &http2.Transport{
			ReadIdleTimeout:    time.Duration(options.IdleTimeout),
			PingTimeout:        time.Duration(options.PingTimeout),
			DisableCompression: true,
		}
		if tlsConfig == nil {
			transport.DialTLSContext = func(ctx context.Context, network, addr string, cfg *tls.STDConfig) (net.Conn, error) {
				return dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
			}
		} else {
			transport.DialTLSContext = func(ctx context.Context, network, addr string, cfg *tls.STDConfig) (net.Conn, error) {
				return tlsDialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
			}
		}
		return transport
	}
	client := &Client{
		ctx:          ctx,
		serverAddr:   serverAddr,
		options:      options,
		transport:    newHTTP2(),
		newTransport: newHTTP2,
		// Path and RawPath together are what make the request line the reference's string; see
		// service_name.go for why neither alone can.
		url: &url.URL{
			Scheme:  "https",
			Host:    serverAddr.String(),
			Path:    clientPath,
			RawPath: clientRawPath,
		},
		host: host,
	}
	client.pool = v2rayhttp.NewGenerationPool(client.transport)
	client.coordinator = service.FromContext[*runtimecoord.Coordinator](ctx)
	client.registration, client.removeRegistration = generationRegistration(
		client.coordinator, "v2raygrpclite/"+serverAddr.String())
	return client
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	// The hand-out is where "this pool belongs to a previous network" must be answered: the listener
	// phase has not run yet, and http2.Transport would otherwise reuse a connection whose path just
	// ended. Retiring the transport removes the whole previous generation from new streams;
	// connections that still carry a stream finish on the path they started on.
	if c.generationStale() {
		c.retireGeneration()
	}
	// The generation this stream is being built FOR. A creation that started before a network change
	// and finished after it produced a result bound to the path that just ended; publishing it as
	// current would hand every later demand a stream the audit called stale.
	startGeneration := c.currentGeneration()

	pipeInReader, pipeInWriter := io.Pipe()
	requestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	request := &http.Request{
		Method: http.MethodPost,
		Body:   pipeInReader,
		URL:    c.url,
		Header: defaultClientHeader,
		Host:   c.host,
	}
	conn := newLateGunConn(pipeInWriter, cancel)
	keepSession := adapter.KeepSessionFromContext(ctx)
	// The generation is pinned for the whole stream and released EXACTLY once: the error paths below
	// release it as soon as the attempt ends, and the conn's own close releases it for a stream that
	// was published. The once guard is what keeps a late close from decrementing it twice.
	generation := c.pool.Begin()
	var generationOnce sync.Once
	releaseGeneration := func() {
		generationOnce.Do(func() { c.pool.Finish(generation) })
	}
	conn.onClose = func() {
		releaseGeneration()
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
	// gRPC servers send the response headers together with the first message, so RoundTrip
	// returning does not mark the stream as established; the request headers being written does.
	request = request.WithContext(httptrace.WithClientTrace(requestCtx, &httptrace.ClientTrace{
		WroteHeaders: func() {
			handshakeTimer.Stop()
		},
	}))
	go func() {
		response, err := generation.Transport().RoundTrip(request)
		handshakeTimer.Stop()
		if err != nil {
			releaseGeneration()
			if handshakeTimedOut.Load() {
				err = os.ErrDeadlineExceeded
			}
			conn.setup(nil, err)
		} else if response.StatusCode != 200 {
			response.Body.Close()
			releaseGeneration()
			conn.setup(nil, E.New("v2ray-grpc: unexpected status: ", response.Status))
		} else if c.currentGeneration() != startGeneration {
			// The result belongs to a generation that ended while it was in flight. Discard it:
			// acknowledging it would claim the pool is current on evidence from the old network.
			response.Body.Close()
			releaseGeneration()
			conn.setup(nil, staleGenerationError{})
		} else {
			c.acknowledgeGeneration()
			conn.setup(response.Body, nil)
		}
	}()
	return conn, nil
}

func (c *Client) MultiplexEnabled() bool {
	return true
}

func (c *Client) SetKeepIdleConnections(keep bool) {
	c.closeIdle.Store(!keep)
	if !keep {
		c.CloseIdleConnections()
	}
}

func (c *Client) CloseIdleConnections() {
	if c.pool != nil {
		c.pool.CloseIdle()
		return
	}
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

func (c *Client) Close() error {
	if c.pool != nil {
		c.pool.CloseAll()
	} else if c.transport != nil {
		v2rayhttp.ResetTransport(c.transport)
	}
	// Unregister last: a torn-down pool must never be reached by a later generation sweep.
	if c.removeRegistration != nil {
		c.removeRegistration()
		c.removeRegistration = nil
	}
	return nil
}
