//go:build go1.27 && !http2legacy && badlinkname && tfogo_checklinkname0 && http2stdlibwrap

package http

import (
	"net"
	"net/http"
	"unsafe"

	"golang.org/x/net/http2"
)

type http2ClientConn struct {
	*http.ClientConn
}

func newHTTP2ClientConn(transport *http2.Transport, conn net.Conn) (*http2ClientConn, error) {
	clientConn, err := transport.NewClientConn(conn)
	if err != nil {
		return nil, err
	}
	return &http2ClientConn{ClientConn: *(**http.ClientConn)(unsafe.Pointer(clientConn))}, nil
}

func (c *http2ClientConn) isClosed() bool {
	return c.Err() != nil
}

// idle reports that the connection carries no stream. It is the only safe condition for the
// ordinary HTTP/2 pool trim: this pool has no session provenance, so active work must never be
// disturbed.
func (c *http2ClientConn) idle() bool {
	return c.InFlight() == 0
}

func (c *http2ClientConn) reserveNewRequest() bool {
	return c.Reserve() == nil
}

func (c *http2ClientConn) roundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get(":protocol") == "" {
		return c.ClientConn.RoundTrip(request)
	}
	internalClientConn := (*[2]unsafe.Pointer)(unsafe.Pointer(c.ClientConn))[1]
	return http2RoundTrip(request, func(clientRequest unsafe.Pointer) (unsafe.Pointer, error) {
		return netHTTPClientConnRoundTrip(internalClientConn, clientRequest)
	})
}

//go:linkname http2RoundTrip net/http.http2RoundTrip
func http2RoundTrip(request *http.Request, roundTrip func(clientRequest unsafe.Pointer) (unsafe.Pointer, error)) (*http.Response, error)

//go:linkname netHTTPClientConnRoundTrip net/http/internal/http2.NetHTTPClientConn.RoundTrip
func netHTTPClientConnRoundTrip(clientConn unsafe.Pointer, clientRequest unsafe.Pointer) (unsafe.Pointer, error)
