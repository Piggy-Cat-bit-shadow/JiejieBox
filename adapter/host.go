package adapter

import (
	"context"
	"net/netip"
	"sync/atomic"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common/byteformats"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// JudgeHostFlow decides a flow whose destination is an endpoint's own local address.
//
// Such a flow is not for the far side of the endpoint: it is for the address the endpoint answers
// on inside the tunnel. Returning ActionAccept leaves it to whatever the platform does with a
// packet addressed to an address the kernel does not own; when a routed port is registered, the
// honest answer is to send it to that port, which is the local stack itself.
//
// Without a routed port the function falls back to ActionAccept, so a build or a profile that
// never registers one keeps the previous behaviour.
func JudgeHostFlow(ctx context.Context, networkManager NetworkManager, logger log.ContextLogger, network uint8, source netip.AddrPort, destination netip.AddrPort) tun.FlowVerdict {
	if networkManager == nil {
		return tun.FlowVerdict{Action: tun.ActionAccept}
	}
	port := networkManager.HostPort()
	if port == nil {
		return tun.FlowVerdict{Action: tun.ActionAccept}
	}
	inet4Address, inet6Address := port.PortAddresses()
	hostAddress := inet4Address
	if destination.Addr().Is6() {
		hostAddress = inet6Address
	}
	networkName := flowNetworkName(network)
	if !hostAddress.IsValid() || networkName == "" {
		return tun.FlowVerdict{Action: tun.ActionAccept}
	}
	hostDestination := netip.AddrPortFrom(hostAddress, destination.Port())
	return tun.FlowVerdict{
		Action:      tun.ActionFlow,
		Port:        port,
		Destination: hostDestination,
		NewTracker: func() tun.FlowTracker {
			flowContext := log.ContextWithNewID(ctx)
			sourceAddress := M.SocksaddrFromNetIP(source)
			destinationAddress := M.SocksaddrFromNetIP(hostDestination)
			if networkName == N.NetworkICMP {
				sourceAddress.Port = 0
				destinationAddress.Port = 0
			}
			logger.InfoContext(flowContext, "forward ", networkName, " connection from ", sourceAddress, " to ", destinationAddress, " via tun")
			return NewHostFlowTracker(flowContext, logger)
		},
	}
}

func flowNetworkName(network uint8) string {
	switch network {
	case uint8(header.TCPProtocolNumber):
		return N.NetworkTCP
	case uint8(header.UDPProtocolNumber):
		return N.NetworkUDP
	case uint8(header.ICMPv4ProtocolNumber), uint8(header.ICMPv6ProtocolNumber):
		return N.NetworkICMP
	default:
		return ""
	}
}

var _ tun.FlowTracker = (*HostFlowTracker)(nil)

// HostFlowTracker logs one routed host flow: who it was for, and how much it moved.
//
// The dispatcher owns the flow table, so the tracker carries no state of its own beyond the
// counters; CloseFlow is the only place the numbers are read.
type HostFlowTracker struct {
	ctx      context.Context
	logger   log.ContextLogger
	upload   atomic.Int64
	download atomic.Int64
}

func NewHostFlowTracker(ctx context.Context, logger log.ContextLogger) *HostFlowTracker {
	return &HostFlowTracker{ctx: ctx, logger: logger}
}

func (l *HostFlowTracker) AttachFlow(handle tun.FlowHandle) {
}

func (l *HostFlowTracker) CountForward(n int) {
	l.upload.Add(int64(n))
}

func (l *HostFlowTracker) CountReverse(n int) {
	l.download.Add(int64(n))
}

func (l *HostFlowTracker) FlowEstablished() {
}

func (l *HostFlowTracker) CloseFlow(reason tun.FlowCloseReason) {
	l.logger.DebugContext(l.ctx, "flow closed: ", reason,
		", upload ", byteformats.FormatBytes(uint64(l.upload.Load())), ", download ", byteformats.FormatBytes(uint64(l.download.Load())))
}
