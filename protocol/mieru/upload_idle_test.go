package mieru

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// discardRouter accepts the inbound session and hands it to the test without
// writing application data. It is the server half of a one-way upload: the
// client keeps sending while the server deliberately stays silent.
type discardRouter struct {
	adapter.Router
	connection chan net.Conn
}

func (r *discardRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.connection <- conn
}

// TestMieruClientReadSurvivesLongUnidirectionalUpload pins the SDK defect fixed
// by v3.39.0 (enfein/mieru#321).
//
// Before v3.39.0 every client Session.Write armed a 10 s read deadline. An
// application read that started after a write captured that deadline once and
// returned ErrTimeout ten seconds later, even though the client was still
// uploading successfully and the connection was healthy. Any flow that sends
// for longer than ten seconds without receiving application data — a large
// upload, an interactive session whose peer is quiet — was torn down at the
// ten-second mark.
//
// The test arms the former deadline with a write, starts a read that waits for
// a reply, uploads for longer than the old deadline, and only then lets the
// server answer. v3.39.0 returns the reply; v3.38.1 fails the read (and usually
// the later writes) at ten seconds.
func TestMieruClientReadSurvivesLongUnidirectionalUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("the regression window is the SDK's former 10 s client read deadline")
	}
	const (
		// The defect fired ten seconds after the write that armed the
		// deadline; the upload must outlast that window.
		uploadWindow = 11 * time.Second
		// The old deadline is armed by this write, so the read below starts
		// with a timer in v3.38.1.
		armDeadline = "arm"
		reply       = "still-here"
	)

	port := freePortForBothTransports(t)
	router := &discardRouter{connection: make(chan net.Conn, 1)}
	created, err := NewInbound(context.Background(), router, nopLogger(), "mieru-in", inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	inboundInstance, ok := created.(*Inbound)
	require.True(t, ok)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })

	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))
	dialCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	clientConn, err := outboundInstance.DialContext(dialCtx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.60:443"))
	require.NoError(t, err)
	defer clientConn.Close()

	var serverConn net.Conn
	select {
	case serverConn = <-router.connection:
	case <-time.After(10 * time.Second):
		t.Fatal("the inbound session was never routed")
	}
	defer serverConn.Close()

	_, err = clientConn.Write([]byte(armDeadline))
	require.NoError(t, err)
	require.Equal(t, []byte(armDeadline), readExactWithTimeout(t, serverConn, len(armDeadline)))

	// This read waits for application data from the server. In v3.38.1 it
	// carries the 10 s deadline armed by the write above.
	readResult := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		buffer := make([]byte, len(reply))
		n, readErr := clientConn.Read(buffer)
		readResult <- struct {
			data []byte
			err  error
		}{data: append([]byte(nil), buffer[:n]...), err: readErr}
	}()

	var writeErr error
	uploadUntil := time.Now().Add(uploadWindow)
	payload := []byte("u")
	for time.Now().Before(uploadUntil) {
		if _, writeErr = clientConn.Write(payload); writeErr != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, writeErr, "the upload must keep working past the SDK's former 10 s read deadline")

	_, err = serverConn.Write([]byte(reply))
	require.NoError(t, err)

	select {
	case result := <-readResult:
		require.NoError(t, result.err, "the pending read must survive a long unidirectional upload")
		require.Equal(t, []byte(reply), result.data)
	case <-time.After(5 * time.Second):
		t.Fatal("the pending read did not receive the reply")
	}
}
