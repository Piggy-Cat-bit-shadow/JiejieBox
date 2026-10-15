package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The load generator.
//
// It drives one shaped workload through a proxied SOCKS5 inbound (or directly,
// for control runs) and prints a single JSON result object on stdout. Four
// properties are deliberate:
//
//  1. A shared start barrier. Every worker connects and warms up first, then
//     all of them are released at once, so aggregate throughput is not diluted
//     by per-connection startup skew, and the phase is a single well-defined
//     window rather than "roughly when the first worker started".
//  2. Warmup is discarded. The first proxied connection of a run pays a
//     handshake, a certificate exchange and a pool fill; charging that to the
//     steady state is how a benchmark reports a transport's startup cost as
//     its throughput.
//  3. CPU is charged to the processes that paid it. The PIDs of the tunnel
//     client and of the reference server are sampled around the measured phase
//     only, so cpu_sec and the derived per-byte cost exclude process startup
//     and teardown.
//  4. Workers report over a channel, never by writing into a slice the
//     aggregator is reading. A benchmark that samples a data race reports
//     numbers that cannot be reproduced.
//
// Phase markers ("#phase start" / "#phase end") go to stderr so an orchestrator
// can line up external observations without trusting wall-clock alignment.

type loadGenOptions struct {
	pattern     string
	target      string
	proxy       string
	concurrency int
	totalBytes  int64
	durationMs  int
	iterations  int
	payload     int
	quantum     int
	warmupMs    int
	timeoutMs   int
	verify      bool
	label       string
	report      string
	cpuPIDs     pidList
}

// warmupTransferBytes is how much data each worker moves before the barrier for
// a throughput pattern: enough to force the tunnel to establish, the XHTTP pool
// to fill and TCP to leave slow start.
const warmupTransferBytes = 2 << 20

// connectWarmupIterations are connection-setup iterations performed before the
// barrier. The connect pattern measures connection setup, so its warmup is
// iterations of the same thing rather than a transfer that would hide it.
const connectWarmupIterations = 3

type loadGenReport struct {
	Label          string                   `json:"label,omitempty"`
	Pattern        string                   `json:"pattern"`
	Target         string                   `json:"target"`
	Proxy          string                   `json:"proxy,omitempty"`
	Concurrency    int                      `json:"concurrency"`
	PayloadBytes   int                      `json:"payload_bytes,omitempty"`
	Iterations     int                      `json:"iterations,omitempty"`
	DurationMs     float64                  `json:"duration_ms"`
	BytesIn        int64                    `json:"bytes_in"`
	BytesOut       int64                    `json:"bytes_out"`
	TotalBytes     int64                    `json:"total_bytes"`
	MBytesPerSec   float64                  `json:"mbytes_per_sec"`
	MBitsPerSec    float64                  `json:"mbits_per_sec"`
	Ops            int64                    `json:"ops"`
	OpsPerSec      float64                  `json:"ops_per_sec"`
	Mismatches     int64                    `json:"mismatches"`
	Latency        *latencySummary          `json:"latency_us,omitempty"`
	ProcessCPU     map[string]processSample `json:"process_cpu,omitempty"`
	CPUSeconds     float64                  `json:"cpu_sec,omitempty"`
	MicroSecPerMB  float64                  `json:"us_per_mbyte,omitempty"`
	MicroSecPerOp  float64                  `json:"us_per_op,omitempty"`
	CoreFraction   float64                  `json:"core_fraction,omitempty"`
	Errors         []string                 `json:"errors,omitempty"`
	PhaseStartUnix int64                    `json:"phase_start_unix_nano"`
	PhaseEndUnix   int64                    `json:"phase_end_unix_nano"`
}

