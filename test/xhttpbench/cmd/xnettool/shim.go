package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The impairment box.
//
// It sits on the path between a tunnel client and the reference server, and it
// exists because this project measured on a host with no Linux, no container
// runtime and no WSL distribution: `tc netem` is not available, and a Windows
// host without a packet-filter driver cannot drop a TCP segment without
// acknowledging it. So the impairments offered here are the ones that can be
// produced honestly at the application layer, and each is named for what it
// does:
//
//	-delay-ms     one-way delay per direction (the round trip is twice this)
//	-jitter-ms    uniform jitter added to each chunk's release time
//	-stall-prob   probability that a chunk is held for -stall-ms extra
//	-reset-prob   probability that a connection is aborted mid-life
//
// Delay is a delay LINE, not a sleep per write: the reader keeps reading while
// the writer releases each chunk at (arrival + delay), so a saturated path still
// runs at full speed and only the first byte pays the latency. A naive
// sleep-per-write would turn a high-RTT link into a stop-and-wait link and would
// report the shim's buffer size as the network's bandwidth.
//
// The queue between the two halves is bounded (shimQueueDepth chunks). That bound
// is the emulated bandwidth-delay product: 128 x 64 KiB = 8 MiB per direction,
// which is a normal BDP for a 100 ms path at 640 Mbit/s.
//
// LIMIT, stated rather than implied: this is not packet loss. Dropping a chunk
// here would corrupt the stream rather than trigger a retransmission, because the
// sender's ACKs are produced by this process's own kernel. A true light-loss cell
// needs netem or a filter driver and is BLOCKED_BY_ENVIRONMENT on this host; what
// the shim can produce is the *observable* effect of a bad path — extra latency,
// bursty stalls and aborted connections — and every result says which impairment
// was in force.

const (
	shimChunkSize  = 64 * 1024
	shimQueueDepth = 128
)

var shimBufferPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, shimChunkSize)
		return &buffer
	},
}

type shimOptions struct {
	listen       string
	forward      string
	delayMs      int
	jitterMs     int
	stallProb    float64
	stallMs      int
	resetProb    float64
	resetAfterMs int
	queueDepth   int
	report       string
}

type shimStats struct {
	connections atomic.Int64
	resets      atomic.Int64
	stalls      atomic.Int64
	bytesUp     atomic.Int64
	bytesDown   atomic.Int64
	errors      atomic.Int64
}

type shimChunk struct {
	buffer  *[]byte
	n       int
	release time.Time
}

func runShim(args []string) error {
	options := shimOptions{}
	flags := flag.NewFlagSet("shim", flag.ExitOnError)
	flags.StringVar(&options.listen, "listen", "127.0.0.1:9100", "listen address (the client points here)")
	flags.StringVar(&options.forward, "forward", "127.0.0.1:9000", "address to forward to (the reference server)")
	flags.IntVar(&options.delayMs, "delay-ms", 0, "one-way delay added to every chunk")
	flags.IntVar(&options.jitterMs, "jitter-ms", 0, "uniform jitter added to each chunk's release time")
	flags.Float64Var(&options.stallProb, "stall-prob", 0, "probability that a chunk is held for -stall-ms extra")
	flags.IntVar(&options.stallMs, "stall-ms", 0, "extra hold time for a stalled chunk")
	flags.Float64Var(&options.resetProb, "reset-prob", 0, "probability that a connection is aborted mid-life")
	flags.IntVar(&options.resetAfterMs, "reset-after-ms", 1500, "typical connection age before an injected reset")
	flags.IntVar(&options.queueDepth, "queue-depth", shimQueueDepth, "in-flight chunks per direction: the emulated bandwidth-delay product")
	flags.StringVar(&options.report, "report", "", "write a JSON counters snapshot here on SIGINT")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if options.queueDepth < 1 {
		options.queueDepth = 1
	}

	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "shim: %s -> %s delay=%dms jitter=%dms stall=%.4f/%dms reset=%.4f\n",
		listener.Addr(), options.forward, options.delayMs, options.jitterMs,
		options.stallProb, options.stallMs, options.resetProb)

	stats := &shimStats{}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		_ = listener.Close()
		if options.report != "" {
			_ = writeJSONFile(options.report, shimSnapshot(options, stats))
		}
		os.Exit(0)
	}()

	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			break
		}
		stats.connections.Add(1)
		go serveShim(conn, options, stats)
	}
	return nil
}

