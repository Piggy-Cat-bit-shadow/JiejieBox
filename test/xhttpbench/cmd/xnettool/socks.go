package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// A minimal SOCKS5 client (RFC 1928, no authentication).
//
// Written by hand instead of pulled in as a dependency because the load
// generator must build and behave identically before and after the transport
// under test is optimized: a dependency change in the middle of the project
// would move the measuring stick.

const (
	socksVersion    = 0x05
	socksCmdConnect = 0x01
	socksAtypIPv4   = 0x01
	socksAtypDomain = 0x03
	socksAtypIPv6   = 0x04
)

// dialThroughSOCKS opens target through the SOCKS5 proxy at proxyAddress.
func dialThroughSOCKS(proxyAddress string, target string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxyAddress, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyAddress, err)
	}
	if err = socksHandshake(conn, target, timeout); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func socksHandshake(conn net.Conn, target string, timeout time.Duration) error {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	// Greeting: version, one method, "no authentication".
	if _, err := conn.Write([]byte{socksVersion, 0x01, 0x00}); err != nil {
		return fmt.Errorf("write greeting: %w", err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fmt.Errorf("read method selection: %w", err)
	}
	if reply[0] != socksVersion {
		return fmt.Errorf("proxy answered SOCKS version %d", reply[0])
	}
	if reply[1] != 0x00 {
		return fmt.Errorf("proxy requires authentication method %d", reply[1])
	}

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("split target %s: %w", target, err)
	}
	port, err := parsePort(portText)
	if err != nil {
		return err
	}

	request := make([]byte, 0, 262)
	request = append(request, socksVersion, socksCmdConnect, 0x00)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			request = append(request, socksAtypIPv4)
			request = append(request, v4...)
		} else {
			request = append(request, socksAtypIPv6)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("target host name too long")
		}
		request = append(request, socksAtypDomain, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err = conn.Write(request); err != nil {
		return fmt.Errorf("write connect request: %w", err)
	}

	var header [4]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil {
		return fmt.Errorf("read connect reply: %w", err)
	}
	if header[0] != socksVersion {
		return fmt.Errorf("proxy answered SOCKS version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("proxy refused the connect request, reply code %d", header[1])
	}
	switch header[3] {
	case socksAtypIPv4:
		_, err = io.ReadFull(conn, make([]byte, 4+2))
	case socksAtypIPv6:
		_, err = io.ReadFull(conn, make([]byte, 16+2))
	case socksAtypDomain:
		var length [1]byte
		if _, err = io.ReadFull(conn, length[:]); err != nil {
			return fmt.Errorf("read bound address length: %w", err)
		}
		_, err = io.ReadFull(conn, make([]byte, int(length[0])+2))
	default:
		return fmt.Errorf("proxy answered unknown address type %d", header[3])
	}
	if err != nil {
		return fmt.Errorf("read bound address: %w", err)
	}
	return conn.SetDeadline(time.Time{})
}

func parsePort(text string) (int, error) {
	port := 0
	if text == "" {
		return 0, fmt.Errorf("empty port")
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("invalid port %q", text)
		}
		port = port*10 + int(text[i]-'0')
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %q out of range", text)
	}
	return port, nil
}