func runLoadGen(args []string) error {
	options := loadGenOptions{}
	flags := flag.NewFlagSet("loadgen", flag.ExitOnError)
	flags.StringVar(&options.pattern, "pattern", "download", "download | upload | duplex | pingpong | connect")
	flags.StringVar(&options.target, "target", "127.0.0.1:9200", "target address (the destination behind the tunnel)")
	flags.StringVar(&options.proxy, "proxy", "", "SOCKS5 inbound to dial through; empty means direct")
	flags.IntVar(&options.concurrency, "concurrency", 1, "parallel proxied connections")
	flags.Int64Var(&options.totalBytes, "total-bytes", 64<<20, "total bytes to move, split across connections (throughput patterns)")
	flags.IntVar(&options.durationMs, "duration-ms", 0, "if > 0, run for this long instead of a fixed byte count")
	flags.IntVar(&options.iterations, "iterations", 200, "iterations per connection (pingpong, connect)")
	flags.IntVar(&options.payload, "payload", 512, "bytes per request (pingpong)")
	flags.IntVar(&options.quantum, "quantum", 4<<20, "bytes per request in duration mode")
	flags.IntVar(&options.warmupMs, "warmup-ms", 1000, "warmup before the measured phase; discarded")
	flags.IntVar(&options.timeoutMs, "timeout-ms", 120000, "per-operation timeout")
	flags.BoolVar(&options.verify, "verify", true, "verify every received byte against the pattern")
	flags.StringVar(&options.label, "label", "", "label echoed into the result")
	flags.StringVar(&options.report, "report", "", "also write the JSON result here")
	flags.Var(&options.cpuPIDs, "cpu-pid", "process to charge CPU to (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if options.concurrency < 1 {
		return fmt.Errorf("loadgen: concurrency must be >= 1")
	}
	if options.durationMs <= 0 && options.totalBytes <= 0 && options.pattern != "pingpong" && options.pattern != "connect" {
		return fmt.Errorf("loadgen: pattern %s needs -total-bytes or -duration-ms", options.pattern)
	}

	report := loadGenReport{
		Label:       options.label,
		Pattern:     options.pattern,
		Target:      options.target,
		Proxy:       options.proxy,
		Concurrency: options.concurrency,
	}
	if options.pattern == "pingpong" {
		report.PayloadBytes = options.payload
		report.Iterations = options.iterations
	}
	if options.pattern == "connect" {
		report.Iterations = options.iterations
	}

	// Phase one: connect and warm up, then hold at the barrier.
	ready := sync.WaitGroup{}
	start := make(chan struct{})
	outcomes := make(chan workerResult, options.concurrency)
	for worker := 0; worker < options.concurrency; worker++ {
		ready.Add(1)
		go func(worker int) {
			prepared := prepareWorker(options, worker)
			ready.Done()
			<-start
			outcomes <- measureWorker(options, prepared)
		}(worker)
	}
	ready.Wait()

	// Phase two: one measured window for every worker.
	tracker := newCPUTracker(options.cpuPIDs)
	phaseStart := time.Now()
	fmt.Fprintln(os.Stderr, "#phase start")
	close(start)
	results := make([]workerResult, 0, options.concurrency)
	for i := 0; i < options.concurrency; i++ {
		results = append(results, <-outcomes)
	}
	phaseEnd := time.Now()
	processCPU, _ := tracker.finish()
	elapsed := phaseEnd.Sub(phaseStart).Seconds()
	fmt.Fprintln(os.Stderr, "#phase end")

	var bytesIn, bytesOut, ops, mismatches int64
	var latencies []float64
	errorCounts := make(map[string]int)
	for _, result := range results {
		bytesIn += result.bytesIn
		bytesOut += result.bytesOut
		ops += result.ops
		mismatches += result.mismatches
		latencies = append(latencies, result.latencies...)
		if result.err != nil {
			errorCounts[result.err.Error()]++
		}
	}
	for message, count := range errorCounts {
		report.Errors = append(report.Errors, fmt.Sprintf("%s (x%d)", message, count))
	}

	report.DurationMs = round3(elapsed * 1000)
	report.BytesIn = bytesIn
	report.BytesOut = bytesOut
	report.TotalBytes = bytesIn + bytesOut
	report.Ops = ops
	report.Mismatches = mismatches
	if elapsed > 0 {
		report.MBytesPerSec = round3(float64(report.TotalBytes) / 1e6 / elapsed)
		report.MBitsPerSec = round3(float64(report.TotalBytes) * 8 / 1e6 / elapsed)
		report.OpsPerSec = round3(float64(ops) / elapsed)
	}
	report.Latency = summarizeLatencies(latencies)
	report.ProcessCPU = processCPU
	for _, sample := range processCPU {
		report.CPUSeconds += sample.CPUSeconds
	}
	if report.TotalBytes > 0 {
		report.MicroSecPerMB = round3(report.CPUSeconds * 1e6 / (float64(report.TotalBytes) / 1e6))
	}
	if report.Ops > 0 {
		report.MicroSecPerOp = round3(report.CPUSeconds * 1e6 / float64(report.Ops))
	}
	if elapsed > 0 {
		report.CoreFraction = round3(report.CPUSeconds / elapsed)
	}
	report.PhaseStartUnix = phaseStart.UnixNano()
	report.PhaseEndUnix = phaseEnd.UnixNano()

	encoded, err := jsonMarshal(report)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if options.report != "" {
		if err = writeJSONFile(options.report, report); err != nil {
			return err
		}
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("loadgen: %s", strings.Join(report.Errors, "; "))
	}
	return nil
}

type workerResult struct {
	conn       net.Conn
	bytesIn    int64
	bytesOut   int64
	ops        int64
	mismatches int64
	latencies  []float64
	err        error
}

