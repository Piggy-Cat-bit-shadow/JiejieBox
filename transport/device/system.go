package device

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"sync"
	"syscall"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/kernelports"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

const systemDeviceReadBufferSize = 65535 + tun.PacketOffset

type systemDevice struct {
	baseDevice
	stateAccess  sync.RWMutex
	options      Options
	dialer       N.Dialer
	device       tun.Tun
	ports        *kernelports.Pool
	inet4Address netip.Addr
	inet6Address netip.Addr
	closed       bool
}

func newSystemDevice(options Options) (*systemDevice, error) {
	if options.Name == "" {
		options.Name = tun.CalculateInterfaceName(options.NamePrefix)
	}
	interfaceDialer, err := dialer.NewDefault(options.Context, option.DialerOptions{
		AbstractDialerOptions: option.AbstractDialerOptions{
			BindInterface: options.Name,
		},
	})
	if err != nil {
		return nil, err
	}
	inet4Address, inet6Address := firstAddresses(options.Configuration.Address)
	return &systemDevice{
		options:      options,
		dialer:       interfaceDialer,
		inet4Address: inet4Address,
		inet6Address: inet6Address,
	}, nil
}

func (d *systemDevice) Start() error {
	d.stateAccess.Lock()
	defer d.stateAccess.Unlock()
	err := d.startLocked()
	if err != nil {
		return err
	}
	ports, err := kernelports.New()
	if err != nil {
		d.options.Logger.Warn(E.Cause(err, "reserve selector ports"))
	}
	d.ports = ports
	return nil
}

func (d *systemDevice) startLocked() error {
	if d.closed {
		return net.ErrClosed
	}
	if d.device != nil {
		return nil
	}
	tunOptions := d.buildTunOptions()
	tunInterface, err := tun.New(tunOptions)
	if err != nil {
		return err
	}
	err = tunInterface.Start()
	if err != nil {
		tunInterface.Close()
		return err
	}
	d.device = tunInterface
	d.options.Logger.Info("started at ", d.options.Name)
	go d.readLoop(tunInterface, int(d.options.MTU))
	return nil
}

func (d *systemDevice) buildTunOptions() tun.Options {
	inet4Address, inet6Address := firstAddresses(d.options.Configuration.Address)
	d.inet4Address = inet4Address
	d.inet6Address = inet6Address
	inet4Addresses, inet6Addresses := splitPrefixes(d.options.Configuration.Address)
	networkManager := service.FromContext[adapter.NetworkManager](d.options.Context)
	tunOptions := tun.Options{
		Name:                 d.options.Name,
		Inet4Address:         inet4Addresses,
		Inet6Address:         inet6Addresses,
		MTU:                  d.options.MTU,
		GSO:                  true,
		InterfaceScope:       true,
		DNSMode:              tun.DNSModeDisabled,
		InterfaceMonitor:     nil,
		InterfaceFinder:      nil,
		Logger:               d.options.Logger,
		IPRoute2TableIndex:   tun.DefaultIPRoute2TableIndex,
		IPRoute2RuleIndex:    tun.DefaultIPRoute2RuleIndex,
		EXP_DisableDNSHijack: true,
	}
	if networkManager != nil {
		tunOptions.InterfaceMonitor = networkManager.InterfaceMonitor()
		tunOptions.InterfaceFinder = networkManager.InterfaceFinder()
	}
	return tunOptions
}

