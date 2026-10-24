//go:build darwin && cgo

package tls

import (
	"crypto/tls"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"golang.org/x/sys/unix"
)

// The Apple engine used to dup the caller's socket with unix.Dup and release the anchor and
// network objects on only some paths. A successful handshake leaked the duplicated fd into the
// nw_connection object, so every reconnect raised the process fd count. This test keeps one
// listener and performs many complete handshake/close cycles; the count must return to its
// baseline rather than grow with the number of cycles.
func TestAppleClientHandshakeDoesNotLeakFileDescriptors(t *testing.T) {
	serverCertificate, serverCertificatePEM := newAppleTestCertificate(t, "localhost")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			tlsConn := tls.Server(conn, &tls.Config{
				Certificates: []tls.Certificate{serverCertificate},
				MinVersion:   tls.VersionTLS12,
				MaxVersion:   tls.VersionTLS12,
				NextProtos:   []string{"h2"},
			})
			if handshakeErr := tlsConn.Handshake(); handshakeErr != nil {
				_ = tlsConn.Close()
				serverDone <- handshakeErr
				return
			}
			_ = tlsConn.Close()
			serverDone <- nil
		}
	}()

	options := option.OutboundTLSOptions{
		Enabled:     true,
		Engine:      "apple",
		ServerName:  "localhost",
		MinVersion:  "1.2",
		MaxVersion:  "1.2",
		ALPN:        badoption.Listable[string]{"h2"},
		Certificate: badoption.Listable[string]{serverCertificatePEM},
	}

	// Warm up once: the first connection creates the engine and its dispatch queue, which are
	// process-lifetime objects and must not be counted as per-connection growth.
	warmClient, err := newAppleTestClientConn(t, listener.Addr().String(), options)
	if err != nil {
		t.Fatal(err)
	}
	_ = warmClient.Close()
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	baseline := appleOpenFileDescriptors(t)

	const cycles = 30
	for index := range cycles {
		clientConn, dialErr := newAppleTestClientConn(t, listener.Addr().String(), options)
		if dialErr != nil {
			t.Fatalf("cycle %d: %v", index, dialErr)
		}
		if closeErr := clientConn.Close(); closeErr != nil {
			t.Fatalf("cycle %d close: %v", index, closeErr)
		}
		if serverErr := <-serverDone; serverErr != nil {
			t.Fatalf("cycle %d server: %v", index, serverErr)
		}
	}

	_ = listener.Close()
	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	after := appleOpenFileDescriptors(t)
	if after > baseline+2 {
		t.Fatalf("file descriptors grew from %d to %d over %d handshakes", baseline, after, cycles)
	}
}

// A failed handshake must release its duplicated socket and network object too.
func TestAppleClientHandshakeFailureDoesNotLeakFileDescriptors(t *testing.T) {
	serverCertificate, _ := newAppleTestCertificate(t, "localhost")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{}, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			tlsConn := tls.Server(conn, &tls.Config{
				Certificates: []tls.Certificate{serverCertificate},
				MinVersion:   tls.VersionTLS12,
			})
			_ = tlsConn.Handshake()
			_ = tlsConn.Close()
			serverDone <- struct{}{}
		}
	}()

	options := option.OutboundTLSOptions{
		Enabled:    true,
		Engine:     "apple",
		ServerName: "localhost",
		MinVersion: "1.2",
		MaxVersion: "1.2",
		// No certificate: verification fails on every attempt.
	}
	baseline := appleOpenFileDescriptors(t)
	for index := range 10 {
		clientConn, dialErr := newAppleTestClientConn(t, listener.Addr().String(), options)
		if dialErr == nil {
			_ = clientConn.Close()
			t.Fatalf("attempt %d unexpectedly verified an unknown certificate", index)
		}
		<-serverDone
	}
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := appleOpenFileDescriptors(t)
	if after > baseline+2 {
		t.Fatalf("file descriptors grew from %d to %d over failed handshakes", baseline, after)
	}
}

func appleOpenFileDescriptors(t *testing.T) int {
	t.Helper()
	limit, err := unix.Getdtablesize()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for fd := 0; fd < limit; fd++ {
		if _, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			count++
		}
	}
	return count
}
