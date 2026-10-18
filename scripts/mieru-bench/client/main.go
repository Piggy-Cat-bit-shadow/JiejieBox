// Command mieru-bench-client is the measuring end of the Mieru benchmark
// stand. It starts its own TCP and UDP echo targets, then drives them through a
// SOCKS5 proxy with a fixed workload and prints one JSON result.
//
// It deliberately reports application payload bytes, not wire bytes: the
// comparison is between two proxy clients over the same loopback path, so the
// payload is the common denominator. The orchestrator records process CPU,
// RSS and the variant configuration next to this JSON.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

const (
	modeDownload = 'D'
	modeUpload   = 'U'
	modeEcho     = 'E'
)

type result struct {
	Label          string    `json:"label"`
	Mode           string    `json:"mode"`
	Connections    int       `json:"connections"`
	PayloadBytes   int64     `json:"payload_bytes"`
	WallSeconds    float64   `json:"wall_seconds"`
	ThroughputMiB  float64   `json:"throughput_mib_s"`
	Packets        int       `json:"packets,omitempty"`
	PacketsPerSec  float64   `json:"packets_per_sec,omitempty"`
	Loss           int       `json:"loss,omitempty"`
	P50Ms          float64   `json:"p50_ms,omitempty"`
	P95Ms          float64   `json:"p95_ms,omitempty"`
	P99Ms          float64   `json:"p99_ms,omitempty"`
	Samples        []float64 `json:"samples_ms,omitempty"`
	Success        int       `json:"success"`
	Failures       int       `json:"failures"`
	Errors         []string  `json:"errors,omitempty"`
	ConnectSamples []float64 `json:"connect_samples_ms,omitempty"`
}

func main() {
	socksAddr := flag.String("socks", "127.0.0.1:11082", "SOCKS5 proxy address")
	label := flag.String("label", "unlabeled", "variant label")
	mode := flag.String("mode", "tcp-download", "tcp-download, tcp-upload, tcp-latency, udp-echo, first-byte")
	connections := flag.Int("connections", 1, "parallel connections or UDP associations")
	payloadBytes := flag.Int64("bytes", 32<<20, "bytes per TCP connection")
	requests := flag.Int("requests", 500, "request/response iterations for latency or first-byte")
	packets := flag.Int("packets", 2000, "datagrams per UDP association")
	packetSize := flag.Int("size", 1400, "payload size per request or datagram")
	window := flag.Int("window", 64, "UDP datagrams in flight")
	warmup := flag.Int("warmup", 3, "iterations excluded from latency statistics")
	timeout := flag.Duration("timeout", 120*time.Second, "overall operation timeout")
	flag.Parse()

	tcpTarget, udpTarget := startTargets()
	bench := &benchmark{
		socks:     *socksAddr,
		label:     *label,
		mode:      *mode,
		conns:     *connections,
		bytes:     *payloadBytes,
		requests:  *requests,
		packets:   *packets,
		size:      *packetSize,
		window:    *window,
		warmup:    *warmup,
		timeout:   *timeout,
		tcpTarget: tcpTarget,
		udpTarget: udpTarget,
	}
	out, err := bench.run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

// startTargets creates the destination side on loopback. The same targets are
// used for every variant, so target cost cancels out of the comparison.
func startTargets() (*net.TCPAddr, *net.UDPAddr) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			conn, acceptErr := tcpListener.Accept()
			if acceptErr != nil {
				return
			}
			go serveTCP(conn)
		}
	}()
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, addr, readErr := udpConn.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			_, _ = udpConn.WriteTo(buffer[:n], addr)
		}
	}()
	return tcpListener.Addr().(*net.TCPAddr), udpConn.LocalAddr().(*net.UDPAddr)
}