func (d *systemDevice) readLoop(tunInterface tun.Tun, mtu int) {
	linuxTUN, isLinuxTUN := tunInterface.(tun.LinuxTUN)
	if isLinuxTUN && linuxTUN.BatchSize() > 1 {
		d.readLoopLinux(linuxTUN, linuxTUN.BatchSize(), mtu)
		return
	}
	darwinTUN, isDarwinTUN := tunInterface.(tun.DarwinTUN)
	if isDarwinTUN {
		d.readLoopDarwin(darwinTUN)
		return
	}
	frontHeadroom := d.options.PacketFrontHeadroom
	rearHeadroom := d.options.PacketRearHeadroom
	readBuffer := make([]byte, systemDeviceReadBufferSize)
	var readRetry tun.ReadRetry
	for {
		readN, err := tunInterface.Read(readBuffer)
		if err != nil {
			// A transient read error must not end the datapath (LX 4537a1ac), and a persistent one
			// must not spin (LX 3f0958bf). See readErrorIsFatal.
			if d.readErrorIsFatal(err) {
				return
			}
			readRetry.Wait(err)
			continue
		}
		readRetry.Reset()
		if readN <= tun.PacketOffset {
			continue
		}
		packet := readBuffer[tun.PacketOffset:readN]
		if d.blockIPv6Enabled() && header.IPVersion(packet) == header.IPv6Version {
			continue
		}
		packetBuffer := buf.NewSize(frontHeadroom + len(packet) + rearHeadroom)
		packetBuffer.Resize(frontHeadroom, 0)
		common.Must1(packetBuffer.Write(packet))
		err = d.writeOutbound([]*buf.Buffer{packetBuffer})
		if err != nil {
			d.options.Logger.Debug(E.Cause(err, "write packet"))
		}
	}
}

func (d *systemDevice) readLoopLinux(tunInterface tun.LinuxTUN, batchSize int, mtu int) {
	frontHeadroom := d.options.PacketFrontHeadroom
	packetSize := frontHeadroom + mtu + d.options.PacketRearHeadroom
	packetBuffers := make([]*buf.Buffer, batchSize)
	readBuffers := make([][]byte, batchSize)
	packetSizes := make([]int, batchSize)
	outboundBuffers := make([]*buf.Buffer, 0, batchSize)
	for i := range packetBuffers {
		packetBuffers[i] = buf.NewSize(packetSize)
	}
	defer buf.ReleaseMulti(packetBuffers)
	var readRetry tun.ReadRetry
	for {
		for i, packetBuffer := range packetBuffers {
			packetBuffer.Reset()
			packetBuffer.Resize(frontHeadroom, 0)
			readBuffers[i] = packetBuffer.FreeBytes()[:mtu]
		}
		packetCount, readErr := tunInterface.BatchRead(readBuffers, 0, packetSizes)
		blockIPv6 := d.blockIPv6Enabled()
		for i := range packetCount {
			packetBuffers[i].Truncate(packetSizes[i])
			if blockIPv6 && header.IPVersion(packetBuffers[i].Bytes()) == header.IPv6Version {
				continue
			}
			outboundBuffers = append(outboundBuffers, packetBuffers[i])
			packetBuffers[i] = buf.NewSize(packetSize)
		}
		if len(outboundBuffers) > 0 {
			writeErr := d.writeOutbound(outboundBuffers)
			if writeErr != nil {
				d.options.Logger.Debug(E.Cause(writeErr, "write packet batch"))
			}
			clear(outboundBuffers)
			outboundBuffers = outboundBuffers[:0]
		}
		if readErr != nil {
			// The write half above deliberately swallows its error and keeps looping
			// (LX 4537a1ac); the read half must not do the opposite and end the loop on an error
			// that is merely a queue condition. See readErrorIsFatal.
			if d.readErrorIsFatal(readErr) {
				return
			}
			readRetry.Wait(readErr)
			continue
		}
		readRetry.Reset()
	}
}

func (d *systemDevice) readLoopDarwin(tunInterface tun.DarwinTUN) {
	frontHeadroom := d.options.PacketFrontHeadroom
	rearHeadroom := d.options.PacketRearHeadroom
	var readRetry tun.ReadRetry
	for {
		packetBuffers, readErr := tunInterface.BatchRead(frontHeadroom, rearHeadroom)
		outboundBuffers := packetBuffers[:0]
		blockIPv6 := d.blockIPv6Enabled()
		for _, packetBuffer := range packetBuffers {
			if packetBuffer.IsEmpty() {
				packetBuffer.Release()
				continue
			}
			if blockIPv6 && header.IPVersion(packetBuffer.Bytes()) == header.IPv6Version {
				packetBuffer.Release()
				continue
			}
			outboundBuffers = append(outboundBuffers, packetBuffer)
		}
		if len(outboundBuffers) > 0 {
			writeErr := d.writeOutbound(outboundBuffers)
			if writeErr != nil {
				d.options.Logger.Debug(E.Cause(writeErr, "write packet batch"))
			}
		}
		if readErr != nil {
			if d.readErrorIsFatal(readErr) {
				return
			}
			readRetry.Wait(readErr)
			continue
		}
		readRetry.Reset()
	}
}

