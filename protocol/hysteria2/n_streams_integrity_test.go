package hysteria2

// Stream concurrency and byte integrity, under both handshake fingerprints.
//
// The window policy changes only flow-control credit, but the credit is what bounds a stream that is
// open concurrently with 39 others, so the two mechanisms have to be exercised together: a policy
// that lowered the window into an unworkable range, or a fingerprint branch that broke a stream's
// framing, would show up here as lost or crossed bytes rather than as a wrong configuration field.
//
// Every stream carries a payload derived from its own index, and the payload is verified byte for
// byte against what came back. That is the oracle: not "40 dials returned", but "every byte of every
// stream arrived, once, in order, on the stream it was written to".

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/quicpolicy"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const streamIntegrityPayload = 8 << 10

func streamIntegrityPayloadFor(index int) []byte {
	payload := make([]byte, streamIntegrityPayload)
	for position := range payload {
		payload[position] = byte(index*37 + position*11 + 3)
	}
	return payload
}

// TestConcurrentStreamsKeepByteIntegrity dials 1, 10 and 40 streams at once through one outbound -
// one QUIC connection, many streams - under ChromeParrot on (the default) and off, with the Network
// Extension window policy installed so the policy and the concurrency meet in the same connection.
func TestConcurrentStreamsKeepByteIntegrity(t *testing.T) {
	echoTarget := startHysteriaCarrierEcho(t)
	policies := quicpolicy.DefaultNetworkExtensionReceiveWindows()

	for _, disableChromeParrot := range []bool{false, true} {
		for _, streams := range []int{1, 10, 40} {
			name := fmt.Sprintf("streams=%d/chromeParrot=%v", streams, !disableChromeParrot)
			t.Run(name, func(t *testing.T) {
				handler := &hysteriaCarrierHandler{target: echoTarget}
				port := startHysteriaCarrierServer(t, handler)

				ctx := quicpolicy.NewContext(context.Background(), policies)
				options := option.Hysteria2OutboundOptions{
					ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
					Password:      hysteriaCarrierPassword,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
					},
					DisableChromeParrot: disableChromeParrot,
				}
				created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("streams"), "streams", options)
				require.NoError(t, err)
				outboundInstance := created.(*Outbound)
				t.Cleanup(func() { outboundInstance.client.CloseWithError(net.ErrClosed) })

				var waitGroup sync.WaitGroup
				failures := make(chan error, streams)
				connections := make([]net.Conn, streams)
				for index := 0; index < streams; index++ {
					waitGroup.Add(1)
					go func(index int) {
						defer waitGroup.Done()
						dialCtx, cancel := context.WithTimeout(context.Background(), hysteriaCarrierSettle)
						defer cancel()
						conn, dialErr := outboundInstance.DialContext(dialCtx, N.NetworkTCP,
							M.ParseSocksaddr(fmt.Sprintf("stream-%d.test:443", index)))
						if dialErr != nil {
							failures <- fmt.Errorf("stream %d dial: %w", index, dialErr)
							return
						}
						connections[index] = conn
						payload := streamIntegrityPayloadFor(index)
						_ = conn.SetDeadline(time.Now().Add(hysteriaCarrierSettle))
						if _, writeErr := conn.Write(payload); writeErr != nil {
							failures <- fmt.Errorf("stream %d write: %w", index, writeErr)
							return
						}
						echoed := make([]byte, len(payload))
						if _, readErr := io.ReadFull(conn, echoed); readErr != nil {
							failures <- fmt.Errorf("stream %d read: %w", index, readErr)
							return
						}
						for position := range payload {
							if echoed[position] != payload[position] {
								failures <- fmt.Errorf("stream %d byte %d = %d, want %d (bytes crossed between streams or were reordered)",
									index, position, echoed[position], payload[position])
								return
							}
						}
					}(index)
				}
				waitGroup.Wait()
				for index := 0; index < streams; index++ {
					if connections[index] != nil {
						_ = connections[index].Close()
					}
				}
				close(failures)
				for failure := range failures {
					t.Error(failure)
				}
				require.EqualValues(t, streams, handler.streams.Load(),
					"every stream must reach the server exactly once")
			})
		}
	}
}
