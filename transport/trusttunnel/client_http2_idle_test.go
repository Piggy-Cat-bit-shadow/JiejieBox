package trusttunnel

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestAnIdleSessionSendsNoPing pins the "no PING: an idle session sends nothing" contract that
// newHTTP2Transport's comment states and that nothing executed before.
//
// # Why the contract is worth a test
//
// A TrustTunnel session is long-lived and mostly idle. A keep-alive PING would put frames on the
// wire for a tunnel carrying nothing: wake-ups on a phone, traffic a censor can pattern-match, and a
// session that can be closed by a peer that stops answering pings. The knob that decides this moved
// in Go 1.27 - net/http's HTTP2Config now carries SendPingTimeout/PingTimeout, and x/net's
// Transport.ReadIdleTimeout is a compatibility view onto it - which is exactly the kind of move that
// can flip a default without any line of this repository changing. The same move already changed the
// preface (see chromeMaxFrameSize).
//
// # Why there is a positive control
//
// "No PING arrived" is also what a broken observer sees. The second case configures a transport that
// MUST ping, so a zero in the first case is evidence about the transport rather than about the
// detector.
func TestAnIdleSessionSendsNoPing(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		readIdle time.Duration
		wantPing bool
		idleFor  time.Duration
	}{
		{name: "the transport the outbound builds", readIdle: 0, wantPing: false, idleFor: 600 * time.Millisecond},
		{name: "positive control: a transport told to ping", readIdle: 50 * time.Millisecond, wantPing: true, idleFor: 600 * time.Millisecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			serverTLS, clientTLS := newTestTLS(t)
			serverTLS.config.NextProtos = []string{http2.NextProtoTLS}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()

			transport, err := newHTTP2Transport()
			if err != nil {
				t.Fatal(err)
			}
			transport.ReadIdleTimeout = testCase.readIdle

			clientDone := make(chan error, 1)
			go func() {
				conn, dialErr := net.Dial("tcp", listener.Addr().String())
				if dialErr != nil {
					clientDone <- dialErr
					return
				}
				tlsConn := tls.Client(conn, clientTLS.config)
				if handshakeErr := tlsConn.HandshakeContext(context.Background()); handshakeErr != nil {
					clientDone <- handshakeErr
					return
				}
				clientConn, connErr := transport.NewClientConn(tlsConn)
				if connErr != nil {
					clientDone <- connErr
					return
				}
				// Hold the session open and idle for the observation window.
				time.Sleep(testCase.idleFor)
				_ = clientConn.Close()
				clientDone <- nil
			}()

			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			tlsConn := tls.Server(conn, serverTLS.config)
			if err = tlsConn.HandshakeContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			preface := make([]byte, len(http2.ClientPreface))
			if _, err = io.ReadFull(tlsConn, preface); err != nil {
				t.Fatal(err)
			}
			framer := http2.NewFramer(io.Discard, tlsConn)
			// Send the server SETTINGS so the client considers the connection established.
			if err = framer.WriteSettings(); err != nil {
				t.Fatal(err)
			}
			pings := 0
			deadline := time.Now().Add(testCase.idleFor)
			for {
				if remaining := time.Until(deadline); remaining > 0 {
					_ = tlsConn.SetReadDeadline(time.Now().Add(remaining))
				} else {
					break
				}
				frame, readErr := framer.ReadFrame()
				if readErr != nil {
					break
				}
				if frame.Header().Type == http2.FramePing {
					pings++
				}
			}
			if testCase.wantPing {
				if pings == 0 {
					t.Fatal("the positive control saw no PING: the detector cannot see one, so the " +
						"zero in the real case would prove nothing")
				}
				return
			}
			if pings != 0 {
				t.Fatalf("an idle TrustTunnel session sent %d PING frame(s): the transport is built as "+
					"\"no PING: an idle session sends nothing\", and a Go move that re-enables the "+
					"health check on an idle tunnel changes what this camouflage puts on the wire", pings)
			}
			<-clientDone
		})
	}
}

// TestTheSessionPrefersOneConnection is a small guard on the transport's strictness setting, which
// is what keeps a second HTTP/2 connection from being opened when the server's stream limit is
// reached: the pool treats the ClientConn as unusable at the limit, so the session would multiplex
// onto a NEW connection instead of queueing. See newHTTP2Transport.
func TestTheSessionPrefersOneConnection(t *testing.T) {
	transport, err := newHTTP2Transport()
	if err != nil {
		t.Fatal(err)
	}
	if !transport.StrictMaxConcurrentStreams {
		t.Fatal("StrictMaxConcurrentStreams is off: at the server's stream limit the ClientConn reports " +
			"itself unusable and the session opens a second connection instead of queueing the CONNECT")
	}
	if transport.ReadIdleTimeout != 0 {
		t.Fatalf("ReadIdleTimeout = %v, want 0 (an idle session must send nothing)", transport.ReadIdleTimeout)
	}
}
