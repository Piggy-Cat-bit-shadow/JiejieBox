package loadgen

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

// Probe is a functional readiness check: it proves the whole chain (client
// inbound, protocol, server, target) moves a byte, not merely that a port is
// open. A port that accepts and then drops every request is the failure mode a
// pure dial check hides.
func Probe(proxyAddr, targetAddr string, kind target.Kind) error {
	pattern := Upload
	switch kind {
	case target.Sink:
		pattern = Upload
	case target.Source:
		pattern = Download
	case target.Echo:
		pattern = PingPong
	case target.Duplex:
		pattern = Duplex
	case target.UDPEcho:
		pattern = UDP
	default:
		return fmt.Errorf("unknown target kind %q", kind)
	}
	cfg := Config{
		ProxyAddr:     proxyAddr,
		TargetAddr:    targetAddr,
		Pattern:       pattern,
		Streams:       1,
		Duration:      400 * time.Millisecond,
		ChunkSize:     16 * 1024,
		PingPongBytes: 64,
		PacketSize:    64,
		Verify:        true,
		IOGrace:       500 * time.Millisecond,
	}
	ctx := context.Background()
	result, err := Run(ctx, cfg)
	if err != nil {
		// A short probe legitimately ends with a closed stream; only an empty
		// result is a failure.
		if result.PayloadBytes == 0 && len(result.Latencies) == 0 && result.DatagramsReceived == 0 {
			return err
		}
		return nil
	}
	if result.PayloadBytes == 0 && len(result.Latencies) == 0 && result.DatagramsReceived == 0 {
		return errors.New("probe moved no bytes")
	}
	if result.MismatchedBytes > 0 {
		return fmt.Errorf("probe payload was corrupted: %d bytes", result.MismatchedBytes)
	}
	return nil
}