func shimSnapshot(options shimOptions, stats *shimStats) map[string]any {
	return map[string]any{
		"listen":          options.listen,
		"forward":         options.forward,
		"delay_ms":        options.delayMs,
		"jitter_ms":       options.jitterMs,
		"stall_prob":      options.stallProb,
		"stall_ms":        options.stallMs,
		"reset_prob":      options.resetProb,
		"queue_depth":     options.queueDepth,
		"queue_bytes":     options.queueDepth * shimChunkSize,
		"connections":     stats.connections.Load(),
		"resets_injected": stats.resets.Load(),
		"stalls_injected": stats.stalls.Load(),
		"bytes_up":        stats.bytesUp.Load(),
		"bytes_down":      stats.bytesDown.Load(),
		"errors":          stats.errors.Load(),
	}
}

func serveShim(client net.Conn, options shimOptions, stats *shimStats) {
	defer client.Close()
	upstream, err := net.DialTimeout("tcp", options.forward, 10*time.Second)
	if err != nil {
		stats.errors.Add(1)
		return
	}
	defer upstream.Close()

	var closeOnce sync.Once
	abort := func() {
		closeOnce.Do(func() {
			if tcp, ok := client.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			if tcp, ok := upstream.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	defer abort()

	if options.resetProb > 0 && rand.Float64() < options.resetProb {
		age := time.Duration(options.resetAfterMs) * time.Millisecond
		if age > 0 {
			age = age/2 + time.Duration(rand.Int63n(int64(age)+1))
		}
		time.AfterFunc(age, func() {
			stats.resets.Add(1)
			abort()
		})
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		pump(client, upstream, options, stats, &stats.bytesUp, true)
	}()
	go func() {
		defer waitGroup.Done()
		pump(upstream, client, options, stats, &stats.bytesDown, false)
	}()
	waitGroup.Wait()
}

// pump forwards one direction through a delay line. With no impairment
// configured it degrades to a plain pooled copy, so a control run measures the
// tunnel rather than the shim.
func pump(source net.Conn, destination net.Conn, options shimOptions, stats *shimStats, counter *atomic.Int64, allowStall bool) {
	queued := options.delayMs > 0 || options.jitterMs > 0 || (allowStall && options.stallProb > 0 && options.stallMs > 0)
	if !queued {
		pumpDirect(source, destination, stats, counter)
		return
	}

	queue := make(chan shimChunk, options.queueDepth)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for chunk := range queue {
			if wait := time.Until(chunk.release); wait > 0 {
				time.Sleep(wait)
			}
			_, writeErr := destination.Write((*chunk.buffer)[:chunk.n])
			shimBufferPool.Put(chunk.buffer)
			if writeErr != nil {
				return
			}
		}
	}()

	for {
		buffer := shimBufferPool.Get().(*[]byte)
		read, err := source.Read(*buffer)
		if read > 0 {
			release := time.Now().Add(time.Duration(options.delayMs) * time.Millisecond)
			if options.jitterMs > 0 {
				spread := int64(options.jitterMs)*2 + 1
				jitter := time.Duration(rand.Int63n(spread)-int64(options.jitterMs)) * time.Millisecond
				release = release.Add(jitter)
			}
			if allowStall && options.stallProb > 0 && rand.Float64() < options.stallProb {
				stats.stalls.Add(1)
				release = release.Add(time.Duration(options.stallMs) * time.Millisecond)
			}
			select {
			case queue <- shimChunk{buffer: buffer, n: read, release: release}:
				counter.Add(int64(read))
			case <-writerDone:
				shimBufferPool.Put(buffer)
				return
			}
		} else {
			shimBufferPool.Put(buffer)
		}
		if err != nil {
			if err != io.EOF {
				stats.errors.Add(1)
			}
			break
		}
	}
	close(queue)
	<-writerDone
	if tcp, ok := destination.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func pumpDirect(source net.Conn, destination net.Conn, stats *shimStats, counter *atomic.Int64) {
	buffer := make([]byte, shimChunkSize)
	for {
		read, err := source.Read(buffer)
		if read > 0 {
			if _, writeErr := destination.Write(buffer[:read]); writeErr != nil {
				return
			}
			counter.Add(int64(read))
		}
		if err != nil {
			if err != io.EOF {
				stats.errors.Add(1)
			}
			break
		}
	}
	if tcp, ok := destination.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}
