// Package target is the far end of a measured transfer: the thing behind the
// proxy. It runs in the lab process, not through the proxy, so its own CPU is
// load-generator cost and is reported separately.
package target

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is the traffic shape the target serves.
type Kind string

const (
	// Sink reads and discards, verifying the pattern. Used for upload.
	Sink Kind = "sink"
	// Source writes the pattern for Duration then closes. Used for download.
	Source Kind = "source"
	// Echo writes back exactly what it reads. Used for ping-pong and connect.
	Echo Kind = "echo"
	// Duplex reads and writes at the same time.
	Duplex Kind = "duplex"
	// UDPEcho reflects datagrams.
	UDPEcho Kind = "udp-echo"
)

// PatternPage is the 4 KiB block every stream is made of. Verification is a
// memcmp against this page, which is fast enough not to become the bottleneck
// the measurement is trying to see past.
var PatternPage = func() []byte {
	page := make([]byte, 4096)
	for i := range page {
		page[i] = byte(i*31 + 7)
	}
	return page
}()

// Stats is what the target observed.
type Stats struct {
	Bytes     int64
	Datagrams int64
	// Echoed counts datagrams whose echo write succeeded, so an ingress count is
	// never confused with a datagram that actually left the target.
	Echoed      int64
	Connections int64
	Verified    int64
	Mismatched  int64
	WriteBytes  int64
	Extra       map[string]int64
}

// Server serves one kind on one address until Close.
type Server struct {
	kind     Kind
	duration time.Duration
	verify   bool

	listener net.Listener
	packet   net.PacketConn
	closed   atomic.Bool

	bytes       atomic.Int64
	datagrams   atomic.Int64
	echoed      atomic.Int64
	connections atomic.Int64
	verified    atomic.Int64
	mismatched  atomic.Int64
	writeBytes  atomic.Int64

	wg sync.WaitGroup
}

// Start binds addr and serves kind. duration is how long a Source writes before
// closing; it is ignored by the other kinds.
func Start(kind Kind, addr string, duration time.Duration, verify bool) (*Server, error) {
	s := &Server{kind: kind, duration: duration, verify: verify}
	if kind == UDPEcho {
		packet, err := net.ListenPacket("udp", addr)
		if err != nil {
			return nil, err
		}
		s.packet = packet
		s.wg.Add(1)
		go s.serveUDP()
		return s, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s.listener = listener
	s.wg.Add(1)
	go s.serveTCP()
	return s, nil
}

// Addr reports the bound address.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	if s.packet != nil {
		return s.packet.LocalAddr().String()
	}
	return ""
}

// Snapshot returns the counters accumulated so far.
func (s *Server) Snapshot() Stats {
	return Stats{
		Bytes:       s.bytes.Load(),
		Datagrams:   s.datagrams.Load(),
		Echoed:      s.echoed.Load(),
		Connections: s.connections.Load(),
		Verified:    s.verified.Load(),
		Mismatched:  s.mismatched.Load(),
		WriteBytes:  s.writeBytes.Load(),
	}
}

// Reset zeroes the counters between warmup and measurement.
func (s *Server) Reset() {
	s.bytes.Store(0)
	s.datagrams.Store(0)
	s.echoed.Store(0)
	s.connections.Store(0)
	s.verified.Store(0)
	s.mismatched.Store(0)
	s.writeBytes.Store(0)
}

// Close stops the server and waits for its goroutines.
func (s *Server) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	if s.packet != nil {
		err = s.packet.Close()
	}
	s.wg.Wait()
	return err
}

func (s *Server) serveTCP() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connections.Add(1)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	switch s.kind {
	case Sink:
		s.readAll(conn)
	case Source:
		s.writeFor(conn, s.duration)
	case Echo:
		s.echo(conn)
	case Duplex:
		var reader sync.WaitGroup
		reader.Add(1)
		go func() {
			defer reader.Done()
			s.readAll(conn)
		}()
		s.writeFor(conn, s.duration)
		_ = conn.Close()
		reader.Wait()
	}
}

// readAll counts bytes and, when verification is on, checks each block against
// the pattern. The stream is verified at its own offsets, so a chunked write at
// the sender cannot shift the comparison.
func (s *Server) readAll(conn net.Conn) {
	buffer := make([]byte, 256*1024)
	var offset int64
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			s.bytes.Add(int64(n))
			if s.verify {
				s.verifyBlock(buffer[:n], offset)
			}
			offset += int64(n)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return
			}
			return
		}
	}
}

func (s *Server) verifyBlock(chunk []byte, offset int64) {
	total := int64(len(chunk))
	if bad := Verify(chunk, offset); bad > 0 {
		s.mismatched.Add(bad)
		return
	}
	s.verified.Add(total)
}

func (s *Server) writeFor(conn net.Conn, duration time.Duration) {
	deadline := time.Now().Add(duration)
	chunk := make([]byte, 256*1024)
	for i := range chunk {
		chunk[i] = PatternPage[i%len(PatternPage)]
	}
	for time.Now().Before(deadline) {
		n, err := conn.Write(chunk)
		if n > 0 {
			s.writeBytes.Add(int64(n))
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) echo(conn net.Conn) {
	buffer := make([]byte, 256*1024)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			if _, writeErr := conn.Write(buffer[:n]); writeErr != nil {
				return
			}
			s.bytes.Add(int64(n))
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) serveUDP() {
	defer s.wg.Done()
	buffer := make([]byte, 64*1024)
	for {
		n, from, err := s.packet.ReadFrom(buffer)
		if err != nil {
			return
		}
		s.datagrams.Add(1)
		s.bytes.Add(int64(n))
		if written, err := s.packet.WriteTo(buffer[:n], from); err == nil && written > 0 {
			s.echoed.Add(1)
		}
	}
}

// DecodeLength reads a length prefix used by the ping-pong pattern.
func DecodeLength(header []byte) int {
	return int(binary.BigEndian.Uint32(header))
}

// EncodeLength writes a length prefix.
func EncodeLength(buffer []byte, n int) {
	binary.BigEndian.PutUint32(buffer, uint32(n))
}
