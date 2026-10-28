package adapter

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
)

// hostFlowNetworkManager answers HostPort and nothing else. The embedded nil interface supplies
// the rest of NetworkManager; a test that reaches any other method panics, which is the point.
type hostFlowNetworkManager struct {
	NetworkManager
	port tun.RoutedPort
}

func (m hostFlowNetworkManager) HostPort() tun.RoutedPort {
	return m.port
}

// hostFlowPort is a routed port: the tun stack as the dispatcher sees it.
type hostFlowPort struct {
	inet4 netip.Addr
	inet6 netip.Addr
	mtu   uint32
}

func (p hostFlowPort) RoutedPort() {}

func (p hostFlowPort) PortAddresses() (netip.Addr, netip.Addr) {
	return p.inet4, p.inet6
}

func (p hostFlowPort) PortMTU() uint32 {
	return p.mtu
}

func (p hostFlowPort) AttachReturn(tun.Return) error { return nil }

func (p hostFlowPort) DetachReturn(tun.Return) error { return nil }

func (p hostFlowPort) WritePackets([][]byte) error { return nil }

func TestJudgeHostFlow(t *testing.T) {
	logger := log.NewNOPFactory().Logger()
	inet4 := netip.MustParseAddr("172.19.0.1")
	inet6 := netip.MustParseAddr("fdfe:dcba:9876::1")
	port := hostFlowPort{inet4: inet4, inet6: inet6, mtu: 1500}
	source := netip.MustParseAddrPort("172.19.0.2:12345")

	t.Run("no routed port keeps the previous behaviour", func(t *testing.T) {
		manager := hostFlowNetworkManager{}
		verdict := JudgeHostFlow(context.Background(), manager, logger, 6, source, netip.MustParseAddrPort("172.19.0.1:53"))
		if verdict.Action != tun.ActionAccept {
			t.Fatalf("expected ActionAccept without a routed port, got %v", verdict.Action)
		}
	})

	t.Run("ipv4 local destination goes to the routed port", func(t *testing.T) {
		manager := hostFlowNetworkManager{port: port}
		destination := netip.MustParseAddrPort("172.19.0.1:53")
		verdict := JudgeHostFlow(context.Background(), manager, logger, 6, source, destination)
		if verdict.Action != tun.ActionFlow {
			t.Fatalf("expected ActionFlow, got %v", verdict.Action)
		}
		if verdict.Port == nil {
			t.Fatal("expected the routed port in the verdict")
		}
		if verdict.Destination.Addr() != inet4 || verdict.Destination.Port() != destination.Port() {
			t.Fatalf("unexpected host destination %v", verdict.Destination)
		}
	})

	t.Run("ipv6 local destination uses the stack's v6 address", func(t *testing.T) {
		manager := hostFlowNetworkManager{port: port}
		destination := netip.MustParseAddrPort("[fdfe:dcba:9876::1]:53")
		verdict := JudgeHostFlow(context.Background(), manager, logger, 6, source, destination)
		if verdict.Action != tun.ActionFlow {
			t.Fatalf("expected ActionFlow, got %v", verdict.Action)
		}
		if verdict.Destination.Addr() != inet6 {
			t.Fatalf("unexpected host destination %v", verdict.Destination)
		}
	})

	t.Run("a stack without the family falls back", func(t *testing.T) {
		manager := hostFlowNetworkManager{port: hostFlowPort{inet4: inet4}}
		destination := netip.MustParseAddrPort("[fdfe:dcba:9876::1]:53")
		verdict := JudgeHostFlow(context.Background(), manager, logger, 6, source, destination)
		if verdict.Action != tun.ActionAccept {
			t.Fatalf("expected ActionAccept without a v6 address, got %v", verdict.Action)
		}
	})

	t.Run("an unknown protocol falls back", func(t *testing.T) {
		manager := hostFlowNetworkManager{port: port}
		verdict := JudgeHostFlow(context.Background(), manager, logger, 99, source, netip.MustParseAddrPort("172.19.0.1:53"))
		if verdict.Action != tun.ActionAccept {
			t.Fatalf("expected ActionAccept for an unknown protocol, got %v", verdict.Action)
		}
	})
}
