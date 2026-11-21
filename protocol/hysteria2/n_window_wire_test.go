package hysteria2

// What the QUIC receive-window policy does to the connection that is actually dialled.
//
// The existing quicwindow_test.go walks the same two steps the pinned client walks and asserts the
// resulting *quic.Config, which is the right place for the precedence table: it can enumerate rows
// that a live handshake cannot (a policy whose grants are larger than its ceilings, for instance).
// What it cannot show is that the numbers it computes are the numbers the PEER sees, because the
// values that matter are the ones advertised in the handshake - and quic-go rewrites those after
// every setting the caller wrote (populateConfig, and the ChromeParrot branch inside it).
//
// So this file measures from the other end of the wire. The loopback server pushes bytes into the
// stream while the client's application does not read; the sender can only get as far as the
// receiver's advertised initial window allows, so "how many bytes the server could hand to
// quic.Stream.Write before it blocked" is the advertised window, observed where it is enforced. The
// same flow then drains the whole payload back through the client, which is the byte-integrity half:
// a window that is bounded is only acceptable if nothing is lost or reordered by the bound.

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/quicpolicy"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/byteformats"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const (
	windowProbeChunk       = 32 << 10
	windowProbePushTotal   = 8 << 20
	windowProbeStallWindow = 700 * time.Millisecond
	windowProbeDeadline    = 30 * time.Second
)

// windowProbePayload is the deterministic byte stream the server pushes: every byte is a function
// of its offset, so the client can verify content and order without holding a second copy.
func windowProbePayload(offset int64) byte {
	return byte(offset*131 + 7)
}

// pushProbeHandler pushes windowProbePushTotal bytes into the first accepted stream and records how
// many of them quic.Stream accepted. It never reads from the stream, so the only thing that can stop
// the push is the receiver's flow-control credit.
type pushProbeHandler struct {
	accepted atomic.Int64
	started  chan struct{}
	finished chan struct{}
}

func newPushProbeHandler() *pushProbeHandler {
	return &pushProbeHandler{
		started:  make(chan struct{}, 1),
		finished: make(chan struct{}),
	}
}

func (h *pushProbeHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		chunk := make([]byte, windowProbeChunk)
		var offset int64
		for offset < windowProbePushTotal {
			size := int64(len(chunk))
			if remaining := windowProbePushTotal - offset; remaining < size {
				size = remaining
			}
			for index := int64(0); index < size; index++ {
				chunk[index] = windowProbePayload(offset + index)
			}
			written, err := conn.Write(chunk[:size])
			h.accepted.Add(int64(written))
			offset += int64(written)
			if err != nil {
				return
			}
			if written == 0 {
				return
			}
		}
		close(h.finished)
	}()
}

func (h *pushProbeHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	conn.Close()
}

// memoryBytesFrom parses a byte-size literal the way a configuration does.
func memoryBytesFrom(t *testing.T, literal string) *byteformats.MemoryBytes {
	t.Helper()
	var memory byteformats.MemoryBytes
	require.NoError(t, memory.UnmarshalJSON([]byte(literal)))
	return &memory
}

// windowProbeOptions is the outbound one measurement dials with.
type windowProbeOptions struct {
	windows             *quicpolicy.ReceiveWindows
	disableChromeParrot bool
	streamWindow        string
	connectionWindow    string
}