// readErrorIsFatal reports whether a TUN read error ends the device's read loop for good.
//
// # The two failures this splits apart
//
// LX 4537a1ac and LX 3f0958bf are the two halves of one question, and a read loop can only be
// wrong in one direction or the other:
//
//   - returning on ANY error turns a momentary queue condition into a permanent black hole. The
//     process stays up, the tunnel still reports connected, and nothing restarts this goroutine -
//     `Start` is called once per device lifetime - so every later packet is dropped until the
//     configuration is reloaded;
//   - `continue`ing on EVERY error turns a permanent one into a spin: error, continue, error,
//     continue, at 100% of a core, with a log line per iteration.
//
// The recoverability test is the pinned dependency's own (`tun.IsRecoverableReadError`), which is
// the contract the TUN implementations are written against: `ENOBUFS` and `ENOMEM` are what
// Darwin's `recvmsg_x` and Linux's batch reads report when the queue is full, and they are exactly
// the errors that should be retried with backoff rather than treated as teardown.
//
// # Why this is not a callsite-only patch
//
// The same predicate and the same backoff are already applied by `protocol/bridge/backend_linux.go`,
// which reads the same TUN through the same `BatchRead`. This file was the one place that did not
// use them, so the two datapaths disagreed about what an `ENOBUFS` means.
//
// A fatal error is still logged at Error, and a recoverable one at Debug with the retry's own
// escalating delay, so a persistently full queue is visible without becoming a log storm.
//
// # Why the recoverability test is not tun.IsRecoverableReadError ALONE
//
// That helper compares POSIX constants, and on Windows none of them ever matches the value that
// arrives - Go spells errno with the application-defined bit set, so syscall.ENOBUFS is 0x20000046
// while the Winsock error for the same condition is 10055, and the Windows TUN hands over the raw
// value (sing-tun/tun_windows.go:447 `fmt.Errorf("read failed: %w", errno)`). Classifying on the
// POSIX set alone therefore made this function answer "fatal" for exactly the transient conditions
// it exists to tolerate, ending the datapath on a buffer-exhausted queue - the LX 4537a1ac class
// this predicate was added to fix, still live on one platform. See windows_read_errors.go for the
// measurements and readErrorIsRecoverable for the platform-neutral test.
func (d *systemDevice) readErrorIsFatal(err error) bool {
	if E.IsClosed(err) || E.IsMulti(err, syscall.EBADF) {
		return true
	}
	if readErrorIsRecoverable(err) {
		d.options.Logger.Debug(E.Cause(err, "read packet"))
		return false
	}
	d.options.Logger.Error(E.Cause(err, "read packet"))
	return true
}

func (d *systemDevice) UpdateConfiguration(configuration Configuration) error {
	d.stateAccess.Lock()
	defer d.stateAccess.Unlock()
	previousConfiguration := d.options.Configuration
	previousMTU := d.options.MTU
	updatedMTU := d.options.MTU
	if configuration.MTU != 0 {
		updatedMTU = configuration.MTU
	}
	d.options.MTU = updatedMTU
	d.options.Configuration = configuration
	if d.device == nil {
		inet4Address, inet6Address := firstAddresses(configuration.Address)
		d.inet4Address = inet4Address
		d.inet6Address = inet6Address
		return nil
	}
	if !slices.Equal(previousConfiguration.Address, configuration.Address) ||
		previousMTU != updatedMTU {
		d.device.Close()
		d.device = nil
		return d.startLocked()
	}
	return nil
}

func (d *systemDevice) blockIPv6Enabled() bool {
	d.stateAccess.RLock()
	defer d.stateAccess.RUnlock()
	return d.options.Configuration.BlockIPv6
}

