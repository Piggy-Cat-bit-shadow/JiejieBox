package listener

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/redir"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/database64128/tfo-go/v2"
)

func (l *Listener) ListenTCP() (net.Listener, error) {
	tcpListener, err := ListenTCPWithOptions(l.ctx, l.listenOptions, l.listenOptions.ListenPort, l.tproxy)
	if err != nil {
		return nil, err
	}
	l.logger.Info("tcp server started at ", tcpListener.Addr())
	l.tcpListener = tcpListener
	return tcpListener, nil
}

// ListenTCPWithOptions opens a TCP listener with the socket semantics of the
// given ListenOptions, on an explicit port.
//
// It is the same code path as (*Listener).ListenTCP, exposed so an inbound
// whose sockets are created by a protocol SDK (Mieru's listener factories)
// applies ListenOptions for real instead of accepting the configuration and
// silently ignoring it.
//
// The address comes from listenOptions.Listen (the configuration's listen
// field), not from a caller-supplied address: a factory receives the SDK's
// resolved endpoint, whose IP is derived from the same field a moment earlier,
// and honouring one of them here would be a second source of truth.
func ListenTCPWithOptions(ctx context.Context, listenOptions option.ListenOptions, port uint16, tproxy bool) (net.Listener, error) {
	//nolint:staticcheck
	if listenOptions.ProxyProtocol || listenOptions.ProxyProtocolAcceptNoHeader {
		return nil, E.New("Proxy Protocol is deprecated and removed in sing-box 1.6.0")
	}
	var err error
	bindAddr := M.SocksaddrFrom(listenOptions.Listen.Build(netip.AddrFrom4([4]byte{127, 0, 0, 1})), port)
	var listenConfig net.ListenConfig
	if listenOptions.BindInterface != "" {
		listenConfig.Control = control.Append(listenConfig.Control, control.BindToInterface(service.FromContext[adapter.NetworkManager](ctx).InterfaceFinder(), listenOptions.BindInterface, -1))
	}
	if listenOptions.RoutingMark != 0 {
		listenConfig.Control = control.Append(listenConfig.Control, control.RoutingMark(uint32(listenOptions.RoutingMark)))
	}
	if listenOptions.ReuseAddr {
		listenConfig.Control = control.Append(listenConfig.Control, control.ReuseAddr())
	}
	if listenOptions.DisableTCPKeepAlive {
		listenConfig.KeepAlive = -1
		listenConfig.KeepAliveConfig.Enable = false
	} else {
		keepIdle := time.Duration(listenOptions.TCPKeepAlive)
		if keepIdle == 0 {
			keepIdle = C.TCPKeepAliveInitial
		}
		keepInterval := time.Duration(listenOptions.TCPKeepAliveInterval)
		if keepInterval == 0 {
			keepInterval = C.TCPKeepAliveInterval
		}
		listenConfig.KeepAliveConfig = net.KeepAliveConfig{
			Enable:   true,
			Idle:     keepIdle,
			Interval: keepInterval,
		}
	}
	if listenOptions.TCPMultiPath {
		listenConfig.SetMultipathTCP(true)
	} else {
		// Go 1.24+ enables MPTCP for listeners on Linux by default. The kernel
		// MPTCP listener is the attack surface Xray disabled in v26.10.31
		// (remote root on a vulnerable Linux kernel), so "unset" must mean
		// plain TCP here; tcp_multi_path remains the explicit opt-in above.
		listenConfig.SetMultipathTCP(false)
	}
	if tproxy {
		listenConfig.Control = control.Append(listenConfig.Control, func(network, address string, conn syscall.RawConn) error {
			return control.Raw(conn, func(fd uintptr) error {
				return redir.TProxy(fd, !strings.HasSuffix(network, "4"), false)
			})
		})
	}
	tcpListener, err := ListenNetworkNamespace[net.Listener](ctx, listenOptions.NetNs, func() (net.Listener, error) {
		if listenOptions.TCPFastOpen {
			var tfoConfig tfo.ListenConfig
			tfoConfig.ListenConfig = listenConfig
			return tfoConfig.Listen(ctx, NetworkWithAddressFamily(N.NetworkTCP, bindAddr.Addr), bindAddr.String())
		} else {
			return listenConfig.Listen(ctx, NetworkWithAddressFamily(N.NetworkTCP, bindAddr.Addr), bindAddr.String())
		}
	})
	if err != nil {
		return nil, err
	}
	return tcpListener, nil
}

func (l *Listener) loopTCPIn() {
	tcpListener := l.tcpListener
	var (
		metadata    adapter.InboundContext
		acceptDelay time.Duration
	)
	for {
		conn, err := tcpListener.Accept()
		if err != nil {
			if l.shutdown.Load() && E.IsClosed(err) {
				return
			}
			//nolint:staticcheck
			if netError, isNetError := err.(net.Error); isNetError && netError.Temporary() {
				if acceptDelay == 0 {
					acceptDelay = 5 * time.Millisecond
				} else {
					acceptDelay = min(2*acceptDelay, time.Second)
				}
				l.logger.Error(err, ", retrying in ", acceptDelay)
				time.Sleep(acceptDelay)
				continue
			}
			l.tcpListener.Close()
			l.logger.Error("tcp listener closed: ", err)
			return
		}
		acceptDelay = 0
		//nolint:staticcheck
		metadata.InboundDetour = l.listenOptions.Detour
		metadata.Source = M.SocksaddrFromNet(conn.RemoteAddr()).Unwrap()
		metadata.OriginDestination = M.SocksaddrFromNet(conn.LocalAddr()).Unwrap()
		ctx := log.ContextWithNewID(l.ctx)
		l.logger.InfoContext(ctx, "inbound connection from ", metadata.Source)
		go l.connHandler.NewConnection(ctx, conn, metadata, nil)
	}
}
