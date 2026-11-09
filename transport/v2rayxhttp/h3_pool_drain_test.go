//go:build with_xhttp && with_quic

package v2rayxhttp

// The XHTTP H3 row: a real H3 outbound, the production network-change action, a healthy old flow
// that keeps carrying bytes, a new flow that lands in a NEW pool generation, and the old connection
// released at the end.
//
// # What was missing
//
// The VLESS wiring test only proved that InterfaceUpdated reaches Reset, and the pool tests ran with
// an H2/raw transport. The row asks for the three things an H3 pool does on a physical change:
//
//	drain      the old stream keeps its transport and keeps echoing
//	new pool   the next stream cannot reuse that connection, so it opens one in the new generation
//	release    the draining connection is closed once its last stream ends
//
// # Why the server is real
//
// This is the same fixture the H3 round-trip test uses - a real http3.Server on a real UDP socket with
// the XHTTP stream handler - extended to a long-lived echo so the stream can carry bytes on both sides
// of the change. No mock transport is involved: the client's HTTP/3 connection, its pool bookkeeping
// and the server's request lifecycle are the production ones.

import (
	"context"
	stdTLS "crypto/tls"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go/http3"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// startH3EchoServer serves the XHTTP stream handler on a real HTTP/3 endpoint and reports how many
// request streams have finished, which is the server-side half of "the old connection was released".
func startH3EchoServer(t *testing.T) (*http3.Server, M.Socksaddr, []byte, *atomic.Int32) {
	t.Helper()
	certificate, hash := testCertificate(t)
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := &atomic.Int32{}
	server := &http3.Server{
		TLSConfig: http3.ConfigureTLSConfig(&stdTLS.Config{Certificates: []stdTLS.Certificate{*certificate}}),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			// Long-lived echo: every 4 bytes that arrive are echoed back immediately, so the same
			// stream can carry a byte before AND after the network change.
			buffer := make([]byte, 4)
			for {
				if _, readErr := io.ReadFull(request.Body, buffer); readErr != nil {
					break
				}
				if _, writeErr := writer.Write(buffer); writeErr != nil {
					break
				}
				writer.(http.Flusher).Flush()
			}
			finished.Add(1)
		}),
	}
	go func() { _ = server.Serve(packetConn) }()
	t.Cleanup(func() { _ = server.Close() })
	return server, M.SocksaddrFromNet(packetConn.LocalAddr()), hash, finished
}

// newH3PoolClient builds the production XHTTP client in stream-one mode against the fixture.
func newH3PoolClient(t *testing.T, serverAddress M.Socksaddr, hash []byte) *Client {
	t.Helper()
	tlsConfig, err := boxTLS.NewClient(context.Background(), log.NewNOPFactory().Logger(), "localhost", option.OutboundTLSOptions{
		Enabled:                    true,
		ServerName:                 "localhost",
		ALPN:                       []string{"h3"},
		CertificatePublicKeySHA256: [][]byte{hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewClient(context.Background(), N.SystemDialer, serverAddress,
		option.V2RayXHTTPOptions{Mode: modeStreamOne, Path: "/xhttp/"}, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	client, isClient := transport.(*Client)
	if !isClient {
		t.Fatalf("NewClient built %T", transport)
	}
	if client.httpVersion != httpVersion3 {
		t.Fatalf("the fixture must exercise HTTP/3, got %s", client.httpVersion)
	}
	return client
}

// echoThrough writes payload on the stream and requires the same bytes back.
func echoThrough(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write %q: %v", payload, err)
	}
	buffer := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read echo of %q: %v", payload, err)
	}
	if string(buffer) != payload {
		t.Fatalf("echo = %q, want %q", buffer, payload)
	}
}

func TestTheH3PoolDrainsAHealthyFlowAndOpensANewGeneration(t *testing.T) {
	_, serverAddress, hash, finished := startH3EchoServer(t)
	client := newH3PoolClient(t, serverAddress, hash)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oldFlow, err := client.DialContext(ctx)
	if err != nil {
		t.Fatalf("dial the first flow: %v", err)
	}
	echoThrough(t, oldFlow, "ping")
	before := client.xmux.stats()
	if before.Opened != 1 || before.Active != 1 {
		t.Fatalf("unexpected starting inventory: %+v", before)
	}

	// The production network-change action (what InterfaceUpdated calls; see protocol/vless).
	if err := client.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// (1) DRAIN: the stream that was already open keeps carrying bytes through its old transport.
	echoThrough(t, oldFlow, "pong")
	draining := client.xmux.stats()
	if draining.Generation != before.Generation+1 {
		t.Fatalf("the pool generation did not move: %+v", draining)
	}
	if draining.Draining != 1 || draining.DrainingOldGeneration != 1 {
		t.Fatalf("the old connection was not detached for draining: %+v", draining)
	}

	// (2) NEW GENERATION: a new stream cannot reuse the retired connection, so it opens one in the
	// new generation. The old stream is still open, so the pool now holds two connections.
	newFlow, err := client.DialContext(ctx)
	if err != nil {
		t.Fatalf("dial the post-change flow: %v", err)
	}
	echoThrough(t, newFlow, "next")
	after := client.xmux.stats()
	if after.Opened != before.Opened+1 {
		t.Fatalf("the post-change flow reused the retired connection instead of opening one in the "+
			"new generation: %+v", after)
	}
	if after.Active != 1 || after.Draining != 1 {
		t.Fatalf("the old generation should be draining while the new one is active: %+v", after)
	}

	// (3) RELEASE: closing the drained flow ends its connection, on both sides - the pool's Released
	// counter moves and the server's request handler returns.
	if err := oldFlow.Close(); err != nil {
		t.Fatalf("close the drained flow: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		released := client.xmux.stats()
		if released.Released >= 1 && released.Pooled == 1 && finished.Load() >= 1 {
			t.Logf("MEASURED old H3 connection released: %+v", released)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the drained connection was never released: pool=%+v server-finished=%d",
				released, finished.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The new generation is unaffected. Closing its stream returns the connection to the pool - an
	// idle connection is kept, not closed - and the lifecycle's final Close releases it.
	echoThrough(t, newFlow, "last")
	_ = newFlow.Close()
	deadline = time.Now().Add(15 * time.Second)
	for {
		idle := client.xmux.stats()
		if idle.Active == 0 && idle.Idle == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the post-change stream never returned its connection to the pool: %+v", idle)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = client.Close()
	deadline = time.Now().Add(15 * time.Second)
	for {
		final := client.xmux.stats()
		if final.Released == 2 && final.Pooled == 0 && finished.Load() >= 2 {
			t.Logf("MEASURED both H3 connections released: %+v", final)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pool did not return to empty: %+v (server-finished=%d)", final, finished.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
