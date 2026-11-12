//go:build windows

package tls

import (
	"context"
	stdtls "crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/schannel"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
)

// windowsTLS13Available reports whether this machine can negotiate TLS 1.3 at
// all, so a test can assert the supported branch where the platform has it and
// the refusal branch where it does not.
//
// A probe failure is a test failure, not a skip: "could not ask Schannel" is a
// different outcome from "Schannel said no", and the package treats it that way.
func windowsTLS13Available(t *testing.T) bool {
	t.Helper()
	if schannel.TLS13Supported() {
		return true
	}
	probeErr := schannel.TLS13ProbeError()
	if probeErr != nil {
		t.Fatalf("TLS 1.3 capability probe failed: %v", probeErr)
	}
	t.Log("this Windows build cannot negotiate TLS 1.3; asserting the refusal branch")
	return false
}

// recordingConn counts what a handshake actually writes to the wire.
//
// It exists so the refusal branch can assert "nothing was sent" instead of only
// "the call returned an error": the defect this guards against was a DTLS-shaped
// ClientHello leaving the process before any error surfaced, so an error alone
// is not enough evidence.
type recordingConn struct {
	net.Conn

	access     sync.Mutex
	writeBytes int
	writeCalls int
}

func (c *recordingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.access.Lock()
	c.writeCalls++
	c.writeBytes += n
	c.access.Unlock()
	return n, err
}

func (c *recordingConn) recordedWrites() (calls int, bytes int) {
	c.access.Lock()
	defer c.access.Unlock()
	return c.writeCalls, c.writeBytes
}

