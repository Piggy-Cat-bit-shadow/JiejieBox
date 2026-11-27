package daemon

// Does an active relay's copy-growth permit survive a Box reload?
//
// A reload in this daemon is not a re-start of the same object: `ApplyServiceChange` closes
// the running Box (`oldInstance.Close()`) and then builds a NEW Box, which builds a new
// route.ConnectionManager, which installs a new resourceguard.GrowthBudget in Start. This
// test drives that real path with a live CONNECT tunnel and reads both budgets (the old
// one through a saved manager reference) so the question is answered at the object level:
//
//   - the old generation's permits return to zero once its connections are closed;
//   - the new generation starts at zero and counts independently;
//   - nothing shares the old budget object.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxprovider "github.com/sagernet/sing-box/adapter/provider"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	protocoldirect "github.com/sagernet/sing-box/protocol/direct"
	protocolhttp "github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// growthReloadContext is recoveryBusinessContext's registry set without t.TempDir: on
// Windows the service's cache.db keeps the directory busy past Close, and the automatic
// RemoveAll would fail the test after it already passed. The per-test directory is still
// removed, just without turning a held handle into a failure.
func growthReloadContext(t *testing.T) context.Context {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	protocolhttp.RegisterInbound(inboundRegistry)
	outboundRegistry := outbound.NewRegistry()
	protocoldirect.RegisterOutbound(outboundRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inboundRegistry,
		outboundRegistry,
		boxprovider.NewRegistry(),
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	workingPath, err := os.MkdirTemp("", "sing-box-growth-reload-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(workingPath) })
	return filemanager.WithDefault(ctx, workingPath, workingPath, -1, -1)
}

const growthReloadConfig = `{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`

// growthBudgetOf reads the manager's installed budget. There is no exported accessor by
// design, so the test uses a read-only reflection lens, exactly as the w8 measurement
// harness does; a rename fails loudly instead of measuring nothing.
func growthBudgetOf(t *testing.T, manager adapter.ConnectionManager) (*resourceguard.GrowthBudget, uintptr) {
	t.Helper()
	routeManager, isRouteManager := manager.(*route.ConnectionManager)
	require.True(t, isRouteManager, "the Box installed a non-route connection manager")
	field := reflect.ValueOf(routeManager).Elem().FieldByName("growth")
	require.True(t, field.IsValid(), "ConnectionManager has no growth field; the lens must be updated")
	require.Equal(t, reflect.Ptr, field.Kind())
	require.False(t, field.IsNil(), "the growth budget is not installed; manager.Start did not run")
	return (*resourceguard.GrowthBudget)(unsafe.Pointer(field.Pointer())), field.Pointer()
}

// growthRelayEchoTarget accepts any number of connections and echoes each stream.
func growthRelayEchoTarget(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// connectThroughProxy opens one CONNECT tunnel through the Box's HTTP inbound.
func connectThroughProxy(t *testing.T, proxyPort int, target string) (io.Reader, io.Writer, net.Conn) {
	t.Helper()
	proxyConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	require.NoError(t, err)
	_, err = fmt.Fprintf(proxyConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)
	reader := bufio.NewReader(proxyConn)
	response, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, "HTTP inbound did not establish the CONNECT tunnel")
	return reader, proxyConn, proxyConn
}

// pushThroughTunnel writes size bytes and reads them back, so BOTH copy directions cross
// their growth threshold and hold a permit.
func pushThroughTunnel(t *testing.T, reader io.Reader, writer io.Writer, size int) {
	t.Helper()
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte(index)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write(payload)
		writeErr <- err
	}()
	echoed := make([]byte, size)
	_, err := io.ReadFull(reader, echoed)
	require.NoError(t, err)
	require.NoError(t, <-writeErr)
	require.Equal(t, payload[0], echoed[0])
}

// TestGrowthBudgetAcrossARealBoxReload is Q1's construction: an active relay, a real
// StartOrReloadService reload, and both generations' budgets read.
func TestGrowthBudgetAcrossARealBoxReload(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: growthReloadContext(t)})
	proxyPort := freeLoopbackPort(t)
	profile := fmt.Sprintf(growthReloadConfig, proxyPort)
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	t.Cleanup(func() { service.Close() })

	target := growthRelayEchoTarget(t)
	readA, writeA, tunnelA := connectThroughProxy(t, proxyPort, target)
	pushThroughTunnel(t, readA, writeA, 2<<20)

	oldInstance := service.Instance()
	require.NotNil(t, oldInstance)
	oldManager := oldInstance.connectionManager
	oldBudget, oldPointer := growthBudgetOf(t, oldManager)
	require.Eventually(t, func() bool { return oldBudget.Stats().InUse == 2*8 }, 5*time.Second, time.Millisecond,
		"the live relay's two directions must hold %d units while it is open; stats: %+v", 2*8, oldBudget.Stats())

	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))

	newInstance := service.Instance()
	require.NotNil(t, newInstance)
	newManager := newInstance.connectionManager
	newBudget, newPointer := growthBudgetOf(t, newManager)
	require.NotEqual(t, oldManager, newManager, "a reload must replace the connection manager")
	require.NotEqual(t, oldPointer, newPointer, "the replacement Box must not reuse the old growth budget")
	require.Zero(t, newBudget.Stats().InUse, "a new generation must start with no inherited permits")

	require.Eventually(t, func() bool { return oldBudget.Stats().InUse == 0 }, 10*time.Second, time.Millisecond,
		"the old generation's permits did not return after its connections were closed; stats: %+v", oldBudget.Stats())

	_ = tunnelA.Close()
	readB, writeB, tunnelB := connectThroughProxy(t, proxyPort, target)
	pushThroughTunnel(t, readB, writeB, 2<<20)
	require.Eventually(t, func() bool { return newBudget.Stats().InUse == 2*8 }, 5*time.Second, time.Millisecond,
		"the new generation must count its own grown directions; stats: %+v", newBudget.Stats())
	require.Zero(t, oldBudget.Stats().InUse, "the closed generation must stay at zero while the new one grows")
	_ = tunnelB.Close()
}
