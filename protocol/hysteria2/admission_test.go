package hysteria2

// Q09 on the production Hysteria2 outbound: the shared QUIC creation is admitted, streams on the
// established connection are not.

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

func newHysteriaAdmissionGuard() *resourceguard.Guard {
	return resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              1,
		Wait:               5 * time.Second,
		InteractiveReserve: 0,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
}

func newAdmittedHysteriaOutbound(t *testing.T, port uint16, guard *resourceguard.Guard) *Outbound {
	t.Helper()
	ctx := resourceguard.NewContext(context.Background(), guard)
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("hysteria2-admission"), "hysteria2-admission",
		option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Password:      hysteriaCarrierPassword,
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
	t.Cleanup(func() { outbound.client.CloseWithError(net.ErrClosed) })
	return outbound
}

func TestHysteria2CreationIsAdmittedAndStreamsAreNot(t *testing.T) {
	guard := newHysteriaAdmissionGuard()
	handler := &hysteriaCarrierHandler{target: startHysteriaCarrierEcho(t)}
	port := startHysteriaCarrierServer(t, handler)
	outbound := newAdmittedHysteriaOutbound(t, port, guard)
	base := resourceguard.NewContext(context.Background(), guard)
	destination := M.ParseSocksaddr("echo.test:443")

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

	held, err = guard.Acquire(base, resourceguard.ClassNormal)
	require.NoError(t, err)
	started = time.Now()
	reused, err := outbound.DialContext(context.Background(), N.NetworkTCP, destination)
	require.NoError(t, err)
	require.NotNil(t, reused)
	require.Less(t, time.Since(started), time.Second,
		"a stream on an established Hysteria2 connection waited for a creation permit")
	require.Equal(t, 1, guard.Stats().Gate.Inflight)
	held.Release()
	require.NoError(t, reused.Close())
}

func TestHysteria2AdmissionIsOnlyWiredWithAGuard(t *testing.T) {
	guard := newHysteriaAdmissionGuard()
	require.NotNil(t, admitCreation(guard, "hysteria2/test"))
	require.Nil(t, admitCreation(nil, "hysteria2/test"))
}