func (d *systemDevice) FrontHeadroom() int {
	d.stateAccess.RLock()
	tunInterface := d.device
	d.stateAccess.RUnlock()
	linuxTUN, isLinuxTUN := tunInterface.(tun.LinuxTUN)
	if !isLinuxTUN {
		return d.baseDevice.FrontHeadroom()
	}
	return max(d.baseDevice.FrontHeadroom(), linuxTUN.FrontHeadroom())
}

func (d *systemDevice) WriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	return d.processInboundBuffers(packetBuffers, d.writeBuffers)
}

func (d *systemDevice) writeBuffers(packetBuffers []*buf.Buffer) error {
	d.stateAccess.RLock()
	tunInterface := d.device
	d.stateAccess.RUnlock()
	if tunInterface == nil {
		return E.New("system device is not ready")
	}
	linuxTUN, isLinuxTUN := tunInterface.(tun.LinuxTUN)
	if isLinuxTUN {
		headroom := linuxTUN.FrontHeadroom()
		packets := make([][]byte, len(packetBuffers))
		var temporaryBuffers []*buf.Buffer
		for i, packetBuffer := range packetBuffers {
			if packetBuffer.Start() >= headroom {
				packetBuffer.ExtendHeader(headroom)
				packets[i] = packetBuffer.Bytes()
				packetBuffer.Advance(headroom)
				continue
			}
			temporaryBuffer := buf.NewSize(headroom + packetBuffer.Len())
			temporaryBuffer.Resize(headroom, 0)
			_, _ = temporaryBuffer.Write(packetBuffer.Bytes())
			temporaryBuffer.ExtendHeader(headroom)
			packets[i] = temporaryBuffer.Bytes()
			temporaryBuffers = append(temporaryBuffers, temporaryBuffer)
		}
		_, err := linuxTUN.BatchWrite(packets, headroom)
		buf.ReleaseMulti(temporaryBuffers)
		return err
	}
	darwinTUN, isDarwinTUN := tunInterface.(tun.DarwinTUN)
	if isDarwinTUN {
		return darwinTUN.BatchWrite(packetBuffers)
	}
	for _, packetBuffer := range packetBuffers {
		err := d.writePacket(packetBuffer.Bytes())
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *systemDevice) writePacket(packet []byte) error {
	d.stateAccess.RLock()
	tunInterface := d.device
	d.stateAccess.RUnlock()
	if tunInterface == nil {
		return E.New("system device is not ready")
	}
	if tun.PacketOffset == 0 {
		_, err := tunInterface.Write(packet)
		return err
	}
	writeBuffer := make([]byte, tun.PacketOffset+len(packet))
	tun.PacketFillHeader(writeBuffer[:tun.PacketOffset], header.IPVersion(packet))
	copy(writeBuffer[tun.PacketOffset:], packet)
	_, err := tunInterface.Write(writeBuffer)
	return err
}

func (d *systemDevice) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.New("invalid non-IP destination")
	}
	return d.dialer.DialContext(ctx, network, destination)
}

func (d *systemDevice) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.New("invalid non-IP destination")
	}
	return d.dialer.ListenPacket(ctx, destination)
}

func (d *systemDevice) PortAddresses() (netip.Addr, netip.Addr) {
	d.stateAccess.RLock()
	defer d.stateAccess.RUnlock()
	return d.inet4Address, d.inet6Address
}

func (d *systemDevice) PortMTU() uint32 {
	d.stateAccess.RLock()
	defer d.stateAccess.RUnlock()
	return d.options.MTU
}

func (d *systemDevice) UpstreamPort() any {
	d.stateAccess.RLock()
	defer d.stateAccess.RUnlock()
	if d.ports == nil {
		return nil
	}
	return d.ports
}

func (d *systemDevice) Close() error {
	d.stateAccess.Lock()
	defer d.stateAccess.Unlock()
	d.closed = true
	var err error
	if d.device != nil {
		err = d.device.Close()
		d.device = nil
	}
	if d.ports != nil {
		err = E.Errors(err, d.ports.Close())
		d.ports = nil
	}
	return err
}

func (d *systemDevice) configurationAddresses() []netip.Prefix {
	d.stateAccess.RLock()
	defer d.stateAccess.RUnlock()
	return slices.Clone(d.options.Configuration.Address)
}
