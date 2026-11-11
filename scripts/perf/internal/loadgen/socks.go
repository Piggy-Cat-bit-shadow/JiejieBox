package loadgen

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// dialSOCKS5 opens a TCP stream to target through a SOCKS5 proxy.
func dialSOCKS5(proxyAddr, targetAddr string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}
	if err = conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	// Greeting: no authentication.
	if _, err = conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, err
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting rejected: %v", reply)
	}
	request, err := connectRequest(targetAddr)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if _, err = conn.Write(request); err != nil {
		conn.Close()
		return nil, err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil {
		conn.Close()
		return nil, err
	}
	if header[0] != 0x05 || header[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: code %d", header[1])
	}
	switch header[3] {
	case 0x01:
		_, err = io.CopyN(io.Discard, conn, 4+2)
	case 0x03:
		length := make([]byte, 1)
		if _, err = io.ReadFull(conn, length); err == nil {
			_, err = io.CopyN(io.Discard, conn, int64(length[0])+2)
		}
	case 0x04:
		_, err = io.CopyN(io.Discard, conn, 16+2)
	default:
		err = fmt.Errorf("socks5 unknown address type %d", header[3])
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	// The deadline was only for the handshake; the caller owns the stream now.
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func connectRequest(targetAddr string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("target host name too long")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	return request, nil
}

// udpAssociation is a SOCKS5 UDP ASSOCIATE session: one control TCP connection
// that keeps the association alive, plus the relay address datagrams go to.
type udpAssociation struct {
	control net.Conn
	relay   *net.UDPAddr
	socket  *net.UDPConn
}

func associateUDP(proxyAddr string, timeout time.Duration) (*udpAssociation, error) {
	control, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	if _, err = control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		control.Close()
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(control, reply); err != nil {
		control.Close()
		return nil, err
	}
	if reply[1] != 0x00 {
		control.Close()
		return nil, fmt.Errorf("socks5 udp greeting rejected: %v", reply)
	}
	// Request an association for the wildcard address; the relay decides.
	request := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err = control.Write(request); err != nil {
		control.Close()
		return nil, err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(control, header); err != nil {
		control.Close()
		return nil, err
	}
	if header[1] != 0x00 {
		control.Close()
		return nil, fmt.Errorf("socks5 udp associate failed: code %d", header[1])
	}
	var relayHost string
	switch header[3] {
	case 0x01:
		buffer := make([]byte, 4)
		if _, err = io.ReadFull(control, buffer); err != nil {
			control.Close()
			return nil, err
		}
		relayHost = net.IP(buffer).String()
	case 0x04:
		buffer := make([]byte, 16)
		if _, err = io.ReadFull(control, buffer); err != nil {
			control.Close()
			return nil, err
		}
		relayHost = net.IP(buffer).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err = io.ReadFull(control, length); err != nil {
			control.Close()
			return nil, err
		}
		buffer := make([]byte, int(length[0]))
		if _, err = io.ReadFull(control, buffer); err != nil {
			control.Close()
			return nil, err
		}
		relayHost = string(buffer)
	default:
		control.Close()
		return nil, fmt.Errorf("socks5 unknown relay address type %d", header[3])
	}
	portBuffer := make([]byte, 2)
	if _, err = io.ReadFull(control, portBuffer); err != nil {
		control.Close()
		return nil, err
	}
	// A relay that answers with the wildcard address means "same host as the
	// control connection".
	proxyHost, _, _ := net.SplitHostPort(proxyAddr)
	if relayHost == "0.0.0.0" || relayHost == "::" {
		relayHost = proxyHost
	}
	relay := &net.UDPAddr{IP: net.ParseIP(relayHost), Port: int(binary.BigEndian.Uint16(portBuffer))}
	socket, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		control.Close()
		return nil, err
	}
	return &udpAssociation{control: control, relay: relay, socket: socket}, nil
}

func (a *udpAssociation) Close() error {
	if a.socket != nil {
		_ = a.socket.Close()
	}
	if a.control != nil {
		return a.control.Close()
	}
	return nil
}

// udpPacket builds the SOCKS5 UDP request header for one datagram.
func udpPacket(targetAddr string, payload []byte) ([]byte, error) {
	host, portText, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 0, len(payload)+22)
	packet = append(packet, 0x00, 0x00, 0x00) // RSV, RSV, FRAG
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			packet = append(packet, 0x01)
			packet = append(packet, ip4...)
		} else {
			packet = append(packet, 0x04)
			packet = append(packet, ip.To16()...)
		}
	} else {
		packet = append(packet, 0x03, byte(len(host)))
		packet = append(packet, host...)
	}
	packet = binary.BigEndian.AppendUint16(packet, uint16(port))
	packet = append(packet, payload...)
	return packet, nil
}