// serveTCP reads the 9-byte workload header and then performs the requested
// direction. A header keeps the target stateless across downloads, uploads and
// echo/latency requests.
func serveTCP(conn net.Conn) {
	defer conn.Close()
	var header [9]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return
	}
	mode := header[0]
	length := int64(binary.BigEndian.Uint64(header[1:]))
	switch mode {
	case modeDownload:
		_, _ = io.CopyN(conn, zeroReader{}, length)
	case modeUpload:
		_, _ = io.CopyN(io.Discard, conn, length)
		_, _ = conn.Write([]byte{1})
	case modeEcho:
		_, _ = io.Copy(conn, conn)
	default:
		return
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type benchmark struct {
	socks     string
	label     string
	mode      string
	conns     int
	bytes     int64
	requests  int
	packets   int
	size      int
	window    int
	warmup    int
	timeout   time.Duration
	tcpTarget *net.TCPAddr
	udpTarget *net.UDPAddr
}

func (b *benchmark) run() (result, error) {
	switch b.mode {
	case "tcp-download":
		return b.runTCPStream(modeDownload)
	case "tcp-upload":
		return b.runTCPStream(modeUpload)
	case "tcp-latency":
		return b.runTCPLatency()
	case "udp-echo":
		return b.runUDP()
	case "first-byte":
		return b.runFirstByte()
	default:
		return result{}, fmt.Errorf("unknown mode %q", b.mode)
	}
}

func (b *benchmark) baseResult(mode string) result {
	return result{
		Label:       b.label,
		Mode:        mode,
		Connections: b.conns,
	}
}

func (b *benchmark) runTCPStream(headerMode byte) (result, error) {
	out := b.baseResult(b.mode)
	started := time.Now()
	var firstErr error
	var errOnce sync.Once
	var waitGroup sync.WaitGroup
	for index := 0; index < b.conns; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			conn, err := b.dialSOCKS(b.tcpTarget)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			defer conn.Close()
			if err = writeHeader(conn, headerMode, b.bytes); err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			switch headerMode {
			case modeDownload:
				_, err = io.CopyN(io.Discard, conn, b.bytes)
			case modeUpload:
				_, err = io.CopyN(conn, zeroReader{}, b.bytes)
				if err == nil {
					var ack [1]byte
					_, err = io.ReadFull(conn, ack[:])
					if err == nil && ack[0] != 1 {
						err = errors.New("bad upload acknowledgement")
					}
				}
			}
			if err != nil {
				errOnce.Do(func() { firstErr = err })
			}
		}()
	}
	waitGroup.Wait()
	out.WallSeconds = time.Since(started).Seconds()
	out.PayloadBytes = b.bytes * int64(b.conns)
	if out.WallSeconds > 0 {
		out.ThroughputMiB = float64(out.PayloadBytes) / (1 << 20) / out.WallSeconds
	}
	if firstErr != nil {
		out.Failures = 1
		out.Errors = []string{firstErr.Error()}
	} else {
		out.Success = b.conns
	}
	return out, nil
}

func (b *benchmark) runTCPLatency() (result, error) {
	out := b.baseResult("tcp-latency")
	conn, err := b.dialSOCKS(b.tcpTarget)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if err = writeHeader(conn, modeEcho, int64(b.size)); err != nil {
		return out, err
	}
	payload := make([]byte, b.size)
	reply := make([]byte, b.size)
	samples := make([]float64, 0, b.requests)
	for index := 0; index < b.warmup+b.requests; index++ {
		started := time.Now()
		if _, err = conn.Write(payload); err != nil {
			return out, err
		}
		if _, err = io.ReadFull(conn, reply); err != nil {
			return out, err
		}
		if index >= b.warmup {
			samples = append(samples, float64(time.Since(started).Microseconds())/1000)
		}
	}
	out.Success = len(samples)
	out.Samples = samples
	out.P50Ms, out.P95Ms, out.P99Ms = percentiles(samples)
	// A latency run still carries payload: report it so CPU/GiB has a numerator.
	out.PayloadBytes = int64(len(samples) * b.size)
	return out, nil
}

func (b *benchmark) runFirstByte() (result, error) {
	out := b.baseResult("first-byte")
	samples := make([]float64, 0, b.requests)
	connectSamples := make([]float64, 0, b.requests)
	payload := bytes.Repeat([]byte{0x5a}, b.size)
	reply := make([]byte, b.size)
	for index := 0; index < b.warmup+b.requests; index++ {
		started := time.Now()
		conn, err := b.dialSOCKS(b.tcpTarget)
		if err != nil {
			return out, err
		}
		connected := time.Now()
		if err = writeHeader(conn, modeEcho, int64(b.size)); err != nil {
			conn.Close()
			return out, err
		}
		if _, err = conn.Write(payload); err != nil {
			conn.Close()
			return out, err
		}
		if _, err = io.ReadFull(conn, reply); err != nil {
			conn.Close()
			return out, err
		}
		finished := time.Now()
		conn.Close()
		if index >= b.warmup {
			connectSamples = append(connectSamples, float64(connected.Sub(started).Microseconds())/1000)
			samples = append(samples, float64(finished.Sub(started).Microseconds())/1000)
		}
	}
	out.Success = len(samples)
	out.Samples = samples
	out.ConnectSamples = connectSamples
	out.P50Ms, out.P95Ms, out.P99Ms = percentiles(samples)
	out.PayloadBytes = int64(len(samples) * b.size * 2)
	return out, nil
}