// prepareWorker opens the proxied connection and moves the warmup traffic, then
// resets the counters so nothing measured before the barrier is reported.
func prepareWorker(options loadGenOptions, worker int) workerResult {
	timeout := time.Duration(options.timeoutMs) * time.Millisecond
	if options.pattern == "connect" {
		warm := workerResult{}
		runConnect(options, timeout, connectWarmupIterations, &warm)
		return workerResult{err: warm.err}
	}
	conn, err := dialForLoadGen(options, timeout)
	if err != nil {
		return workerResult{err: err}
	}
	scratch := workerResult{conn: conn}
	warmupErr := error(nil)
	if options.warmupMs > 0 {
		switch options.pattern {
		case "download", "upload", "duplex":
			warmupErr = throughputTransfer(options, conn, warmupTransferBytes, make([]byte, 256*1024), timeout, &scratch)
		case "pingpong":
			warmupErr = pingPong(conn, options.payload, warmUpIterations(options), options.verify, timeout, &scratch)
		default:
			warmupErr = fmt.Errorf("unknown pattern %q", options.pattern)
		}
	}
	return workerResult{conn: conn, err: warmupErr}
}

func measureWorker(options loadGenOptions, prepared workerResult) workerResult {
	timeout := time.Duration(options.timeoutMs) * time.Millisecond
	if prepared.err != nil {
		if prepared.conn != nil {
			_ = prepared.conn.Close()
		}
		return prepared
	}
	result := workerResult{}
	if options.pattern == "connect" {
		runConnect(options, timeout, options.iterations, &result)
		return result
	}
	if prepared.conn == nil {
		result.err = fmt.Errorf("warmup did not produce a connection")
		return result
	}
	defer prepared.conn.Close()

	switch options.pattern {
	case "download", "upload", "duplex":
		runThroughput(options, prepared.conn, timeout, &result)
	case "pingpong":
		if err := pingPong(prepared.conn, options.payload, options.iterations, options.verify, timeout, &result); err != nil {
			result.err = err
		}
	default:
		result.err = fmt.Errorf("unknown pattern %q", options.pattern)
	}
	return result
}

func warmUpIterations(options loadGenOptions) int {
	iterations := options.warmupMs / 5
	if iterations < 3 {
		iterations = 3
	}
	if iterations > 200 {
		iterations = 200
	}
	return iterations
}

func dialForLoadGen(options loadGenOptions, timeout time.Duration) (net.Conn, error) {
	if options.proxy != "" {
		return dialThroughSOCKS(options.proxy, options.target, timeout)
	}
	return net.DialTimeout("tcp", options.target, timeout)
}

func runThroughput(options loadGenOptions, conn net.Conn, timeout time.Duration, result *workerResult) {
	deadlineMode := options.durationMs > 0
	end := time.Now().Add(time.Duration(options.durationMs) * time.Millisecond)
	quantum := int64(options.quantum)
	if quantum <= 0 {
		quantum = 4 << 20
	}
	if !deadlineMode {
		quantum = options.totalBytes
	}
	buffer := make([]byte, 256*1024)
	for {
		if deadlineMode {
			if !time.Now().Before(end) {
				return
			}
		} else if quantum <= 0 {
			return
		}
		if err := throughputTransfer(options, conn, quantum, buffer, timeout, result); err != nil {
			result.err = err
			return
		}
		if !deadlineMode {
			return
		}
	}
}

// throughputTransfer performs one request/response of the chosen shape.
func throughputTransfer(options loadGenOptions, conn net.Conn, size int64, buffer []byte, timeout time.Duration, result *workerResult) error {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	switch options.pattern {
	case "download":
		if _, err := fmt.Fprintf(conn, "D %d\n", size); err != nil {
			return err
		}
		received, mismatches, err := readExact(conn, size, 0, options.verify, buffer)
		result.bytesIn += received
		result.mismatches += int64(mismatches)
		return err
	case "upload":
		if _, err := fmt.Fprintf(conn, "U %d\n", size); err != nil {
			return err
		}
		written, err := writePattern(conn, size, buffer)
		result.bytesOut += written
		if err != nil {
			return err
		}
		return readUploadAck(conn, size, result)
	case "duplex":
		if _, err := fmt.Fprintf(conn, "X %d %d\n", size, size); err != nil {
			return err
		}
		var waitGroup sync.WaitGroup
		var uploadErr, downloadErr error
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			written, err := writePattern(conn, size, make([]byte, 256*1024))
			result.bytesOut += written
			uploadErr = err
		}()
		received, mismatches, err := readExact(conn, size, 0, options.verify, buffer)
		result.bytesIn += received
		result.mismatches += int64(mismatches)
		downloadErr = err
		waitGroup.Wait()
		if uploadErr != nil {
			return uploadErr
		}
		if downloadErr != nil {
			return downloadErr
		}
		return readUploadAck(conn, size, result)
	default:
		return fmt.Errorf("throughputTransfer: unknown pattern %q", options.pattern)
	}
}

