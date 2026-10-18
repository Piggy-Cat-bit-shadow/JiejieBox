package mieru

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	mierumodel "github.com/enfein/mieru/v3/apis/model"
)

// Mieru carries UDP packets inside a stream using the framing the official
// SDK documents in apis/common/packet_over_stream.go:
//
//	0x00 | uint16 big-endian payload length | SOCKS5 UDP header + payload | 0xff
//
// where the inner packet is a SOCKS5 UDP request (RSV RSV FRAG ATYP ...). The
// implementation here is a direct codec rather than a wrapper around the
// SDK's PacketOverStreamTunnel + UDPAssociateWrapper because those allocate a
// new buffer THREE times per datagram (header, packet, framed copy). This one
// writes the whole frame once into one pooled buffer and reads directly into
// the caller's buffer. The wire format is pinned by a conformance test against
// the SDK implementation.
const (
	uotFramePrefix = byte(0x00)
	uotFrameSuffix = byte(0xff)
	// The length field is 16 bits and covers the SOCKS5 UDP header plus the
	// payload. The SDKS's own encoder rejects larger packets for the same
	// reason.
	uotMaxDataLength = 0xffff
)

// socks5UDPHeaderLength is the 2 reserved bytes + 1 fragment byte.
const socks5UDPHeaderLength = 3

// writeUOTFrame writes one framed datagram to w.
//
// Destination must be a literal IP or an FQDN; the address is encoded exactly
// as apis/model.AddrSpec.WriteToSocks5 encodes it.
func writeUOTFrame(w io.Writer, payload []byte, destination M.Socksaddr) error {
	var address [1 + 255 + 2]byte
	addressLength, err := encodeSocks5Address(address[:], destination)
	if err != nil {
		return err
	}
	dataLength := socks5UDPHeaderLength + addressLength + len(payload)
	if dataLength > uotMaxDataLength {
		return fmt.Errorf("mieru: UDP datagram of %d bytes exceeds the %d-byte frame limit", dataLength, uotMaxDataLength)
	}
	frame := buf.NewSize(4 + dataLength)
	defer frame.Release()
	frameData := frame.Extend(4 + dataLength)
	frameData[0] = uotFramePrefix
	binary.BigEndian.PutUint16(frameData[1:3], uint16(dataLength))
	// RSV, RSV, FRAG. Fragmentation is not implemented by Mieru.
	frameData[3] = 0
	frameData[4] = 0
	frameData[5] = 0
	copy(frameData[6:], address[:addressLength])
	copy(frameData[6+addressLength:], payload)
	frameData[len(frameData)-1] = uotFrameSuffix
	return writeAll(w, frameData)
}

// uotFramer decodes the Mieru UDP-over-stream framing from one reader.
//
// The scratch arrays are fields rather than locals on purpose: passing a local
// array to io.ReadFull through the io.Reader interface makes it escape, and a
// per-datagram allocation on the receive path is exactly the cost this codec
// exists to avoid. One framer lives per association; the fields are bounded
// (255-byte FQDN maximum) and reused for every packet.
type uotFramer struct {
	reader      io.Reader
	frameHeader [3]byte
	udpHeader   [socks5UDPHeaderLength]byte
	addressType [1]byte
	suffix      [1]byte
	ipv4        [6]byte
	ipv6        [18]byte
	fqdn        [255 + 2]byte
}

func newUOTFramer(reader io.Reader) *uotFramer {
	return &uotFramer{reader: reader}
}

func (f *uotFramer) readFrame(p []byte) (int, M.Socksaddr, error) {
	if _, err := io.ReadFull(f.reader, f.frameHeader[:]); err != nil {
		return 0, M.Socksaddr{}, err
	}
	if f.frameHeader[0] != uotFramePrefix {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: UDP frame prefix 0x%02x is not 0x00", f.frameHeader[0])
	}
	dataLength := int(binary.BigEndian.Uint16(f.frameHeader[1:]))
	if dataLength < socks5UDPHeaderLength {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: UDP frame of %d bytes is too short for the SOCKS5 header", dataLength)
	}
	if _, err := io.ReadFull(f.reader, f.udpHeader[:]); err != nil {
		return 0, M.Socksaddr{}, err
	}
	if f.udpHeader[0] != 0 || f.udpHeader[1] != 0 {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: invalid SOCKS5 UDP reserved bytes %02x %02x", f.udpHeader[0], f.udpHeader[1])
	}
	if f.udpHeader[2] != 0 {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: SOCKS5 UDP fragmentation is not supported (frag=0x%02x)", f.udpHeader[2])
	}
	destination, addressLength, err := f.readAddress()
	if err != nil {
		return 0, M.Socksaddr{}, err
	}
	payloadLength := dataLength - socks5UDPHeaderLength - addressLength
	if payloadLength < 0 {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: UDP frame length %d is shorter than its addresses", dataLength)
	}
	if payloadLength > len(p) {
		if _, err = io.CopyN(io.Discard, f.reader, int64(payloadLength)+1); err != nil {
			return 0, M.Socksaddr{}, err
		}
		return 0, destination, io.ErrShortBuffer
	}
	if _, err = io.ReadFull(f.reader, p[:payloadLength]); err != nil {
		return 0, M.Socksaddr{}, err
	}
	if _, err = io.ReadFull(f.reader, f.suffix[:]); err != nil {
		return 0, M.Socksaddr{}, err
	}
	if f.suffix[0] != uotFrameSuffix {
		return 0, M.Socksaddr{}, fmt.Errorf("mieru: UDP frame suffix 0x%02x is not 0xff", f.suffix[0])
	}
	return payloadLength, destination, nil
}

