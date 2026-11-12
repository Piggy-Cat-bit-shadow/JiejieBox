package tuic

// Q09 on the production outbound: a real session creation is admitted once, and a new stream on the
// established connection is not admitted again.
//
// The sing-quic client publishes one QUIC connection and shares the creation in flight; the hook is
// called once per creation. This test holds the only permit and makes both paths observable against
// the real service: a creation must wait while the gate is saturated, a stream on the established
// connection must not.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func newAdmissionGuard(t *testing.T) *resourceguard.Guard {
	t.Helper()
	return resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              1,
		Wait:               5 * time.Second,
		InteractiveReserve: 0,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
}

func newAdmittedTUICOutbound(t *testing.T, port uint16, guard *resourceguard.Guard) *Outbound {
	t.Helper()
	ctx := resourceguard.NewContext(context.Background(), guard)
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("tuic-admission"), "tuic-admission",
		option.TUICOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			UUID:          carrierTestUUID,
			Password:      carrierTestPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "localhost",
					Insecure:   true,
				},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() { _ = outbound.client.CloseWithError(net.ErrClosed) })
	return outbound
}

func TestTUICCreationIsAdmittedAndStreamsAreNot(t *testing.T) {
	guard := newAdmissionGuard(t)
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServer(t, handler)
	outbound := newAdmittedTUICOutbound(t, port, guard)
	base := resourceguard.NewContext(context.Background(), guard)
	destination := M.ParseSocksaddr("echo.test:443")

	// Saturation: the only permit is held, so the creation must wait.
	held, err := guard.Acquire(base, resourceguard.ClassNormal)
	require.NoError(t, err)
	started := time.Now()
	returned := make(chan net.Conn, 1)
	failures := make(chan error, 1)
	go func() {
		conn, dialErr := outbound.DialContext(context.Background(), N.NetworkTCP, destination)
		if dialErr != nil {
			failures <- dialErr
			return
		}
		returned <- conn
	}()
	select {
	case dialErr := <-failures:
		t.Fatalf("a creation returned after %v with the gate saturated (err=%v)", time.Since(started), dialErr)
	case conn := <-returned:
		_ = conn.Close()
		t.Fatalf("a creation returned after %v with the gate saturated", time.Since(started))
	case <-time.After(300 * time.Millisecond):
		// Expected: the creation is waiting for the permit.
	}
	held.Release()
	var created net.Conn
	select {
	case created = <-returned:
		require.NotNil(t, created)
	case dialErr := <-failures:
		require.NoError(t, dialErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the creation never returned after the permit was released")
	}
	require.NoError(t, created.Close())

	// Reuse: the QUIC connection is established, so a new stream must be served while the gate is
	// saturated - it creates nothing.
	held, err = guard.Acquire(base, resourceguard.ClassNormal)
	require.NoError(t, err)
	started = time.Now()
	reused, err := outbound.DialContext(context.Background(), N.NetworkTCP, destination)
	require.NoError(t, err)
	require.NotNil(t, reused)
	require.Less(t, time.Since(started), time.Second,
		"a stream on an established TUIC connection waited for a creation permit")
	require.Equal(t, 1, guard.Stats().Gate.Inflight, "the reuse must not have acquired a second permit")
	held.Release()
	require.NoError(t, reused.Close())
}

// TestTUICAdmissionIsOnlyWiredWithAGuard pins the two constructor states.
func TestTUICAdmissionIsOnlyWiredWithAGuard(t *testing.T) {
	guard := newAdmissionGuard(t)
	require.NotNil(t, admitCreation(guard, "tuic/test"))
	require.Nil(t, admitCreation(nil, "tuic/test"))
}
