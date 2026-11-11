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
	"encoding/binary"
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
	// Stagger delays stream N by N*Stagger before it starts, so a diagnostic run
	// can separate "concurrent handshakes fail" from "the transfer path fails".
	// Zero starts every stream together, which is what a throughput cell wants.
	Stagger time.Duration
	// UDPRate is the offered rate for the udp pattern, in packets per second.
	// Zero keeps the historical unpaced saturation mode; a positive value selects
	// paced mode, which is the only mode in which "lost" describes the path.
	UDPRate int
	// UDPDrain is the post-send observation window. Zero selects the default
	// (500 ms when paced, none when unpaced); a negative value disables it.
	UDPDrain time.Duration
	// UDPQuietGuard overrides the tail guard used to decide whether the last
	// reply sat too close to the observation edge. Zero selects max(250 ms, 10 %
	// of the window). It is an operational policy, never proof of no late tail.
	UDPQuietGuard time.Duration
	// UDPEpoch tags this phase's payloads so a late packet from another phase can
	// be counted as stale instead of as a duplicate.
	UDPEpoch uint32
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
	// UDP accounting: writes are local accepts, unique is what came back once,
	// duplicate/out_of_order are attribution, foreign/stale are rejected input.
	DatagramAttempts   int64
	DatagramWrites     int64
	DatagramScheduled  int64
	DatagramSkipped    int64
	DatagramUnique     int64
	DatagramDuplicate  int64
	DatagramReturned   int64
	DatagramOutOfOrder int64
	DatagramForeign    int64
	DatagramStale      int64
	SendWindow         time.Duration
	DrainDuration      time.Duration
	DrainLastReply     time.Duration
	// DrainComplete is kept as the alias "the observation window reached
	// complete_quiet". New consumers use the split fields below; old JSON that
	// carries only DrainComplete is not proof of a complete tail.
	DrainComplete   bool
	AllReturned     bool
	Aborted         bool
	Paced           bool
	Jitter          time.Duration
	VerifiedBytes   int64
	MismatchedBytes int64
	Connections     int64
	Errors          []string

	// Split observation state. ObservationDuration is the configured post-send
	// horizon; ObservationComplete means it ran to its end with a live context.
	// TailActive means the last reply landed inside the guard, so the tail may
	// have been cut. QuietPeriod is time since the last reply at the end.
	ObservationDuration time.Duration
	ObservationElapsed  time.Duration
	ObservationComplete bool
	QuietPeriod         time.Duration
	TailActive          bool
	DrainStatus         string
	// LossEstimable is true only for complete_quiet with exact sequence
	// tracking. lost/loss_percent are only derived from such a run.
	LossEstimable bool
	// NotReturned is writes-unique, i.e. "no reply before the deadline", never
	// "the network dropped this many". It is recorded even when not estimable.
	NotReturned      int64
	NotReturnedRatio float64
	// SequenceTrackingComplete is false once the sequence window is full; unique
	// and duplicate are then lower bounds only.
	SequenceTrackingComplete bool
	DatagramUnattributed     int64
	DatagramWriteErrors      int64
	// SendPhaseDuration is the time from phase start to the last write
	// completion; WriteCompletionPPS is writes divided by it (not by the
	// configured duration). SendOverrun is how far it exceeded the window.
	SendPhaseDuration  time.Duration
	SendOverrun        time.Duration
	ConfiguredPPS      float64
	ScheduledPPS       float64
	AttemptPPS         float64
	WriteCompletionPPS float64
	// Offsets from phase start for the last scheduled callback, the last write
	// start and the last successful write completion.
	LastScheduledOffset  time.Duration
	LastWriteStartOffset time.Duration
	LastWriteEndOffset   time.Duration
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
			if cfg.Stagger > 0 && stream > 0 {
				startTimer := time.NewTimer(time.Duration(stream) * cfg.Stagger)
				select {
				case <-ctx.Done():
					startTimer.Stop()
					return
				case <-startTimer.C:
				}
			}
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

	rate := cfg.UDPRate
	paced := rate > 0
	drain := cfg.UDPDrain
	if drain == 0 {
		drain = 0
		if paced {
			drain = 500 * time.Millisecond
		}
	}
	if drain < 0 {
		drain = 0
	}
	guard := cfg.UDPQuietGuard
	if guard <= 0 {
		guard = drain / 10
		if guard < 250*time.Millisecond {
			guard = 250 * time.Millisecond
		}
	}
	if cfg.PacketSize < udpHeaderSize+16 {
		cfg.PacketSize = udpHeaderSize + 16
	}
	epoch := cfg.UDPEpoch
	if epoch == 0 {
		epoch = uint32(time.Now().UnixNano())
	}
	// The reader must validate against the epoch that was actually stamped, not
	// the zero value that means "pick one".
	cfg.UDPEpoch = epoch

	template := make([]byte, cfg.PacketSize)
	for offset := udpHeaderSize; offset < len(template); offset += len(target.PatternPage) {
		copy(template[offset:], target.PatternPage)
	}
	socksPacket := func(payload []byte) ([]byte, error) {
		return udpPacket(cfg.TargetAddr, payload)
	}

	start := time.Now()
	deadline := start.Add(cfg.Duration)
	// All local measurements use offsets from start on the monotonic clock.
	// UnixNano is deliberately not used for timing: an NTP step between send and
	// receive would otherwise turn into a negative or inflated latency.
	mono := func() int64 { return time.Since(start).Nanoseconds() }
	_ = association.socket.SetWriteDeadline(deadline.Add(drain + 2*time.Second))

	var (
		attempts       atomic.Int64
		writes         atomic.Int64
		writeErrors    atomic.Int64
		scheduled      atomic.Int64
		skipped        atomic.Int64
		seq            atomic.Uint32
		firstErr       atomic.Value
		sendEnd        atomic.Int64
		lastScheduled  atomic.Int64
		lastWriteStart atomic.Int64
		lastWriteEnd   atomic.Int64
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		firstErr.CompareAndSwap(nil, err.Error())
	}
	sendOne := func(buffer []byte) {
		sequence := seq.Add(1)
		binary.BigEndian.PutUint32(buffer[0:4], udpMagic)
		binary.BigEndian.PutUint32(buffer[4:8], epoch)
		binary.BigEndian.PutUint32(buffer[8:12], sequence)
		// sent_monotonic_ns: an offset from phase start, not a wall-clock stamp.
		// The reader lives in the same process and clock domain, so this is a
		// monotonic one-way delay estimate.
		binary.BigEndian.PutUint64(buffer[12:20], uint64(mono()))
		attempts.Add(1)
		packet, buildErr := socksPacket(buffer)
		if buildErr != nil {
			writeErrors.Add(1)
			recordErr(buildErr)
			return
		}
		lastWriteStart.Store(mono())
		if _, writeErr := association.socket.WriteToUDP(packet, association.relay); writeErr != nil {
			writeErrors.Add(1)
			recordErr(writeErr)
			return
		}
		lastWriteEnd.Store(mono())
		writes.Add(1)
	}

	var latencies = newLatencySink(cfg.MaxLatencySamples)
	var (
		unique, duplicate, outOfOrder, foreign, stale int64
		unattributed                                  int64
		jitterSum                                     time.Duration
		jitterN                                       int
		lastSeen                                      time.Time
		lastReplyAt                                   time.Time
	)
	window := newUDPSeqWindow()
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
				select {
				case <-ctx.Done():
					return
				default:
				}
				continue
			}
			payload := socksUDPPayload(buffer[:n])
			if len(payload) < udpHeaderSize {
				foreign++
				continue
			}
			magic, packetEpoch, sequence, sentNs, ok := parseUDPHeader(payload)
			if !ok || magic != udpMagic || (cfg.UDPEpoch != 0 && packetEpoch != cfg.UDPEpoch) {
				if ok && magic == udpMagic {
					stale++
				} else {
					foreign++
				}
				continue
			}
			lastReplyAt = time.Now()
			first, exact := window.add(sequence)
			if !exact {
				// The sequence window is full; this reply cannot be attributed as
				// unique or duplicate. Never count it as unique.
				unattributed++
				continue
			}
			if first {
				unique++
				nowMono := mono()
				if sentNs > 0 && int64(sentNs) <= nowMono {
					latencies.add(time.Duration(nowMono - int64(sentNs)))
				}
			} else {
				duplicate++
			}
			if first && sequence < window.maxSeen && window.maxSeen > 1 {
				outOfOrder++
			}
			now := time.Now()
			if !lastSeen.IsZero() {
				jitterSum += now.Sub(lastSeen)
				jitterN++
			}
			lastSeen = now
		}
	}()

	var senders sync.WaitGroup
	if paced {
		senders.Add(1)
		go func() {
			defer senders.Done()
			buffer := append([]byte(nil), template...)
			paced := runPacer(ctx, start, deadline, rate, func() {
				lastScheduled.Store(mono())
				sendOne(buffer)
			})
			scheduled.Add(paced.scheduled)
			skipped.Add(paced.skipped)
			if paced.aborted {
				recordErr(ctx.Err())
			}
			sendEnd.Store(mono())
		}()
	} else {
		for stream := 0; stream < cfg.Streams; stream++ {
			senders.Add(1)
			go func() {
				defer senders.Done()
				buffer := append([]byte(nil), template...)
				for time.Now().Before(deadline) {
					if ctx.Err() != nil {
						recordErr(ctx.Err())
						return
					}
					scheduled.Add(1)
					lastScheduled.Store(mono())
					sendOne(buffer)
				}
				sendEnd.Store(mono())
			}()
		}
	}
	senders.Wait()
	sendOffset := sendEnd.Load()
	if sendOffset <= 0 {
		sendOffset = mono()
	}
	sendWindowEnd := start.Add(time.Duration(sendOffset))
	sendWindow := time.Duration(sendOffset)

	drainStart := time.Now()
	if drain > 0 && ctx.Err() == nil {
		timer := time.NewTimer(drain)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	drainDuration := time.Since(drainStart)
	close(stop)
	<-done

	// The reader has returned: its lastReplyAt is now immutable and can be read.
	drainLastReply := time.Duration(0)
	if !lastReplyAt.IsZero() {
		drainLastReply = lastReplyAt.Sub(sendWindowEnd)
		if drainLastReply < 0 {
			drainLastReply = 0
		}
	}
	written := writes.Load()
	writeErrCount := writeErrors.Load()
	returned := unique + duplicate
	notReturned := written - unique
	if notReturned < 0 {
		notReturned = 0
	}
	quiet := drainDuration
	if !lastReplyAt.IsZero() {
		quiet = time.Since(lastReplyAt)
	}
	drainStatus, observationComplete, tailActive := drainStateFor(ctx.Err(), drain, quiet, !lastReplyAt.IsZero(), guard)
	sequenceComplete := !window.truncated
	lossEstimable := drainStatus == "complete_quiet" && sequenceComplete
	phase := sendWindow
	if phase <= 0 {
		phase = time.Millisecond
	}
	result := Result{
		SentBytes:          written * int64(cfg.PacketSize),
		DatagramsSent:      written,
		DatagramsReceived:  returned,
		PayloadBytes:       unique * int64(cfg.PacketSize),
		Elapsed:            sendWindow,
		Latencies:          latencies.values(),
		DatagramAttempts:   attempts.Load(),
		DatagramWrites:     written,
		DatagramScheduled:  scheduled.Load(),
		DatagramSkipped:    skipped.Load(),
		DatagramUnique:     unique,
		DatagramDuplicate:  duplicate,
		DatagramReturned:   returned,
		DatagramOutOfOrder: outOfOrder,
		DatagramForeign:    foreign,
		DatagramStale:      stale,
		SendWindow:         sendWindow,
		DrainDuration:      drainDuration,
		DrainLastReply:     drainLastReply,
		DrainComplete:      lossEstimable,
		AllReturned:        written > 0 && unique >= written,
		Aborted:            ctx.Err() != nil,
		Paced:              paced,

		ObservationDuration:      drain,
		ObservationElapsed:       drainDuration,
		ObservationComplete:      observationComplete,
		QuietPeriod:              quiet,
		TailActive:               tailActive,
		DrainStatus:              drainStatus,
		LossEstimable:            lossEstimable,
		NotReturned:              notReturned,
		SequenceTrackingComplete: sequenceComplete,
		DatagramUnattributed:     unattributed,
		DatagramWriteErrors:      writeErrCount,
		SendPhaseDuration:        sendWindow,
		SendOverrun:              maxDuration(0, sendWindow-cfg.Duration),
		ConfiguredPPS:            float64(rate),
		ScheduledPPS:             float64(scheduled.Load()) / phase.Seconds(),
		AttemptPPS:               float64(attempts.Load()) / phase.Seconds(),
		WriteCompletionPPS:       float64(written) / phase.Seconds(),
	}
	if written > 0 {
		result.NotReturnedRatio = float64(notReturned) / float64(written)
	}
	if at := lastScheduled.Load(); at != 0 {
		result.LastScheduledOffset = time.Duration(at)
	}
	if at := lastWriteStart.Load(); at != 0 {
		result.LastWriteStartOffset = time.Duration(at)
	}
	if at := lastWriteEnd.Load(); at != 0 {
		result.LastWriteEndOffset = time.Duration(at)
	}
	if jitterN > 0 {
		result.Jitter = jitterSum / time.Duration(jitterN)
	}
	if errText, ok := firstErr.Load().(string); ok && errText != "" {
		result.Errors = append(result.Errors, errText)
	}
	result.Connections = 1
	return result, nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

const udpHeaderSize = 20

// udpMagic starts every load-generator datagram ("PLB1").
const udpMagic uint32 = 0x504c4231

// socksUDPPayload strips the SOCKS5 UDP response header. The relay wraps the
// echo in RSV/FRAG/ATYP/address/port; the old reader counted that header as
// payload and accepted any datagram of three bytes or more.
func socksUDPPayload(packet []byte) []byte {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return nil
	}
	offset := 4
	switch packet[3] {
	case 0x01:
		offset += 4
	case 0x04:
		offset += 16
	case 0x03:
		if len(packet) < 5 {
			return nil
		}
		offset = 5 + int(packet[4])
	default:
		return nil
	}
	offset += 2
	if offset > len(packet) {
		return nil
	}
	return packet[offset:]
}

