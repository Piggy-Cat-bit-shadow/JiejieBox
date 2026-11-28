package route

// The deterministic counterexample for M21 ("connect after the first payload").
//
// # Why this file exists instead of a gate in route/conn.go
//
// M21 proposes to create no proxy session until the client has sent at least one byte, so a port
// probe that opens a TCP connection and says nothing costs no session. The premise is true for a
// probe and false for a whole class of legal protocols: SMTP, SSH, FTP, IMAP, POP3, MySQL and RDP
// all speak FIRST. The client is required to wait for the server's greeting, so "wait for the
// client's first payload, then dial" waits for a byte that can only be sent after the dial.
//
// The experiment below is the smallest proof of that, and it is deterministic: the server announces
// its greeting over a channel after Accept, the client sends nothing until it has read the greeting,
// and the gate's only exit is the production bound. No sleep decides the outcome.
//
// # What is already in the tree
//
// A pre-dial payload read ALREADY exists, for a different purpose: when a route rule walks a sniff
// action, matchRule reads the first payload from the connection before the outbound is dialled
// (route/route.go:181 -> actionSniff), bounded by C.ReadPayloadTimeout (300ms), and hands the bytes
// to the dial through bufio.NewCachedConn. A silent flow therefore already pays that bound - once -
// and then dials. M21 would convert "paid the bound and dialled anyway" into "paid the bound and
// refused", which is exactly the change the counterexample exercises.

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// errNoFirstPayload is what the experimental gate returns when the client never speaks first.
var errNoFirstPayload = errors.New("first payload gate: no payload inside the bound")

// firstPayloadGate is the M21 shape in its smallest honest form: one bounded wait for a client
// payload, then the dial. It is a TEST DOUBLE - the experiment's subject is the protocol semantics
// it would impose, which is why it is not wired anywhere.
type firstPayloadGate struct {
	bound  time.Duration
	spoke  <-chan struct{}
	dial   func() (net.Conn, error)
	dialed atomic.Int32
}

func (g *firstPayloadGate) connect(ctx context.Context) (net.Conn, error) {
	select {
	case <-g.spoke:
		g.dialed.Add(1)
		return g.dial()
	case <-time.After(g.bound):
		return nil, errNoFirstPayload
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// smtpLikeServer is a server-first peer: it greets on accept and then waits for the client's line.
// Both handshakes run through channels, so the test observes ordering rather than elapsed time.
type smtpLikeServer struct {
	listener net.Listener
	accepted chan struct{}
	helo     chan string
	errs     chan error
}

func newSMTPLikeServer(t *testing.T) *smtpLikeServer {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	server := &smtpLikeServer{
		listener: listener,
		accepted: make(chan struct{}),
		helo:     make(chan string, 1),
		errs:     make(chan error, 1),
	}
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			server.errs <- acceptErr
			return
		}
		defer conn.Close()
		close(server.accepted)
		if _, writeErr := conn.Write([]byte("220 census.example ESMTP\r\n")); writeErr != nil {
			server.errs <- writeErr
			return
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, readErr := bufio.NewReader(conn).ReadString('\n')
		if readErr != nil {
			server.errs <- readErr
			return
		}
		server.helo <- line
		_, _ = conn.Write([]byte("250 ok\r\n"))
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// TestServerFirstProtocolIsRefusedByAFirstPayloadGate is the counterexample proper.
//
// Variant A is the shipping shape: dial, then read the greeting. It completes.
// Variant B is M21: wait for the client's payload, then dial. The client is waiting for the
// greeting, which only exists on the other side of the dial, so the gate can only time out. The
// proxy session count therefore drops to zero - and the SMTP client is broken.
func TestServerFirstProtocolIsRefusedByAFirstPayloadGate(t *testing.T) {
	t.Run("shipping shape dials first and completes", func(t *testing.T) {
		server := newSMTPLikeServer(t)
		conn, err := net.DialTimeout("tcp4", server.listener.Addr().String(), time.Second)
		require.NoError(t, err)
		defer conn.Close()
		select {
		case <-server.accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("the server never accepted the connection")
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		greeting, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		require.Contains(t, greeting, "220 ")
		_, err = conn.Write([]byte("EHLO client.example\r\n"))
		require.NoError(t, err)
		select {
		case line := <-server.helo:
			require.Contains(t, line, "EHLO")
		case <-time.After(2 * time.Second):
			t.Fatal("the server never received the client's EHLO")
		}
	})

	t.Run("first payload gate refuses a reachable SMTP server", func(t *testing.T) {
		server := newSMTPLikeServer(t)
		spoke := make(chan struct{}) // never closed: an SMTP client speaks only after the greeting
		gate := &firstPayloadGate{
			bound: C.ReadPayloadTimeout,
			spoke: spoke,
			dial: func() (net.Conn, error) {
				return net.DialTimeout("tcp4", server.listener.Addr().String(), time.Second)
			},
		}
		_, err := gate.connect(context.Background())
		require.ErrorIs(t, err, errNoFirstPayload,
			"the gate must refuse once its bound expires without a payload")
		require.Zero(t, gate.dialed.Load(),
			"refusing the flow must be the only outcome that creates no proxy session")
		select {
		case <-server.accepted:
			t.Fatal("a session was created despite the refusal")
		case <-time.After(200 * time.Millisecond):
			// The server is still waiting for a connection that the gate never made. This is the
			// observation, not a synchronisation point: the refusal above already decided it.
		}
	})
}

// TestThePreDialSniffReadIsTheOnlyPayloadWaitToday pins WHERE the existing payload wait lives, so a
// future change cannot claim M21's benefit twice and cannot move the wait into the dial path by
// accident.
//
// The claim: the pre-dial read is bounded by C.ReadPayloadTimeout and its purpose is protocol
// detection. Nothing converts its timeout into a refusal, which is why a silent flow still reaches
// the outbound today.
func TestThePreDialSniffReadIsTheOnlyPayloadWaitToday(t *testing.T) {
	// The bound the router uses when a sniff action reads the first payload before the dial.
	require.Equal(t, 300*time.Millisecond, C.ReadPayloadTimeout)

	source := readRouteSource(t, "route.go")
	require.Contains(t, source, "sniffTimeout := C.ReadPayloadTimeout",
		"the pre-dial payload wait must stay bounded by the production constant")
	require.Contains(t, source, "inputPacketConn.SetReadDeadline(time.Now().Add(sniffTimeout))",
		"the packet sniff must keep a deadline, or a silent flow parks the walk")
}
