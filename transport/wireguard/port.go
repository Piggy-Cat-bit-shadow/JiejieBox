package wireguard

import (
	"net/netip"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/wireguard-go/device"
	wgTun "github.com/sagernet/wireguard-go/tun"
)

func (e *Endpoint) PortAddresses() (netip.Addr, netip.Addr) {
	return e.tunDevice.Inet4Address(), e.tunDevice.Inet6Address()
}

func (e *Endpoint) PortMTU() uint32 {
	return e.options.MTU
}

func (e *Endpoint) UpstreamPort() any {
	return e.tunDevice
}

func (e *Endpoint) WritePackets(packets [][]byte) error {
	wgDevice, release, err := e.acquireDevice()
	if err != nil {
		return err
	}
	if wgDevice == nil {
		return E.New("WireGuard device is not ready")
	}
	defer release()
	// Preserved from the pre-lazy path: an inbound packet is demand, so a suspended (but still
	// resident) device is brought up before the packet is injected. A RELEASED chain never reaches
	// here - the eligible shape has no inbound demand - and fails closed above instead of rebuilding
	// the heavy chain from a packet path.
	e.resume()
	packetRefs := make([]*device.InputPacketRef, 0, len(packets))
	refs := make([]device.InputPacketRef, len(packets))
	packetSlices := make([][]byte, len(packets))
	for i, packet := range packets {
		if len(packet) == 0 {
			continue
		}
		var destination []byte
		switch header.IPVersion(packet) {
		case header.IPv4Version:
			if len(packet) < header.IPv4MinimumSize {
				continue
			}
			destination = header.IPv4(packet).DestinationAddressSlice()
		case header.IPv6Version:
			if len(packet) < header.IPv6MinimumSize {
				continue
			}
			destination = header.IPv6(packet).DestinationAddressSlice()
		default:
			continue
		}
		packetSlices[i] = packet
		refs[i] = device.InputPacketRef{
			Destination:  destination,
			PacketSlices: packetSlices[i : i+1],
		}
		packetRefs = append(packetRefs, &refs[i])
	}
	if len(packetRefs) == 0 {
		return nil
	}
	unmatchedRefs := wgDevice.InputPackets(packetRefs)
	if len(unmatchedRefs) == 0 {
		return nil
	}
	state := e.returnPath.Load()
	if state == nil {
		return nil
	}
	var replies [][]byte
	for _, packetRef := range unmatchedRefs {
		packet := packetRef.PacketSlices[0]
		var source netip.Addr
		if header.IPVersion(packet) == header.IPv4Version {
			source = e.tunDevice.Inet4Address()
		} else {
			source = e.tunDevice.Inet6Address()
		}
		reply, replyOk := tun.BuildICMPError(packet, tun.ICMPErrorNoRoute, source, 0, state.headroom)
		if replyOk {
			replies = append(replies, reply)
		}
	}
	if len(replies) > 0 {
		state.returnPath.ReturnPackets(replies)
	}
	return nil
}

func (e *Endpoint) AttachReturn(returnPath tun.Return) error {
	headroom := returnPath.ReturnHeadroom()
	if headroom > device.MessageTransportOffsetContent {
		return E.New("return path headroom ", headroom, " exceeds available ", device.MessageTransportOffsetContent)
	}
	newState := &returnPathState{
		returnPath: returnPath,
		headroom:   headroom,
	}
	for {
		currentState := e.returnPath.Load()
		if currentState != nil {
			if currentState.returnPath == returnPath {
				return nil
			}
			return E.New("return path already attached")
		}
		if e.returnPath.CompareAndSwap(nil, newState) {
			return nil
		}
	}
}

func (e *Endpoint) DetachReturn(returnPath tun.Return) error {
	currentState := e.returnPath.Load()
	if currentState != nil && currentState.returnPath == returnPath {
		e.returnPath.CompareAndSwap(currentState, nil)
	}
	return nil
}

type returnPathState struct {
	returnPath tun.Return
	headroom   int
}

// returnDeviceWrapper is the wgTun.Device one wireguard-go Device is built with. It is created per
// build, not per endpoint: the previous shape kept a single wrapper for the endpoint's lifetime,
// which would let a stale Device reach whatever attachment a later rebuild installed.
//
// Write is the half that belongs to the endpoint rather than to one Device: a return path attached
// to the endpoint must receive decrypted packets whichever Device decrypted them.
type returnDeviceWrapper struct {
	wgTun.Device
	endpoint *Endpoint
}

func (d *returnDeviceWrapper) Write(bufs [][]byte, offset int) (int, error) {
	state := d.endpoint.returnPath.Load()
	if state == nil || len(bufs) == 0 {
		return d.Device.Write(bufs, offset)
	}
	packets := make([][]byte, len(bufs))
	for i, packet := range bufs {
		// wireguard-go leaves device.MessageTransportOffsetContent writable bytes in front of the decrypted packet.
		packets[i] = packet[offset-state.headroom:]
	}
	unconsumed := state.returnPath.ReturnPackets(packets)
	if len(unconsumed) == 0 {
		return 0, nil
	}
	if len(unconsumed) == len(bufs) {
		return d.Device.Write(bufs, offset)
	}
	remaining := make([][]byte, 0, len(unconsumed))
	searchIndex := 0
	for _, packet := range unconsumed {
		for searchIndex < len(bufs) && &packet[0] != &bufs[searchIndex][offset-state.headroom] {
			searchIndex++
		}
		if searchIndex == len(bufs) {
			break
		}
		remaining = append(remaining, bufs[searchIndex])
		searchIndex++
	}
	return d.Device.Write(remaining, offset)
}
