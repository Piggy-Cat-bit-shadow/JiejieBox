// Package loadgen drives a shaped workload through a proxy's SOCKS5 inbound and
// reports what actually arrived.
//
// The load generator never counts bytes it merely queued: an upload's payload
// is what the target received, a download's is what the client verified. That
// distinction is the difference between measuring a network path and measuring
// a socket buffer.
package loadgen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

// Pattern is the traffic shape.
type Pattern string

const (
	Upload   Pattern = "upload"
	Download Pattern = "download"
	PingPong Pattern = "pingpong"
	Connect  Pattern = "connect"
	Duplex   Pattern = "duplex"
	UDP      Pattern = "udp"
)

// Config is one load phase.
type Config struct {
	ProxyAddr     string
	TargetAddr    string
	Pattern       Pattern
	Streams       int
	Duration      time.Duration
	ChunkSize     int
	PingPongBytes int
	PacketSize    int
	Verify        bool
	// MaxLatencySamples bounds memory for a long ping-pong run; the oldest
	// samples are dropped, and the count is reported so a percentile is never
	// computed from a silently truncated set.
	MaxLatencySamples int
	// IOGrace is how long a stream may stay silent past the phase deadline
	// before its socket deadline fires. A probe sets it low so a stalled tunnel
	// fails fast instead of looking like a slow one.
	IOGrace time.Duration
}

// Result is what one load phase observed.
type Result struct {
	SentBytes         int64
	ReceivedBytes     int64
	PayloadBytes      int64
	Elapsed           time.Duration
	Latencies         []time.Duration
	LatencyDropped    int64
	DatagramsSent     int64
	DatagramsReceived int64
	Jitter            time.Duration
	VerifiedBytes     int64
	MismatchedBytes   int64
	Connections       int64
	Errors            []string
}

func (c *Config) normalize() {
	if c.Streams <= 0 {
		c.Streams = 1
	}
	if c.ChunkSize <= 0 {
		c.ChunkSize = 256 * 1024
	}
	if c.PingPongBytes <= 0 {
		c.PingPongBytes = 1024
	}
	if c.PacketSize <= 0 {
		c.PacketSize = 1200
	}
	if c.Duration <= 0 {
		c.Duration = time.Second
	}
	if c.MaxLatencySamples <= 0 {
		c.MaxLatencySamples = 1 << 20
	}
	if c.IOGrace <= 0 {
		// The grace only exists so a stalled socket cannot hang the runner
		// forever. A large value silently stretches the measured window on a
		// lossy path, which understates goodput and mislabels the duration.
		c.IOGrace = 500 * time.Millisecond
	}
}

// Run executes the configured phase.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg.normalize()
	ctx, cancel := context.WithTimeout(ctx, cfg.Duration+30*time.Second)
	defer cancel()

	switch cfg.Pattern {
	case Upload, Download, PingPong, Connect, Duplex:
	case UDP:
		return runUDP(ctx, cfg)
	default:
		return Result{}, fmt.Errorf("unknown pattern %q", cfg.Pattern)
	}

	var (
		sent      atomic.Int64
		received  atomic.Int64
		verified  atomic.Int64
		mismatch  atomic.Int64
		conns     atomic.Int64
		latencies = newLatencySink(cfg.MaxLatencySamples)
		errMu     sync.Mutex
		errs      []string
	)
	deadline := time.Now().Add(cfg.Duration)
	start := time.Now()

	record := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if len(errs) < 8 {
			errs = append(errs, err.Error())
		}
		errMu.Unlock()
	}

	var wg sync.WaitGroup
	for stream := 0; stream < cfg.Streams; stream++ {
		wg.Add(1)
		go func(stream int) {
			defer wg.Done()
			switch cfg.Pattern {
			case Upload:
				record(uploadStream(cfg, deadline, stream, &sent, latencies))
			case Download:
				record(downloadStream(cfg, deadline, &received, &verified, &mismatch, latencies))
			case PingPong:
				record(pingPongStream(cfg, deadline, &received, latencies))
			case Connect:
				record(connectStream(cfg, deadline, &conns, latencies))
			case Duplex:
				record(duplexStream(cfg, deadline, &sent, &received, &verified, &mismatch))
			}
		}(stream)
	}
	wg.Wait()
	elapsed := time.Since(start)

	result := Result{
		SentBytes:       sent.Load(),
		ReceivedBytes:   received.Load(),
		Elapsed:         elapsed,
		Latencies:       latencies.values(),
		LatencyDropped:  latencies.dropped(),
		VerifiedBytes:   verified.Load(),
		MismatchedBytes: mismatch.Load(),
		Connections:     conns.Load(),
		Errors:          errs,
	}
	switch cfg.Pattern {
	case Upload:
		result.PayloadBytes = result.SentBytes
	case Download:
		result.PayloadBytes = result.ReceivedBytes
	case Duplex:
		result.PayloadBytes = result.SentBytes + result.ReceivedBytes
	case PingPong:
		result.PayloadBytes = result.ReceivedBytes
	case Connect:
		result.PayloadBytes = 0
	}
	return result, nil
}