// assertTLS13Refusal drives the production handshake path against a TLS 1.3-only
// server on a machine that cannot do TLS 1.3, and requires a precise capability
// error with no handshake bytes written.
func assertTLS13Refusal(t *testing.T) {
	t.Helper()

	serverCertificate, serverCertificatePEM := newWindowsTestCertificate(t, "localhost")
	serverResult, serverAddress := startWindowsTLSTestServer(t, &stdtls.Config{
		Certificates: []stdtls.Certificate{serverCertificate},
		MinVersion:   stdtls.VersionTLS13,
		MaxVersion:   stdtls.VersionTLS13,
	})

	clientConn, err := newWindowsTestClientConn(t, serverAddress, option.OutboundTLSOptions{
		Enabled:     true,
		Engine:      C.TLSEngineWindows,
		ServerName:  "localhost",
		MinVersion:  "1.3",
		Certificate: badoption.Listable[string]{serverCertificatePEM},
	})
	if err == nil {
		_ = clientConn.Close()
		t.Fatal("expected a TLS 1.3 request to be refused on a machine without TLS 1.3")
	}
	if !errors.Is(err, schannel.ErrVersionUnavailable) {
		t.Fatalf("expected ErrVersionUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "TLS 1.3") {
		t.Fatalf("refusal does not name the requested version: %v", err)
	}

	result := <-serverResult
	if result.err == nil {
		t.Fatal("server completed a handshake, so the client sent a hello after all")
	}
}

// TestWindowsClientTLS13CapabilityBoundary is the Q29 regression: whichever
// branch this machine is in, a TLS 1.3 requirement is either satisfied by a
// TLS 1.3 handshake or refused with a capability error - never surfaced as a
// downstream EOF from a hello the peer rejected.
func TestWindowsClientTLS13CapabilityBoundary(t *testing.T) {
	available := windowsTLS13Available(t)

	serverCertificate, serverCertificatePEM := newWindowsTestCertificate(t, "localhost")
	serverResult, serverAddress := startWindowsTLSTestServer(t, &stdtls.Config{
		Certificates: []stdtls.Certificate{serverCertificate},
		MinVersion:   stdtls.VersionTLS13,
		MaxVersion:   stdtls.VersionTLS13,
	})

	ctx, cancel := context.WithTimeout(context.Background(), windowsTLSTestTimeout)
	t.Cleanup(cancel)
	clientConfig, err := NewClientWithOptions(ClientOptions{
		Context: ctx,
		Logger:  logger.NOP(),
		Options: option.OutboundTLSOptions{
			Enabled:     true,
			Engine:      C.TLSEngineWindows,
			ServerName:  "localhost",
			MinVersion:  "1.3",
			Certificate: badoption.Listable[string]{serverCertificatePEM},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rawConn, err := net.DialTimeout("tcp", serverAddress, windowsTLSTestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	recorded := &recordingConn{Conn: rawConn}
	tlsConn, handshakeErr := ClientHandshake(ctx, recorded, clientConfig)
	if handshakeErr != nil {
		rawConn.Close()
	}
	writes, writeBytes := recorded.recordedWrites()

	if !available {
		if handshakeErr == nil {
			_ = tlsConn.Close()
			t.Fatal("expected a capability refusal, got a connection")
		}
		if !errors.Is(handshakeErr, schannel.ErrVersionUnavailable) {
			t.Fatalf("expected ErrVersionUnavailable, got %v", handshakeErr)
		}
		if writes != 0 || writeBytes != 0 {
			t.Fatalf("refusal wrote %d handshake record(s), %d bytes; the capability check must run before the wire", writes, writeBytes)
		}
		result := <-serverResult
		if result.err == nil {
			t.Fatal("server completed a handshake although the client refused before writing")
		}
		return
	}

	if handshakeErr != nil {
		t.Fatalf("TLS 1.3 handshake failed on a machine that reports TLS 1.3 support: %v", handshakeErr)
	}
	defer tlsConn.Close()
	if state := tlsConn.ConnectionState(); state.Version != stdtls.VersionTLS13 {
		t.Fatalf("expected TLS 1.3, got %x", state.Version)
	}
	if writes == 0 {
		t.Fatal("a successful handshake wrote nothing")
	}
	result := <-serverResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.state.Version != stdtls.VersionTLS13 {
		t.Fatalf("server negotiated %x", result.state.Version)
	}
}

// TestWindowsClientTLS13NegotiationEnforced proves the post-handshake seam: a
// machine that CAN do TLS 1.3 still must refuse when the peer answers below the
// required minimum, and the error names both versions.
func TestWindowsClientTLS13NegotiationEnforced(t *testing.T) {
	restore := schannel.SetTLS13SupportForTest(true)
	t.Cleanup(restore)

	serverCertificate, serverCertificatePEM := newWindowsTestCertificate(t, "localhost")
	serverResult, serverAddress := startWindowsTLSTestServer(t, &stdtls.Config{
		Certificates: []stdtls.Certificate{serverCertificate},
		MinVersion:   stdtls.VersionTLS12,
		MaxVersion:   stdtls.VersionTLS12,
	})

	clientConn, err := newWindowsTestClientConn(t, serverAddress, option.OutboundTLSOptions{
		Enabled:     true,
		Engine:      C.TLSEngineWindows,
		ServerName:  "localhost",
		MinVersion:  "1.3",
		Certificate: badoption.Listable[string]{serverCertificatePEM},
	})
	if err == nil {
		_ = clientConn.Close()
		t.Fatal("expected a downgrade to TLS 1.2 to be refused when TLS 1.3 is required")
	}
	if !errors.Is(err, schannel.ErrVersionNotNegotiated) {
		t.Fatalf("expected ErrVersionNotNegotiated, got %v", err)
	}
	if !strings.Contains(err.Error(), "TLS 1.2") || !strings.Contains(err.Error(), "TLS 1.3") {
		t.Fatalf("refusal must name negotiated and required versions: %v", err)
	}

	result := <-serverResult
	if result.err != nil {
		t.Fatalf("the refusal must happen after a completed TLS 1.2 handshake, server said: %v", result.err)
	}
	if result.state.Version != stdtls.VersionTLS12 {
		t.Fatalf("server negotiated %x, expected TLS 1.2", result.state.Version)
	}
}

// TestWindowsClientVersionRangeStillNegotiates guards the common case the
// capability layer must not disturb: an unconstrained range negotiates the
// platform's best version and passes the post-handshake check.
func TestWindowsClientVersionRangeStillNegotiates(t *testing.T) {
	serverCertificate, serverCertificatePEM := newWindowsTestCertificate(t, "localhost")
	serverResult, serverAddress := startWindowsTLSTestServer(t, &stdtls.Config{
		Certificates: []stdtls.Certificate{serverCertificate},
		MinVersion:   stdtls.VersionTLS12,
		MaxVersion:   stdtls.VersionTLS13,
	})

	clientConn, err := newWindowsTestClientConn(t, serverAddress, option.OutboundTLSOptions{
		Enabled:     true,
		Engine:      C.TLSEngineWindows,
		ServerName:  "localhost",
		Certificate: badoption.Listable[string]{serverCertificatePEM},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	result := <-serverResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	negotiated := clientConn.ConnectionState().Version
	if negotiated < stdtls.VersionTLS12 {
		t.Fatalf("negotiated version below the default minimum: %x", negotiated)
	}
	if result.state.Version != negotiated {
		t.Fatalf("client reports %x, server reports %x", negotiated, result.state.Version)
	}
}
