package trusttunnel

// P2-3: a slow target must still be able to establish through a healthy carrier.
//
// The CONNECT establishment budget is a bound on the CARRIER, not an assertion that every target
// answers within a few seconds. A real target can take longer than the ten-second figure an earlier
// draft used; cutting it there would turn a compatibility fix into a new compatibility failure. The
// budget is therefore aligned with the stack's existing request-phase bound (constant.TCPTimeout,
// 15 s), and this test drives a real TLS + HTTP/2 TrustTunnel service whose CONNECT answer is
// delayed past ten seconds.
//
// This is a real transport test (real TCP, TLS, HTTP/2, real server handler), not a fake session.

import (
	"context"
	"io"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// slowTargetHandler answers a CONNECT only after the configured delay, then writes a payload. A
// real upstream connect that is merely slow looks like this to the client.
type slowTargetHandler struct {
	delay   time.Duration
	payload []byte
}

func (h *slowTargetHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	go func() {
		defer conn.Close()
		timer := time.NewTimer(h.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		if _, err := conn.Write(h.payload); err != nil {
			return
		}
		// Keep the CONNECT stream open until the client closes it.
		_, _ = io.Copy(io.Discard, conn)
	}()
}

func (h *slowTargetHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata Metadata) {
	conn.Close()
}

func TestSlowTargetBeyondTenSecondsStillEstablishes(t *testing.T) {
	const delay = 10_200 * time.Millisecond
	payload := []byte("slow-target-ok")
	server := startTestService(t, ProtocolHTTP2, &slowTargetHandler{delay: delay, payload: payload}, UDPLimits{})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)

	ctx := testContext(t)
	started := time.Now()
	conn, err := client.DialContext(ctx, M.ParseSocksaddr("slow.test:443"))
	require.NoError(t, err, "a target slower than ten seconds was cut off by the establishment budget")
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, delay-200*time.Millisecond,
		"the CONNECT returned before the server's delayed answer")
	require.Less(t, elapsed, connectEstablishBudget,
		"the establishment budget did not cover the delayed target")

	received := make([]byte, len(payload))
	_, err = io.ReadFull(conn, received)
	require.NoError(t, err)
	require.Equal(t, payload, received)
	require.NoError(t, conn.Close())
}