// measureAdvertisedWindow dials a loopback Hysteria2 server under the given windows, waits until
// the server's push stops making progress, and returns the number of bytes that had been accepted
// by then. It then drains the entire push through the client and verifies every byte, so the
// measurement is taken on a flow that is proven complete afterwards.
func measureAdvertisedWindow(t *testing.T, probe windowProbeOptions) int64 {
	t.Helper()

	handler := newPushProbeHandler()
	port := startHysteriaCarrierServer(t, handler)

	ctx := context.Background()
	if probe.windows != nil {
		ctx = quicpolicy.NewContext(ctx, *probe.windows)
	}
	options := option.Hysteria2OutboundOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
		Password:      hysteriaCarrierPassword,
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
		},
		DisableChromeParrot: probe.disableChromeParrot,
	}
	if probe.streamWindow != "" {
		options.StreamReceiveWindow = memoryBytesFrom(t, probe.streamWindow)
	}
	if probe.connectionWindow != "" {
		options.ConnectionReceiveWindow = memoryBytesFrom(t, probe.connectionWindow)
	}

	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("window-probe"), "window-probe", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	t.Cleanup(func() { outboundInstance.client.CloseWithError(net.ErrClosed) })

	dialCtx, cancel := context.WithTimeout(context.Background(), hysteriaCarrierSettle)
	defer cancel()
	conn, err := outboundInstance.DialContext(dialCtx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// Hysteria2 carries the TCP request lazily: the server routes the stream only once the client has
	// written something. One byte is enough, and it does not disturb the direction being measured.
	_, err = conn.Write([]byte{'x'})
	require.NoError(t, err)

	select {
	case <-handler.started:
	case <-time.After(hysteriaCarrierSettle):
		t.Fatal("the server never accepted a stream")
	}

	deadline := time.Now().Add(windowProbeDeadline)
	last := int64(-1)
	lastChange := time.Now()
	for {
		current := handler.accepted.Load()
		if current != last {
			last = current
			lastChange = time.Now()
		}
		if last > 0 && time.Since(lastChange) > windowProbeStallWindow {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never stopped making progress (accepted %d bytes); the push may not be flow-controlled", last)
		}
		time.Sleep(20 * time.Millisecond)
	}
	blockedAt := last

	// The bound is only acceptable if the flow is intact: read the whole push back and verify byte
	// for byte. `finished` must also close, which proves the blocked writes resumed.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(windowProbeDeadline)))
	defer conn.SetReadDeadline(time.Time{})
	buffer := make([]byte, windowProbeChunk)
	var offset int64
	for offset < windowProbePushTotal {
		size := int64(len(buffer))
		if remaining := windowProbePushTotal - offset; remaining < size {
			size = remaining
		}
		read, readErr := io.ReadFull(conn, buffer[:size])
		require.NoError(t, readErr, "reading the push at offset %d", offset)
		for index := int64(0); index < int64(read); index++ {
			require.Equal(t, windowProbePayload(offset+index), buffer[index],
				"byte %d of the pushed stream is wrong", offset+index)
		}
		offset += int64(read)
	}
	select {
	case <-handler.finished:
	case <-time.After(hysteriaCarrierSettle):
		t.Fatal("the server never finished pushing after the client drained the stream")
	}
	return blockedAt
}

// TestReceiveWindowGrantsAreTheWindowsThePeerSees is the P14 collision: the four rows that decide
// whose windows reach the handshake, measured on the wire instead of in the options struct.
//
//   - the Network Extension policy with no fingerprint lowers the advertised stream window to the
//     policy's 1 MiB (from the hysteria default of 8 MiB, which is what the row above it shows);
//   - with the fingerprint ON the SAME policy must not move it, because quic-go pins Chrome's own
//     transport parameters after every caller setting;
//   - an operator's explicit stream_receive_window wins over the policy when the handshake is the
//     protocol's own, and is itself replaced by the fingerprint when it is not.
//
// The ranges are wide because the number is "the sender's credit plus whatever the stack buffers";
// the rows are chosen so that no range overlaps its neighbour, which is what makes the assertion
// distinguish them rather than merely bound them.
func TestReceiveWindowGrantsAreTheWindowsThePeerSees(t *testing.T) {
	policies := quicpolicy.DefaultNetworkExtensionReceiveWindows()

	for _, testCase := range []struct {
		name      string
		probe     windowProbeOptions
		low, high int64
	}{
		{
			name:  "hysteria default: no policy, no explicit window, fingerprint on",
			probe: windowProbeOptions{},
			// 8 MiB initial stream window, raised by ChromeParrot to its 6 MiB and then to at least
			// the auto-tuning ceiling, plus the connection window's slack.
			low: 4 << 20, high: 9 << 20,
		},
		{
			name:  "policy with the protocol's own handshake: the grant reaches the peer",
			probe: windowProbeOptions{windows: &policies, disableChromeParrot: true},
			low:   512 << 10, high: 1536 << 10,
		},
		{
			name:  "the same policy with the fingerprint on: the fingerprint keeps the windows",
			probe: windowProbeOptions{windows: &policies},
			low:   4 << 20, high: 9 << 20,
		},
		{
			name: "operator's explicit window wins over the policy",
			probe: windowProbeOptions{
				windows:             &policies,
				streamWindow:        "3145728",
				connectionWindow:    "4194304",
				disableChromeParrot: true,
			},
			// The operator asked for 3 MiB, the policy would have granted 1 MiB.
			low: 2 << 20, high: 4 << 20,
		},
		{
			name: "the fingerprint replaces the operator's explicit window too",
			probe: windowProbeOptions{
				windows:          &policies,
				streamWindow:     "3145728",
				connectionWindow: "4194304",
			},
			// ChromeParrot on (the default) rewrites the advertised initial window to its own 6 MiB
			// after ApplyQUICOptions has written the operator's 3 MiB, which is why this row is not
			// 3 MiB: the fingerprint is the last writer by design.
			low: 4 << 20, high: 9 << 20,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			blockedAt := measureAdvertisedWindow(t, testCase.probe)
			t.Logf("the peer could push %d bytes before the stream blocked", blockedAt)
			require.GreaterOrEqual(t, blockedAt, testCase.low,
				"the sender got less credit than any row of this table allows")
			require.LessOrEqual(t, blockedAt, testCase.high,
				"the sender got more credit than any row of this table allows")
			require.Less(t, blockedAt, int64(windowProbePushTotal),
				"the push completed without ever blocking, so no window was enforced")
		})
	}
}