// parseUDPHeader reads magic, epoch, sequence and the sender's monotonic
// offset (nanoseconds from phase start, not a wall-clock timestamp).
func parseUDPHeader(payload []byte) (magic, epoch, sequence uint32, sentNs uint64, ok bool) {
	if len(payload) < udpHeaderSize {
		return 0, 0, 0, 0, false
	}
	return binary.BigEndian.Uint32(payload[0:4]),
		binary.BigEndian.Uint32(payload[4:8]),
		binary.BigEndian.Uint32(payload[8:12]),
		binary.BigEndian.Uint64(payload[12:20]),
		true
}

// udpSeqWindow attributes a datagram to first-arrival, duplicate or late-order
// without unbounded memory. Once the cap is reached the window reports
// exact=false instead of guessing: a caller must count that datagram as
// unattributed, never as unique, and must stop claiming an exact loss figure.
type udpSeqWindow struct {
	seen      map[uint32]struct{}
	maxSeen   uint32
	truncated bool
	limit     int
}

const udpSeqWindowLimit = 1 << 20

func newUDPSeqWindow() *udpSeqWindow {
	return &udpSeqWindow{seen: make(map[uint32]struct{}, 1024), limit: udpSeqWindowLimit}
}

// add reports whether the sequence is the first arrival and whether the window
// could attribute it at all. exact=false means the window is full.
func (w *udpSeqWindow) add(sequence uint32) (first bool, exact bool) {
	if _, found := w.seen[sequence]; found {
		return false, true
	}
	if w.limit <= 0 {
		w.limit = udpSeqWindowLimit
	}
	if len(w.seen) >= w.limit {
		w.truncated = true
		return false, false
	}
	w.seen[sequence] = struct{}{}
	if sequence > w.maxSeen {
		w.maxSeen = sequence
	}
	return true, true
}