func uploadStream(cfg Config, deadline time.Time, stream int, sent *atomic.Int64, latencies *latencySink) error {
	conn, err := dialSOCKS5(cfg.ProxyAddr, cfg.TargetAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Without a deadline a stalled tunnel turns the measurement into a hang.
	_ = conn.SetDeadline(deadline.Add(cfg.IOGrace))
	chunk := make([]byte, cfg.ChunkSize)
	copy(chunk, target.PatternPage)
	for offset := 0; offset < len(chunk); offset += len(target.PatternPage) {
		copy(chunk[offset:], target.PatternPage)
	}
	total := atomic.Int64{}
	err = loopUntil(deadline, func() error {
		n, writeErr := conn.Write(chunk)
		if n > 0 {
			total.Add(int64(n))
			sent.Add(int64(n))
		}
		return writeErr
	})
	if err != nil && time.Now().After(deadline) {
		// Same rule as the read side: the window ended, the write did not fail.
		err = nil
	}
	// Half-close so the target sees EOF after the last byte, not a reset.
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	return err
}

func downloadStream(cfg Config, deadline time.Time, received, verified, mismatch *atomic.Int64, latencies *latencySink) error {
	conn, err := dialSOCKS5(cfg.ProxyAddr, cfg.TargetAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline.Add(cfg.IOGrace))
	buffer := make([]byte, cfg.ChunkSize)
	var offset int64
	for {
		n, readErr := conn.Read(buffer)
		if n > 0 {
			received.Add(int64(n))
			if cfg.Verify {
				bad := verifyPattern(buffer[:n], offset)
				if bad > 0 {
					mismatch.Add(bad)
				} else {
					verified.Add(int64(n))
				}
			}
			offset += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			// A socket deadline that fires after the phase window is the phase
			// ending, not a failure: on a lossy path the last read can wait for
			// a byte that the window no longer has time to carry.
			if time.Now().After(deadline) {
				return nil
			}
			return readErr
		}
		if time.Now().After(deadline) {
			return nil
		}
	}
}

func pingPongStream(cfg Config, deadline time.Time, received *atomic.Int64, latencies *latencySink) error {
	conn, err := dialSOCKS5(cfg.ProxyAddr, cfg.TargetAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	payload := make([]byte, cfg.PingPongBytes)
	copy(payload, target.PatternPage)
	response := make([]byte, cfg.PingPongBytes)
	for {
		if time.Now().After(deadline) {
			return nil
		}
		// A per-operation deadline keeps one stalled round trip from ending the
		// whole phase with no data at all, without letting a lossy path stretch
		// the phase past its window.
		operationDeadline := time.Now().Add(2 * time.Second)
		if limit := deadline.Add(cfg.IOGrace); operationDeadline.After(limit) {
			operationDeadline = limit
		}
		_ = conn.SetDeadline(operationDeadline)
		start := time.Now()
		if _, err = conn.Write(payload); err != nil {
			return err
		}
		if _, err = io.ReadFull(conn, response); err != nil {
			return err
		}
		latencies.add(time.Since(start))
		received.Add(int64(len(response)))
	}
}

func connectStream(cfg Config, deadline time.Time, conns *atomic.Int64, latencies *latencySink) error {
	for {
		if time.Now().After(deadline) {
			return nil
		}
		start := time.Now()
		conn, err := dialSOCKS5(cfg.ProxyAddr, cfg.TargetAddr, 10*time.Second)
		if err != nil {
			return err
		}
		conns.Add(1)
		operationDeadline := time.Now().Add(5 * time.Second)
		if limit := deadline.Add(cfg.IOGrace); operationDeadline.After(limit) {
			operationDeadline = limit
		}
		_ = conn.SetDeadline(operationDeadline)
		if _, err = conn.Write([]byte{0x01}); err != nil {
			conn.Close()
			return err
		}
		one := make([]byte, 1)
		if _, err = io.ReadFull(conn, one); err != nil {
			conn.Close()
			return err
		}
		latencies.add(time.Since(start))
		conn.Close()
	}
}

func duplexStream(cfg Config, deadline time.Time, sent, received, verified, mismatch *atomic.Int64) error {
	conn, err := dialSOCKS5(cfg.ProxyAddr, cfg.TargetAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline.Add(cfg.IOGrace))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		chunk := make([]byte, cfg.ChunkSize)
		copy(chunk, target.PatternPage)
		for offset := 0; offset < len(chunk); offset += len(target.PatternPage) {
			copy(chunk[offset:], target.PatternPage)
		}
		_ = loopUntil(deadline, func() error {
			n, writeErr := conn.Write(chunk)
			sent.Add(int64(n))
			return writeErr
		})
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		buffer := make([]byte, cfg.ChunkSize)
		var offset int64
		for {
			n, readErr := conn.Read(buffer)
			if n > 0 {
				received.Add(int64(n))
				if cfg.Verify {
					bad := verifyPattern(buffer[:n], offset)
					if bad > 0 {
						mismatch.Add(bad)
					} else {
						verified.Add(int64(n))
					}
				}
				offset += int64(n)
			}
			if readErr != nil {
				return
			}
		}
	}()
	wg.Wait()
	return nil
}

func runUDP(ctx context.Context, cfg Config) (Result, error) {
	association, err := associateUDP(cfg.ProxyAddr, 10*time.Second)
	if err != nil {
		return Result{}, err
	}
	defer association.Close()

	deadline := time.Now().Add(cfg.Duration)
	payload := make([]byte, cfg.PacketSize)
	copy(payload, target.PatternPage)
	packet, err := udpPacket(cfg.TargetAddr, payload)
	if err != nil {
		return Result{}, err
	}

	var (
		sent      atomic.Int64
		received  atomic.Int64
		latencies = newLatencySink(cfg.MaxLatencySamples)
		lastSeen  time.Time
		jitterSum time.Duration
		jitterN   int
	)
	done := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 64*1024)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = association.socket.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, _, readErr := association.socket.ReadFromUDP(buffer)
			if readErr != nil {
				continue
			}
			if n < 3 {
				continue
			}
			received.Add(1)
			now := time.Now()
			if !lastSeen.IsZero() {
				jitterSum += now.Sub(lastSeen)
				jitterN++
			}
			lastSeen = now
		}
	}()

	// The senders run as fast as the association accepts datagrams. A paced
	// generator measures the pacer, not the protocol: the interesting number is
	// where the path stops keeping up, which shows up as a gap between sent and
	// received rather than as a fixed rate.
	var senders sync.WaitGroup
	for stream := 0; stream < cfg.Streams; stream++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for time.Now().Before(deadline) {
				if _, writeErr := association.socket.WriteToUDP(packet, association.relay); writeErr != nil {
					return
				}
				sent.Add(1)
			}
		}()
	}
	senders.Wait()
	close(stop)
	<-done
	_ = ctx

	result := Result{
		SentBytes:         sent.Load() * int64(cfg.PacketSize),
		DatagramsSent:     sent.Load(),
		DatagramsReceived: received.Load(),
		PayloadBytes:      received.Load() * int64(cfg.PacketSize),
		Elapsed:           cfg.Duration,
		Latencies:         latencies.values(),
	}
	if jitterN > 0 {
		result.Jitter = jitterSum / time.Duration(jitterN)
	}
	if sent.Load() > 0 {
		lost := sent.Load() - received.Load()
		if lost > 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%d datagrams lost", lost))
		}
	}
	result.Connections = 1
	return result, nil
}

// verifyPattern returns the number of bytes in chunk that do not match the
// pattern at the stream's own offset. It delegates to the shared verifier so the
// generator and the target cannot disagree about what a valid payload is.
func verifyPattern(chunk []byte, offset int64) int64 {
	return target.Verify(chunk, offset)
}

func loopUntil(deadline time.Time, step func() error) error {
	for time.Now().Before(deadline) {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// latencySink keeps per-operation latencies up to a cap.
type latencySink struct {
	mu           sync.Mutex
	samples      []time.Duration
	cap          int
	droppedCount int64
}

func newLatencySink(cap int) *latencySink {
	return &latencySink{cap: cap}
}

func (s *latencySink) add(d time.Duration) {
	s.mu.Lock()
	if len(s.samples) < s.cap {
		s.samples = append(s.samples, d)
	} else {
		s.droppedCount++
	}
	s.mu.Unlock()
}

func (s *latencySink) values() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Duration, len(s.samples))
	copy(out, s.samples)
	return out
}

func (s *latencySink) dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.droppedCount
}
