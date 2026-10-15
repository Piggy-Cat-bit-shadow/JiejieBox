// Command xnettool is the measuring side of the XHTTP performance harness.
//
// It has three subcommands, and running them as SEPARATE processes is the point:
//
//	target  — the destination behind the tunnel: a deterministic pattern
//	          source, sink and echo, so a byte-exact verdict is possible
//	loadgen — the client that drives traffic through a proxied SOCKS5 inbound
//	shim    — an impairment box (delay, jitter, stalls, resets) placed on the
//	          path between a tunnel client and the reference server
//
// Three processes means the load generator's CPU is never charged to the tunnel
// client, the impairment box is not part of either endpoint, and a client that
// hangs can be killed without taking the measurement with it.
//
// Nothing here imports the fork under test. The load generator must keep
// measuring the same way after the transport is optimized, or the two runs are
// not comparable.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "target":
		err = runTarget(os.Args[2:])
	case "loadgen":
		err = runLoadGen(os.Args[2:])
	case "shim":
		err = runShim(os.Args[2:])
	case "udpshim":
		err = runUDPShim(os.Args[2:])
	case "procstat":
		err = runProcStat(os.Args[2:])
	case "clockprobe":
		err = runClockProbe(os.Args[2:])
	case "run":
		err = runBench(os.Args[2:])
	case "tlscover":
		err = runTLSCover(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "xnettool: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "xnettool %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `xnettool - XHTTP performance harness tool

  xnettool target  -listen 127.0.0.1:9200
      Deterministic pattern source/sink/echo. One TCP listener; the first line
      of each connection is the request:
        D <n>            send n pattern bytes from offset 0
        U <n>            read n bytes, verify, reply "OK <mismatches> <n>"
        P <k> <iters>    echo: iters times (read k, write those k back)

  xnettool loadgen [flags]
      Drive a pattern through a SOCKS5 inbound (or directly) and print one JSON
      result object on stdout.

  xnettool shim    -listen 127.0.0.1:9100 -forward 127.0.0.1:9000 [impairments]
      Delay/jitter/stall/reset box in front of the reference server.

  xnettool procstat -pid <pid> [-pid <pid>...]
      Print per-process CPU seconds and working set (Windows: kernel32 calls).
`)
}