// drainStateFor is the observation state machine. observation_complete says the
// configured window ran to its end with a live context; tail_active says the last
// reply landed inside the guard, so the quiet period is not long enough to call
// the loss estimable. The two are independent: a full window with an active tail
// is still a complete observation, just not a loss-estimable one.
func drainStateFor(ctxErr error, observation time.Duration, quiet time.Duration, sawReply bool, guard time.Duration) (status string, observationComplete bool, tailActive bool) {
	if ctxErr != nil {
		return "canceled", false, false
	}
	if observation <= 0 {
		return "zero_window", false, false
	}
	if sawReply && quiet < guard {
		return "window_elapsed_tail_active", true, true
	}
	return "complete_quiet", true, false
}

// runPacer drives send at a fixed offered rate on an absolute schedule. It
// never bursts to catch up: a slot missed by the scheduler is counted as
// skipped, not sent late. The schedule stops at deadline or when ctx is done.
func runPacer(ctx context.Context, start, deadline time.Time, rate int, send func()) (out struct {
	scheduled int64
	skipped   int64
	aborted   bool
}) {
	interval := time.Second / time.Duration(rate)
	if interval <= 0 {
		interval = time.Microsecond
	}
	next := start
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			out.aborted = true
			return out
		}
		now := time.Now()
		if !now.Before(deadline) {
			return out
		}
		if now.Before(next) {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			wait := next.Sub(now)
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				out.aborted = true
				return out
			case <-timer.C:
			}
			continue
		}
		if lag := now.Sub(next); lag >= interval {
			missed := int64(lag / interval)
			out.skipped += missed
			out.scheduled += missed
			next = next.Add(time.Duration(missed) * interval)
		}
		out.scheduled++
		send()
		next = next.Add(interval)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
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