func (b *benchmark) runUDP() (result, error) {
	out := b.baseResult("udp-echo")
	started := time.Now()
	type associationResult struct {
		received int
		lost     int
		samples  []float64
		err      error
	}
	results := make(chan associationResult, b.conns)
	perAssociation := b.packets / b.conns
	if perAssociation == 0 {
		perAssociation = 1
	}
	for index := 0; index < b.conns; index++ {
		go func() {
			received, lost, samples, err := b.runUDPAssociation(perAssociation)
			results <- associationResult{received: received, lost: lost, samples: samples, err: err}
		}()
	}
	var allSamples []float64
	for index := 0; index < b.conns; index++ {
		association := <-results
		if association.err != nil {
			out.Failures++
			out.Errors = append(out.Errors, association.err.Error())
			continue
		}
		out.Success += association.received
		out.Loss += association.lost
		allSamples = append(allSamples, association.samples...)
	}
	out.WallSeconds = time.Since(started).Seconds()
	out.Packets = perAssociation * b.conns
	out.PayloadBytes = int64(out.Success * b.size)
	if out.WallSeconds > 0 {
		out.ThroughputMiB = float64(out.PayloadBytes) / (1 << 20) / out.WallSeconds
		out.PacketsPerSec = float64(out.Success) / out.WallSeconds
	}
	out.Samples = allSamples
	out.P50Ms, out.P95Ms, out.P99Ms = percentiles(allSamples)
	return out, nil
}

func (b *benchmark) runUDPAssociation(count int) (int, int, []float64, error) {
	control, relay, err := b.udpAssociate()
	if err != nil {
		return 0, 0, nil, err
	}
	defer control.Close()
	defer relay.Close()
	_ = relay.SetReadBuffer(8 << 20)
	_ = relay.SetWriteBuffer(8 << 20)
	_ = relay.SetDeadline(time.Now().Add(b.timeout))

	targetHeader := socksUDPHeader(b.udpTarget)
	received := 0
	lost := 0
	samples := make([]float64, 0, count)
	acks := make(chan struct{}, b.window)
	receiverDone := make(chan struct{})
	go func() {
		defer close(receiverDone)
		buffer := make([]byte, 65535)
		for received < count {
			n, _, readErr := relay.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			payload, headerErr := stripSocksUDPHeader(buffer[:n])
			if headerErr != nil || len(payload) < 16 {
				continue
			}
			sequence := int(binary.BigEndian.Uint64(payload[:8]))
			if sequence < 0 || sequence >= count {
				continue
			}
			sentAt := int64(binary.BigEndian.Uint64(payload[8:16]))
			samples = append(samples, float64(time.Since(time.Unix(0, sentAt)).Microseconds())/1000)
			received++
			select {
			case acks <- struct{}{}:
			default:
			}
		}
	}()

	datagram := make([]byte, 3+len(targetHeader)+b.size)
	copy(datagram[3:], targetHeader)
	outstanding := 0
	for sequence := 0; sequence < count; sequence++ {
		if outstanding >= b.window {
			select {
			case <-acks:
				outstanding--
			case <-time.After(2 * time.Second):
				// One lost echo must not stall the window for the whole
				// operation timeout. Treat the window as drained and keep
				// sending; the loss counter records what never arrived.
				outstanding = 0
			}
		}
		payload := datagram[3+len(targetHeader):]
		binary.BigEndian.PutUint64(payload[:8], uint64(sequence))
		binary.BigEndian.PutUint64(payload[8:16], uint64(time.Now().UnixNano()))
		// Fill the rest with a stable pattern; content does not matter beyond
		// making the datagram the requested size.
		for index := 16; index < len(payload); index++ {
			payload[index] = byte(sequence)
		}
		if _, err = relay.Write(datagram); err != nil {
			return received, count - received, samples, err
		}
		outstanding++
	}
	// Wait for the last expected echo; only a lost tail pays the grace period.
	// Waiting for the operation timeout here would turn one dropped datagram
	// into a two-minute run.
	select {
	case <-receiverDone:
	case <-time.After(750 * time.Millisecond):
	}
	_ = relay.Close()
	<-receiverDone
	lost = count - received
	if lost < 0 {
		lost = 0
	}
	return received, lost, samples, nil
}