// readExact reads `size` bytes and, when verify is set, compares them with the
// pattern at `baseOffset + received`. The base offset is explicit because the
// echo pattern carries the offset the client wrote at: a verifier that assumed
// every response starts at offset 0 would report a mismatch on every byte of a
// correct echo, which is the loudest possible way to learn this.
func readExact(conn net.Conn, size int64, baseOffset uint64, verify bool, buffer []byte) (int64, int, error) {
	var received int64
	mismatches := 0
	for received < size {
		chunk := int64(len(buffer))
		if remaining := size - received; remaining < chunk {
			chunk = remaining
		}
		read, err := io.ReadFull(conn, buffer[:chunk])
		if verify && read > 0 {
			mismatches += patternVerify(buffer[:read], baseOffset+uint64(received))
		}
		received += int64(read)
		if err != nil {
			return received, mismatches, err
		}
	}
	return received, mismatches, nil
}

func writePattern(conn net.Conn, size int64, buffer []byte) (int64, error) {
	var written int64
	for written < size {
		chunk := int64(len(buffer))
		if remaining := size - written; remaining < chunk {
			chunk = remaining
		}
		patternFill(buffer[:chunk], uint64(written))
		n, err := conn.Write(buffer[:chunk])
		written += int64(n)
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func readUploadAck(conn net.Conn, size int64, result *workerResult) error {
	reader := bufio.NewReaderSize(conn, 4096)
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 3 || fields[0] != "OK" {
		return fmt.Errorf("target answered %q", strings.TrimSpace(line))
	}
	mismatches, parseErr := strconv.ParseInt(fields[1], 10, 64)
	if parseErr != nil {
		return fmt.Errorf("target answered %q", strings.TrimSpace(line))
	}
	acknowledged, parseErr := strconv.ParseInt(fields[2], 10, 64)
	if parseErr != nil || acknowledged != size {
		return fmt.Errorf("target acknowledged %s of %d bytes", fields[2], size)
	}
	result.mismatches += mismatches
	return nil
}

// pingPong issues one request and waits for its answer, which is the shape that
// makes packet-up pay for a fresh HTTP request per write.
//
// It sets a deadline per iteration, like throughputTransfer does per transfer.
// Without one this pattern could block forever: dialForLoadGen's SOCKS handshake
// CLEARS the connection deadline when it finishes (socks.go), so a response that
// never arrives left the read parked with no timeout at all. Measured the hard
// way: a 70-minute soak froze for 13 minutes on one such read, at 0% CPU, while
// the client was healthy (16 goroutines, no stuck transport goroutine, and an
// independent probe through the same client succeeded). The -timeout-ms option was
// silently a no-op for this pattern.
func pingPong(conn net.Conn, payload int, iterations int, verify bool, timeout time.Duration, result *workerResult) error {
	if _, err := fmt.Fprintf(conn, "P %d %d\n", payload, iterations); err != nil {
		return err
	}
	buffer := make([]byte, payload)
	for i := 0; i < iterations; i++ {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		patternFill(buffer, uint64(i*payload))
		started := nowMicros()
		if _, err := conn.Write(buffer); err != nil {
			return err
		}
		received, mismatches, err := readExact(conn, int64(payload), uint64(i*payload), verify, buffer)
		elapsed := nowMicros() - started
		result.bytesOut += int64(payload)
		result.bytesIn += received
		result.mismatches += int64(mismatches)
		if err != nil {
			return err
		}
		result.latencies = append(result.latencies, elapsed)
		result.ops++
	}
	return nil
}

// runConnect measures connection setup: a new proxied tunnel, one byte down,
// then close. It is the metric XMUX reuse moves, and it uses a fresh connection
// on purpose.
func runConnect(options loadGenOptions, timeout time.Duration, iterations int, result *workerResult) {
	for i := 0; i < iterations; i++ {
		started := nowMicros()
		fresh, err := dialForLoadGen(options, timeout)
		if err != nil {
			result.err = err
			return
		}
		if err = fresh.SetDeadline(time.Now().Add(timeout)); err == nil {
			_, err = fmt.Fprintf(fresh, "D 1\n")
		}
		var one [1]byte
		if err == nil {
			_, err = io.ReadFull(fresh, one[:])
		}
		_ = fresh.Close()
		if err != nil {
			result.err = err
			return
		}
		if one[0] != patternBlock[0] {
			result.mismatches++
		}
		result.bytesIn++
		result.bytesOut += 4
		result.ops++
		result.latencies = append(result.latencies, nowMicros()-started)
	}
}
