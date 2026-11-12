package masque

// Q16's remaining cell: a SINGLE saturated stream.
//
// The rest of the Q16 evidence covers the transport's own queues and the multi-stream case (a slow
// consumer on one stream must not stop another stream). The cell that was left open is the one where
// the only stream on the connection is the slow consumer: the application never reads, so the
// sender's stream-level window is closed and no application data can be acknowledged.
//
// The claim to prove is narrow and belongs to the pinned QUIC implementation, not to MASQUE: while
// that one stream is saturated, the connection's control plane still progresses (keepalive keeps the
// connection open past its idle timeout, a second stream can still be opened and completed), a
// cancellation of the blocked writer still progresses, and the stream flow-control window is
// returned only when the receiver actually reads bytes. This test drives the pinned quic-go package
// directly, which is the layer the audit named as the owner of this cell.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"

	"github.com/stretchr/testify/require"
)

func newSlowStreamTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"single-slow-stream"},
	}
}

type slowStreamServer struct {
	firstStreamRead chan int
	firstReadResult chan string
	secondStream    chan struct{}
}

func startSlowStreamServer(t *testing.T, listener *quic.Listener) *slowStreamServer {
	t.Helper()
	server := &slowStreamServer{
		firstStreamRead: make(chan int, 1),
		firstReadResult: make(chan string, 1),
		secondStream:    make(chan struct{}, 1),
	}
	go func() {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			return
		}
		var first atomic.Bool
		for {
			stream, acceptErr := conn.AcceptStream(context.Background())
			if acceptErr != nil {
				return
			}
			if !first.Swap(true) {
				// The slow consumer: read nothing until the test asks, then read exactly one chunk.
				go func() {
					amount := <-server.firstStreamRead
					buffer := make([]byte, amount)
					read, readErr := io.ReadFull(stream, buffer)
					server.firstReadResult <- fmt.Sprintf("read %d of %d: %v", read, amount, readErr)
				}()
				continue
			}
			go func() {
				buffer := make([]byte, 4)
				_, readErr := io.ReadFull(stream, buffer)
				if readErr == nil {
					_, _ = stream.Write(buffer)
				}
				server.secondStream <- struct{}{}
				_ = stream.Close()
			}()
		}
	}()
	return server
}

// TestASingleSaturatedStreamStillHasAControlPlane is the open Q16 cell.
func TestASingleSaturatedStreamStillHasAControlPlane(t *testing.T) {
	tlsConfig := newSlowStreamTLSConfig(t)
	quicConfig := &quic.Config{
		InitialStreamReceiveWindow:     64 * 1024,
		MaxStreamReceiveWindow:         64 * 1024,
		InitialConnectionReceiveWindow: 96 * 1024,
		MaxConnectionReceiveWindow:     96 * 1024,
		KeepAlivePeriod:                50 * time.Millisecond,
		// Shorter than the observation below: without keepalive the connection would be gone by the
		// time the second stream is opened, so the observation also proves PINGs progressed.
		MaxIdleTimeout: time.Second,
	}
	listener, err := quic.ListenAddr("127.0.0.1:0", tlsConfig, quicConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	server := startSlowStreamServer(t, listener)

	clientTLS := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"single-slow-stream"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, listener.Addr().String(), clientTLS, quicConfig)
	require.NoError(t, err)
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(ctx)
	require.NoError(t, err)

	// Fill the stream until the flow-control window closes. The writer keeps trying so the count
	// settles at the window the receiver granted; no deadline is set, the test cancels it later.
	var written atomic.Int64
	writeDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		for {
			writtenNow, writeErr := stream.Write(buffer)
			written.Add(int64(writtenNow))
			if writeErr != nil {
				writeDone <- writeErr
				return
			}
		}
	}()

	settle := func() int64 {
		var last int64
		stable := time.Now()
		for time.Since(stable) < 300*time.Millisecond {
			time.Sleep(20 * time.Millisecond)
			current := written.Load()
			if current != last {
				last = current
				stable = time.Now()
			}
		}
		return last
	}
	blockedAt := settle()
	t.Logf("blocked after %d bytes", blockedAt)
	require.Greater(t, blockedAt, int64(0), "the saturated stream never wrote anything")
	select {
	case writeErr := <-writeDone:
		t.Fatalf("the saturated writer returned before cancellation: %v", writeErr)
	default:
	}

	// Past the idle timeout with no application progress. Keepalive must have kept the connection
	// alive: a second stream is opened and answered, which needs control frames in both directions.
	time.Sleep(1500 * time.Millisecond)
	second, err := conn.OpenStreamSync(ctx)
	require.NoError(t, err, "the connection died while one stream was saturated: keepalive did not progress")
	_, err = second.Write([]byte("ping"))
	require.NoError(t, err)
	echo := make([]byte, 4)
	require.NoError(t, second.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(second, echo)
	require.NoError(t, err)
	require.Equal(t, "ping", string(echo), "a second stream must complete while the only other stream is saturated")
	_ = second.Close()
	select {
	case <-server.secondStream:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never completed the second stream")
	}

	// Window credit is returned only by a real read: nothing moved during the saturation window, and
	// one read chunk unblocks at most that much again. The chunk is larger than the stream window's
	// update threshold; a smaller read legitimately returns no credit yet.
	stillBlocked := written.Load()
	require.Equal(t, blockedAt, stillBlocked, "the stream window grew without the receiver reading")
	server.firstStreamRead <- 48 * 1024
	select {
	case result := <-server.firstReadResult:
		t.Logf("slow reader: %s", result)
	case <-time.After(5 * time.Second):
		t.Log("slow reader did not finish the read")
	}
	require.Eventually(t, func() bool { return written.Load() > stillBlocked }, 5*time.Second, 20*time.Millisecond,
		"reading 48 KiB did not return any stream credit")
	require.LessOrEqual(t, written.Load(), stillBlocked+64*1024,
		"more credit was returned than the receiver consumed")

	// Cancelling the blocked writer must progress even though the peer never read the data.
	stream.CancelWrite(0)
	select {
	case <-writeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("CancelWrite did not unblock the saturated stream writer")
	}
}