func (b *benchmark) dialSOCKS(target *net.TCPAddr) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", b.socks, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if err = conn.SetDeadline(time.Now().Add(b.timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err = conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	greeting := make([]byte, 2)
	if _, err = io.ReadFull(conn, greeting); err != nil {
		conn.Close()
		return nil, err
	}
	if greeting[0] != 0x05 || greeting[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 greeting rejected: %v", greeting)
	}
	request := []byte{0x05, 0x01, 0x00, 0x01}
	request = append(request, target.IP.To4()...)
	request = binary.BigEndian.AppendUint16(request, uint16(target.Port))
	if _, err = conn.Write(request); err != nil {
		conn.Close()
		return nil, err
	}
	if err = readSocksReply(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (b *benchmark) udpAssociate() (net.Conn, *net.UDPConn, error) {
	control, err := net.DialTimeout("tcp", b.socks, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	_ = control.SetDeadline(time.Now().Add(b.timeout))
	if _, err = control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		control.Close()
		return nil, nil, err
	}
	greeting := make([]byte, 2)
	if _, err = io.ReadFull(control, greeting); err != nil {
		control.Close()
		return nil, nil, err
	}
	if greeting[0] != 0x05 || greeting[1] != 0x00 {
		control.Close()
		return nil, nil, fmt.Errorf("SOCKS5 greeting rejected: %v", greeting)
	}
	if _, err = control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		control.Close()
		return nil, nil, err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(control, header); err != nil {
		control.Close()
		return nil, nil, err
	}
	if header[0] != 0x05 || header[1] != 0x00 {
		control.Close()
		return nil, nil, fmt.Errorf("SOCKS5 UDP associate rejected: %v", header)
	}
	host, port, err := readSocksBind(control, header[3])
	if err != nil {
		control.Close()
		return nil, nil, err
	}
	if host.IsUnspecified() {
		proxyHost, _, splitErr := net.SplitHostPort(b.socks)
		if splitErr != nil {
			control.Close()
			return nil, nil, splitErr
		}
		parsed, parseErr := net.ResolveIPAddr("ip", proxyHost)
		if parseErr != nil {
			control.Close()
			return nil, nil, parseErr
		}
		host = parsed.IP
	}
	relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: host, Port: port})
	if err != nil {
		control.Close()
		return nil, nil, err
	}
	return control, relay, nil
}

func readSocksReply(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS request failed with code %d", header[1])
	}
	_, _, err := readSocksBind(conn, header[3])
	return err
}

func readSocksBind(conn net.Conn, addressType byte) (net.IP, int, error) {
	switch addressType {
	case 0x01:
		raw := make([]byte, 6)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return nil, 0, err
		}
		return net.IP(raw[:4]), int(binary.BigEndian.Uint16(raw[4:])), nil
	case 0x04:
		raw := make([]byte, 18)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return nil, 0, err
		}
		return net.IP(raw[:16]), int(binary.BigEndian.Uint16(raw[16:])), nil
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return nil, 0, err
		}
		raw := make([]byte, int(length[0])+2)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return nil, 0, err
		}
		ip, err := net.ResolveIPAddr("ip", string(raw[:int(length[0])]))
		if err != nil {
			return nil, 0, err
		}
		return ip.IP, int(binary.BigEndian.Uint16(raw[int(length[0]):])), nil
	default:
		return nil, 0, fmt.Errorf("unrecognized SOCKS address type %d", addressType)
	}
}

func writeHeader(conn net.Conn, mode byte, length int64) error {
	var header [9]byte
	header[0] = mode
	binary.BigEndian.PutUint64(header[1:], uint64(length))
	_, err := conn.Write(header[:])
	return err
}

func socksUDPHeader(target *net.UDPAddr) []byte {
	header := []byte{0x01}
	header = append(header, target.IP.To4()...)
	header = binary.BigEndian.AppendUint16(header, uint16(target.Port))
	return header
}

func stripSocksUDPHeader(datagram []byte) ([]byte, error) {
	if len(datagram) < 3 {
		return nil, io.ErrUnexpectedEOF
	}
	if datagram[0] != 0 || datagram[1] != 0 || datagram[2] != 0 {
		return nil, errors.New("invalid SOCKS UDP header")
	}
	offset := 3
	switch datagram[offset] {
	case 0x01:
		offset += 7
	case 0x04:
		offset += 19
	case 0x03:
		if len(datagram) < offset+2 {
			return nil, io.ErrUnexpectedEOF
		}
		offset += 2 + int(datagram[offset+1])
	default:
		return nil, fmt.Errorf("unrecognized SOCKS UDP address type %d", datagram[offset])
	}
	if offset > len(datagram) {
		return nil, io.ErrUnexpectedEOF
	}
	return datagram[offset:], nil
}

func percentiles(samples []float64) (p50 float64, p95 float64, p99 float64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	return sorted[percentileIndex(len(sorted), 50)], sorted[percentileIndex(len(sorted), 95)], sorted[percentileIndex(len(sorted), 99)]
}

func percentileIndex(length int, percentile int) int {
	index := (length*percentile + 99) / 100
	if index >= length {
		return length - 1
	}
	if index < 0 {
		return 0
	}
	return index
}
