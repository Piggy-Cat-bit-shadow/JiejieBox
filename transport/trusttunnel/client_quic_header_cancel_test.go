//go:build with_quic

package trusttunnel

// The cancellation window in front of SendRequestHeader.
//
// SendRequestHeader performs a SYNCHRONOUS QUIC stream write. Once the peer's stream or connection
// flow-control window is exhausted, that write blocks inside quic-go until a window update arrives,
// and a context cancellation cannot reach it: only CancelWrite unblocks it. The abort callback used
// to be registered AFTER the header write, so a caller whose deadline expired while the write was
// blocked had no way to end the CONNECT, and the stream (with its flow-control credit) stayed held.
//
// This case builds the blocking condition on a REAL QUIC connection: the server advertises a
// one-byte stream and connection receive window and never reads, so the client's HTTP/3 control
// stream consumes that byte and the request HEADERS block. It then cancels the request context and
// requires the call to return promptly with the cancellation, which can only happen if the abort is
// armed before the write.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"

	"github.com/stretchr/testify/require"
)

func blockedHTTP3ServerCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startBlockedHTTP3Server starts a QUIC listener that completes the handshake but never reads a
// stream and advertises a one-byte receive window, so a client request cannot finish writing its
// HEADERS.
func startBlockedHTTP3Server(t *testing.T) *quic.Listener {
	t.Helper()
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{blockedHTTP3ServerCertificate(t)},
		NextProtos:   []string{http3.NextProtoH3},
	}, &quic.Config{
		HandshakeIdleTimeout:           5 * time.Second,
		MaxIdleTimeout:                 10 * time.Second,
		InitialStreamReceiveWindow:     1,
		MaxStreamReceiveWindow:         1,
		InitialConnectionReceiveWindow: 1,
		MaxConnectionReceiveWindow:     1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept(context.Background())
			if acceptErr != nil {
				return
			}
			// Hold the connection open without reading any stream: the advertised window never grows.
			defer conn.CloseWithError(0, "")
		}
	}()
	return listener
}

func TestHTTP3CancelUnblocksAHeaderWrite(t *testing.T) {
	listener := startBlockedHTTP3Server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	quicConn, err := quic.DialAddr(ctx, listener.Addr().String(), &tls.Config{
		ServerName:         "localhost",
		InsecureSkipVerify: true,
		NextProtos:         []string{http3.NextProtoH3},
	}, &quic.Config{HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 10 * time.Second})
	require.NoError(t, err)
	defer quicConn.CloseWithError(0, "")

	transport := &http3.Transport{DisableCompression: true}
	session := &http3Session{
		quicConn:   quicConn,
		clientConn: transport.NewClientConn(quicConn),
		closeSocket: func() {
		},
	}
	requestCtx, requestCancel := context.WithCancel(context.Background())
	// A header larger than one QUIC packet buffer is required to reach the blocking write: a small
	// request is copied into a STREAM frame and returned from immediately, so only a write that
	// exhausts the one-byte window actually parks inside quic-go. Random bytes keep QPACK from
	// compressing the value below that size.
	padding := make([]byte, 16*1024)
	_, err = rand.Read(padding)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, roundTripErr := session.roundTrip(requestCtx, "example.com:443", http.Header{
			"Proxy-Authorization": []string{"Basic dGVzdDp0ZXN0"},
			"X-Padding":           []string{base64.StdEncoding.EncodeToString(padding)},
		}, true)
		done <- roundTripErr
	}()

	// Give the request time to reach the blocked stream write, then cancel.
	select {
	case err = <-done:
		t.Fatalf("the CONNECT completed before cancellation on a one-byte window: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	started := time.Now()
	requestCancel()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.Canceled,
			"a cancelled blocked header write must report the caller's cancellation, not a transport verdict")
		require.Less(t, time.Since(started), 2*time.Second,
			"the cancelled header write did not return promptly; the abort is not armed before SendRequestHeader")
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled request stayed blocked in the header write")
	}
}
