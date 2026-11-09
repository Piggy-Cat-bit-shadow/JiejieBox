package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"net"
	"net/http"
	"strings"

	"github.com/sagernet/sing-box/common/tls"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/idna"
)

func dialTLS(ctx context.Context, rawDialer N.Dialer, baseTLSConfig tls.Config, destination M.Socksaddr, nextProtos []string, expectProto string) (net.Conn, error) {
	if baseTLSConfig == nil {
		return nil, E.New("TLS transport unavailable")
	}
	tlsConfig := baseTLSConfig.Clone()
	if tlsConfig.ServerName() == "" && destination.IsValid() {
		tlsConfig.SetServerName(destination.AddrString())
	}
	tlsConfig.SetNextProtos(nextProtos)
	conn, err := rawDialer.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return nil, err
	}
	tlsConn, err := tls.ClientHandshake(ctx, conn, tlsConfig)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if expectProto != "" && tlsConn.ConnectionState().NegotiatedProtocol != expectProto {
		tlsConn.Close()
		return nil, errHTTP2Fallback
	}
	return tlsConn, nil
}

func applyHeaders(request *http.Request, headers http.Header, host string) {
	for header, values := range headers {
		request.Header[header] = append([]string(nil), values...)
	}
	if host != "" {
		request.Host = host
	}
}

func requestRequiresHTTP1(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(request.Header.Get("Upgrade"), "websocket")
}

// requestReplayable reports whether the request body can be produced again.
//
// It says nothing about whether the OPERATION may be repeated: see requestIdempotent.
func requestReplayable(request *http.Request) bool {
	return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
}

// requestIdempotent reports whether repeating this operation is harmless, which is a DIFFERENT
// question from whether its body can be rebuilt.
//
// RFC 9110 §9.2.2 defines the idempotent methods. The list accepted here is deliberately narrower
// than the definition's envelope, because the HTTP/3 fallback uses it to run two attempts
// CONCURRENTLY and concurrently is stronger than sequentially: PUT and DELETE are idempotent in
// effect, but racing two of them is not a repetition of a completed operation. A caller that knows
// its POST is safely repeatable can say so with the conventional Idempotency-Key header.
func requestIdempotent(request *http.Request) bool {
	switch request.Method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return request.Header.Get("Idempotency-Key") != "" || request.Header.Get("X-Idempotency-Key") != ""
}

// cloneRequestForRetry builds a second request whose body is rebuilt through GetBody.
//
// It returns an error instead of panicking. GetBody is caller-provided code, and a GetBody that
// fails on a live request used to crash the process: the HTTP/3 race called it inside a goroutine
// leg and the HTTP/2 -> HTTP/1.1 fallback called it on the request path.
func cloneRequestForRetry(request *http.Request) (*http.Request, error) {
	cloned := request.Clone(request.Context())
	if request.Body != nil && request.Body != http.NoBody && request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, E.Cause(err, "rebuild request body")
		}
		cloned.Body = body
	}
	return cloned, nil
}

// cloneRequestForRace builds one request per racing leg, each with its own rebuilt body.
//
// Both clones are made BEFORE any leg starts, so a failing GetBody is an ordinary error on the
// request path rather than a panic inside a goroutine that nobody can recover.
func cloneRequestForRace(request *http.Request) (*http.Request, *http.Request, error) {
	h3Request, err := cloneRequestForRetry(request)
	if err != nil {
		return nil, nil, err
	}
	h2Request, err := cloneRequestForRetry(request)
	if err != nil {
		closeRequestBody(h3Request)
		return nil, nil, err
	}
	return h3Request, h2Request, nil
}

// closeRequestBody closes a request body, tolerating the two shapes that have no body to close.
func closeRequestBody(request *http.Request) {
	if request != nil && request.Body != nil && request.Body != http.NoBody {
		request.Body.Close()
	}
}

func requestAuthority(request *http.Request) string {
	if request == nil || request.URL == nil || request.URL.Host == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(request.URL.Host)
	if err != nil {
		host = request.URL.Host
		port = ""
	}
	if port == "" {
		if request.URL.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host + ":" + port
	}
	ascii, idnaErr := idna.Lookup.ToASCII(host)
	if idnaErr == nil {
		host = ascii
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, port)
}

func buildSTDTLSConfig(baseTLSConfig tls.Config, destination M.Socksaddr, nextProtos []string) (*stdTLS.Config, error) {
	if baseTLSConfig == nil {
		return nil, nil
	}
	tlsConfig := baseTLSConfig.Clone()
	if tlsConfig.ServerName() == "" && destination.IsValid() {
		tlsConfig.SetServerName(destination.AddrString())
	}
	tlsConfig.SetNextProtos(nextProtos)
	return tlsConfig.STDConfig()
}
