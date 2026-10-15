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
	"sync/atomic"
)

// The target: the destination behind the tunnel.
//
// It speaks a small line protocol so that one process covers every shape the
// harness needs to measure, and so that a proxied byte can be verified:
//
//	D <n>          download: write n pattern bytes from offset 0
//	U <n>          upload: read n bytes, verify, answer "OK <mismatches> <n>"
//	P <k> <iters>  ping-pong: iters times (read k bytes, write those k back)
//	X <up> <down>  duplex: write `down` bytes while concurrently reading `up`
//
// Requests are handled in a LOOP on one connection. That is deliberate: a
// proxied TCP connection in the real world carries many requests, and a target
// that closed after one would make every throughput measurement pay a fresh
// tunnel handshake — which is the cost the harness wants to measure separately,
// with the connect pattern, not by accident.
//
// The verbs map onto the three shapes that differ between XHTTP modes: bulk
// downlink (the download body), bulk uplink (POST bodies / the upload stream),
// and small alternating writes, where packet-up pays a request per write.
type targetStats struct {
	connections atomic.Int64
	requests    atomic.Int64
	mismatches  atomic.Int64
	errors      atomic.Int64
}

func runTarget(args []string) error {
	flags := flag.NewFlagSet("target", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:9200", "listen address")
	report := flags.String("report", "", "write a JSON counters snapshot here on exit")
	if err := flags.Parse(args); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "target: listening on %s\n", listener.Addr())

	stats := &targetStats{}
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if strings.Contains(acceptErr.Error(), "use of closed network connection") {
				break
			}
			stats.errors.Add(1)
			fmt.Fprintf(os.Stderr, "target: accept: %v\n", acceptErr)
			continue
		}
		stats.connections.Add(1)
		go serveTarget(conn, stats)
	}
	if *report != "" {
		return writeJSONFile(*report, map[string]any{
			"connections": stats.connections.Load(),
			"requests":    stats.requests.Load(),
			"mismatches":  stats.mismatches.Load(),
			"errors":      stats.errors.Load(),
		})
	}
	return nil
}

func serveTarget(conn net.Conn, stats *targetStats) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				stats.errors.Add(1)
			}
			return
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		stats.requests.Add(1)
		switch fields[0] {
		case "D":
			total, ok := parseCount(fields, 2)
			if !ok {
				stats.errors.Add(1)
				return
			}
			if err = targetDownload(conn, total); err != nil {
				stats.errors.Add(1)
				return
			}
		case "U":
			total, ok := parseCount(fields, 2)
			if !ok {
				stats.errors.Add(1)
				return
			}
			mismatches, readErr := targetUpload(reader, total)
			stats.mismatches.Add(int64(mismatches))
			if readErr != nil {
				stats.errors.Add(1)
				return
			}
			if _, err = fmt.Fprintf(conn, "OK %d %d\n", mismatches, total); err != nil {
				stats.errors.Add(1)
				return
			}
		case "P":
			if len(fields) != 3 {
				stats.errors.Add(1)
				return
			}
			payload, convErr := strconv.Atoi(fields[1])
			iterations, iterErr := strconv.Atoi(fields[2])
			if convErr != nil || payload <= 0 || iterErr != nil || iterations < 0 {
				stats.errors.Add(1)
				return
			}
			if err = targetPingPong(reader, conn, payload, iterations); err != nil {
				stats.errors.Add(1)
				return
			}
		case "X":
			if len(fields) != 3 {
				stats.errors.Add(1)
				return
			}
			uplink, convErr := strconv.ParseInt(fields[1], 10, 64)
			downlink, downErr := strconv.ParseInt(fields[2], 10, 64)
			if convErr != nil || downErr != nil || uplink < 0 || downlink < 0 {
				stats.errors.Add(1)
				return
			}
			mismatches, readErr, writeErr := targetDuplex(reader, conn, uplink, downlink)
			stats.mismatches.Add(int64(mismatches))
			if readErr != nil || writeErr != nil {
				stats.errors.Add(1)
				return
			}
			if _, err = fmt.Fprintf(conn, "OK %d %d\n", mismatches, uplink); err != nil {
				stats.errors.Add(1)
				return
			}
		default:
			stats.errors.Add(1)
			return
		}
	}
}

func parseCount(fields []string, expected int) (int64, bool) {
	if len(fields) != expected {
		return 0, false
	}
	value, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// targetDownload sends `total` pattern bytes starting at offset 0.
func targetDownload(conn net.Conn, total int64) error {
	buffer := make([]byte, 64*1024)
	var offset uint64
	for remaining := total; remaining > 0; {
		chunk := int64(len(buffer))
		if remaining < chunk {
			chunk = remaining
		}
		patternFill(buffer[:chunk], offset)
		if _, err := conn.Write(buffer[:chunk]); err != nil {
			return err
		}
		offset += uint64(chunk)
		remaining -= chunk
	}
	return nil
}

func targetUpload(reader *bufio.Reader, total int64) (int, error) {
	buffer := make([]byte, 64*1024)
	mismatches := 0
	var offset uint64
	for remaining := total; remaining > 0; {
		chunk := int64(len(buffer))
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := io.ReadFull(reader, buffer[:chunk]); err != nil {
			return mismatches, err
		}
		mismatches += patternVerify(buffer[:chunk], offset)
		offset += uint64(chunk)
		remaining -= chunk
	}
	return mismatches, nil
}

func targetPingPong(reader *bufio.Reader, conn net.Conn, payload int, iterations int) error {
	buffer := make([]byte, payload)
	for i := 0; i < iterations; i++ {
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return err
		}
		if _, err := conn.Write(buffer); err != nil {
			return err
		}
	}
	return nil
}

// targetDuplex writes the downlink while reading the uplink. The two directions
// run on separate goroutines because a real tunnel carries both at once, and a
// transport that serializes them should show it as a throughput loss rather
// than as a slow test.
func targetDuplex(reader *bufio.Reader, conn net.Conn, uplink int64, downlink int64) (int, error, error) {
	var waitGroup sync.WaitGroup
	var mismatches int
	var readErr error
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		mismatches, readErr = targetUpload(reader, uplink)
	}()
	writeErr := targetDownload(conn, downlink)
	waitGroup.Wait()
	return mismatches, readErr, writeErr
}
