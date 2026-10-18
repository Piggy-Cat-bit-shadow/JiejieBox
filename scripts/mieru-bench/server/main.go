package main

// Interop peer for the reverse direction: an official-SDK Mieru server on
// Windows. mita itself ships only for Linux; this program uses the same
// github.com/enfein/mieru/v3/apis/server entry point mita uses, plus the
// minimal SOCKS5 request handling a proxy needs, so the fork's Outbound can be
// tested against the official server implementation.

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	mierucommon "github.com/enfein/mieru/v3/apis/common"
	mieruconstant "github.com/enfein/mieru/v3/apis/constant"
	mierumodel "github.com/enfein/mieru/v3/apis/model"
	mieruserver "github.com/enfein/mieru/v3/apis/server"
	mierutp "github.com/enfein/mieru/v3/apis/trafficpattern"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"
)

func main() {
	port := flag.Int("port", 18665, "listen port")
	user := flag.String("user", "baozi", "user name")
	password := flag.String("password", "manlianpenfen", "password")
	trafficPattern := flag.String("traffic-pattern", "", "base64 traffic pattern")
	flag.Parse()

	var pattern *mierupb.TrafficPattern
	if *trafficPattern != "" {
		decoded, err := mierutp.Decode(*trafficPattern)
		if err != nil {
			fatal("decode traffic pattern", err)
		}
		if err = mierutp.Validate(decoded); err != nil {
			fatal("validate traffic pattern", err)
		}
		pattern = decoded
	}

	server := mieruserver.NewServer()
	config := &mieruserver.ServerConfig{
		Config: &mierupb.ServerConfig{
			ListenIPAddress: proto.String("127.0.0.1"),
			TrafficPattern:  pattern,
			PortBindings: []*mierupb.PortBinding{
				{Port: proto.Int32(int32(*port)), Protocol: mierupb.TransportProtocol_TCP.Enum()},
				{Port: proto.Int32(int32(*port)), Protocol: mierupb.TransportProtocol_UDP.Enum()},
			},
			Users: []*mierupb.User{
				{Name: proto.String(*user), Password: proto.String(*password)},
			},
		},
	}
	if err := server.Store(config); err != nil {
		fatal("store config", err)
	}
	if err := server.Start(); err != nil {
		fatal("start", err)
	}
	fmt.Printf("official SDK server ready on 127.0.0.1:%d\n", *port)
	for {
		conn, request, err := server.Accept()
		if err != nil {
			if !server.IsRunning() {
				return
			}
			fmt.Println("accept:", err)
			continue
		}
		go handle(conn, request)
	}
}

func fatal(what string, err error) {
	fmt.Fprintln(os.Stderr, what+":", err)
	os.Exit(1)
}

func handle(conn net.Conn, request *mierumodel.Request) {
	defer conn.Close()
	response := &mierumodel.Response{
		Reply:    mieruconstant.Socks5ReplySuccess,
		BindAddr: mierumodel.AddrSpec{IP: net.IPv4zero, Port: 0},
	}
	if err := response.WriteToSocks5(conn); err != nil {
		return
	}
	switch request.Command {
	case mieruconstant.Socks5ConnectCmd:
		destination := net.JoinHostPort(addrHost(request.DstAddr), fmt.Sprint(request.DstAddr.Port))
		target, err := net.DialTimeout("tcp", destination, 10*time.Second)
		if err != nil {
			fmt.Println("dial tcp:", err)
			return
		}
		defer target.Close()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(target, conn)
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(conn, target)
		}()
		wg.Wait()
	case mieruconstant.Socks5UDPAssociateCmd:
		relayUDP(conn)
	default:
		fmt.Println("unsupported command", request.Command)
	}
}

func addrHost(addr mierumodel.AddrSpec) string {
	if addr.FQDN != "" {
		return addr.FQDN
	}
	return addr.IP.String()
}

// relayUDP reads the stream framing the official SDK documents and forwards
// each datagram to the address in its SOCKS5 UDP header. The probe uses one
// destination, so replies are written back with the destination it sent to.
func relayUDP(conn net.Conn) {
	tunnel := mierucommon.NewPacketOverStreamTunnel(conn)
	var (
		udpConn      net.PacketConn
		udpConnMutex sync.Mutex
		lastDest     net.Addr
	)
	defer func() {
		if udpConn != nil {
			udpConn.Close()
		}
	}()
	buffer := make([]byte, 65535)
	for {
		n, _, err := tunnel.ReadFrom(buffer)
		if err != nil {
			return
		}
		packet := buffer[:n]
		if len(packet) < 4 {
			continue
		}
		if packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
			continue
		}
		reader := newSliceReader(packet[3:])
		var destination mierumodel.AddrSpec
		if err := destination.ReadFromSocks5(reader); err != nil {
			continue
		}
		payload := packet[3+reader.offset:]
		addr := &net.UDPAddr{IP: destination.IP, Port: destination.Port}
		if addr.IP == nil {
			resolved, err := net.ResolveUDPAddr("udp", net.JoinHostPort(destination.FQDN, fmt.Sprint(destination.Port)))
			if err != nil {
				continue
			}
			addr = resolved
		}
		udpConnMutex.Lock()
		if udpConn == nil {
			udpConn, err = net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				udpConnMutex.Unlock()
				return
			}
			go func(pc net.PacketConn) {
				reply := make([]byte, 65535)
				for {
					rn, source, readErr := pc.ReadFrom(reply)
					if readErr != nil {
						return
					}
					sourceAddr, ok := source.(*net.UDPAddr)
					if !ok {
						continue
					}
					udpConnMutex.Lock()
					currentDest := lastDest
					udpConnMutex.Unlock()
					if currentDest != nil && sourceAddr.String() != currentDest.String() {
						continue
					}
					encoded := encodeUDPHeader(sourceAddr)
					encoded = append(encoded, reply[:rn]...)
					_, _ = tunnel.WriteTo(encoded, nil)
				}
			}(udpConn)
		}
		lastDest = addr
		udpConnMutex.Unlock()
		_, _ = udpConn.WriteTo(payload, addr)
	}
}

type sliceReader struct {
	data   []byte
	offset int
}

func newSliceReader(data []byte) *sliceReader { return &sliceReader{data: data} }

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func encodeUDPHeader(addr *net.UDPAddr) []byte {
	out := []byte{0, 0, 0, 0x01}
	out = append(out, addr.IP.To4()...)
	out = binary.BigEndian.AppendUint16(out, uint16(addr.Port))
	return out
}