func (f *uotFramer) readAddress() (M.Socksaddr, int, error) {
	if _, err := io.ReadFull(f.reader, f.addressType[:]); err != nil {
		return M.Socksaddr{}, 0, err
	}
	switch f.addressType[0] {
	case 0x01:
		if _, err := io.ReadFull(f.reader, f.ipv4[:]); err != nil {
			return M.Socksaddr{}, 0, err
		}
		address := netip.AddrFrom4([4]byte(f.ipv4[:4]))
		return M.SocksaddrFrom(address, binary.BigEndian.Uint16(f.ipv4[4:])), 1 + len(f.ipv4), nil
	case 0x04:
		if _, err := io.ReadFull(f.reader, f.ipv6[:]); err != nil {
			return M.Socksaddr{}, 0, err
		}
		address := netip.AddrFrom16([16]byte(f.ipv6[:16]))
		return M.SocksaddrFrom(address, binary.BigEndian.Uint16(f.ipv6[16:])), 1 + len(f.ipv6), nil
	case 0x03:
		if _, err := io.ReadFull(f.reader, f.addressType[:]); err != nil {
			return M.Socksaddr{}, 0, err
		}
		if f.addressType[0] == 0 {
			return M.Socksaddr{}, 0, fmt.Errorf("mieru: empty FQDN in SOCKS5 UDP header")
		}
		fqdnLength := int(f.addressType[0])
		if _, err := io.ReadFull(f.reader, f.fqdn[:fqdnLength+2]); err != nil {
			return M.Socksaddr{}, 0, err
		}
		return M.Socksaddr{
			Fqdn: string(f.fqdn[:fqdnLength]),
			Port: binary.BigEndian.Uint16(f.fqdn[fqdnLength:]),
		}, 1 + 1 + fqdnLength + 2, nil
	default:
		return M.Socksaddr{}, 0, fmt.Errorf("mieru: unrecognized SOCKS5 address type 0x%02x", f.addressType[0])
	}
}

// readUOTFrame is the convenience form used by tests. Production connections
// keep a uotFramer and call readFrame, so the scratch arrays are reused.
func readUOTFrame(r io.Reader, p []byte) (int, M.Socksaddr, error) {
	return newUOTFramer(r).readFrame(p)
}

// readSocks5Address is the convenience form used by tests.
func readSocks5Address(r io.Reader) (M.Socksaddr, int, error) {
	return newUOTFramer(r).readAddress()
}

// encodeSocks5Address writes the SOCKS5 address (ATYP + address + port) for a
// Socksaddr and returns its encoded length.
func encodeSocks5Address(dst []byte, destination M.Socksaddr) (int, error) {
	var addressLength int
	switch {
	case destination.IsFqdn():
		if len(destination.Fqdn) > 255 {
			return 0, fmt.Errorf("mieru: FQDN %q exceeds 255 bytes", destination.Fqdn)
		}
		dst[0] = 0x03
		dst[1] = byte(len(destination.Fqdn))
		copy(dst[2:], destination.Fqdn)
		addressLength = 2 + len(destination.Fqdn)
	case destination.IsIP():
		address := destination.Addr.Unmap()
		if address.Is4() {
			dst[0] = 0x01
			copy(dst[1:5], address.AsSlice())
			addressLength = 5
		} else if address.Is6() {
			dst[0] = 0x04
			copy(dst[1:17], address.AsSlice())
			addressLength = 17
		} else {
			return 0, fmt.Errorf("mieru: unsupported destination address %s", destination.Addr)
		}
	default:
		return 0, fmt.Errorf("mieru: destination %q has neither an IP nor an FQDN", destination.String())
	}
	binary.BigEndian.PutUint16(dst[addressLength:], destination.Port)
	return addressLength + 2, nil
}

// writeAll writes p completely, reporting a zero-progress write instead of
// spinning.
func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// toNetAddrSpec converts a destination to the SDK's net.Addr implementation
// with the network the caller wants. It is the inverse of the inbound
// destination conversion and deliberately avoids the string round-trip in
// NetAddrSpec.From.
func toNetAddrSpec(destination M.Socksaddr, network string) (mierumodel.NetAddrSpec, error) {
	spec := mierumodel.NetAddrSpec{Net: network}
	switch {
	case destination.IsFqdn():
		spec.FQDN = destination.Fqdn
	case destination.IsIP():
		spec.IP = net.IP(destination.Addr.Unmap().AsSlice())
	default:
		return mierumodel.NetAddrSpec{}, fmt.Errorf("mieru: destination %q has neither an IP nor an FQDN", destination.String())
	}
	spec.Port = int(destination.Port)
	return spec, nil
}

// fromNetAddrSpec converts the destination the server received in the SOCKS5
// request to sing-box metadata.
func fromNetAddrSpec(spec mierumodel.NetAddrSpec) (M.Socksaddr, error) {
	if spec.Port < 0 || spec.Port > 65535 {
		return M.Socksaddr{}, fmt.Errorf("mieru: destination port %d is out of range", spec.Port)
	}
	if spec.FQDN != "" {
		return M.Socksaddr{Fqdn: spec.FQDN, Port: uint16(spec.Port)}, nil
	}
	if spec.IP != nil {
		address, ok := netip.AddrFromSlice(spec.IP)
		if !ok {
			return M.Socksaddr{}, fmt.Errorf("mieru: destination IP %v is not a valid address", spec.IP)
		}
		return M.SocksaddrFrom(address.Unmap(), uint16(spec.Port)), nil
	}
	return M.Socksaddr{}, fmt.Errorf("mieru: destination has neither an IP nor an FQDN")
}
