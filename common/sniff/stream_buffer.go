package sniff

import (
	"sync/atomic"

	"github.com/sagernet/sing/common/buf"
)

// On-demand growth for the TCP sniff buffer.
//
// # The gap this closes
//
// route/route.go allocates the stream sniff buffer with buf.NewPacket() BEFORE PeekStream reads
// anything, so a connection that has not sent its first byte already holds the whole packet buffer:
// buf.UDPBufferSize, which is 16 KiB on an ordinary build and 8 KiB with with_low_memory (the Apple
// mobile profile). A flow that waits out the sniff timeout - a server-first peer, a slow client, a
// stalled handshake - holds that array for the whole wait and then releases it without having used
// most of it. The buffer is the same size whether the payload turns out to be 21 bytes (an SSH
// banner) or 16 KiB.
//
// # What this file does NOT change
//
// The CEILING is unchanged, and that is the point of passing it in rather than hard-coding one here:
// growth stops at buf.UDPBufferSize, the capacity the previous code allocated unconditionally, so a
// payload that fills it still ends the sniff exactly as before - ReadOnceFrom returns
// io.ErrShortBuffer, the retry loop stops, the aggregate of that last round is returned, and the
// caller hands the bytes it read to the connection. Nothing here raises the ceiling to the 64 KiB
// Hako uses, and no global buf constant is touched: UDP sniffs, QUIC sniffs and the relay copy
// buffer keep their sizes.
//
// # Why growth REPLACES the buffer object instead of resizing it
//
// sing's Buffer is fixed-capacity. It holds `data []byte` plus a logical `capacity int`; there is no
// Grow method, and ReadOnceFrom writes only into data[end:capacity] (common/buf/buffer.go:137-144
// and :335-341; the measured trajectory is recorded in samples/S-capacity.log). Capacity therefore
// cannot be raised in place, and a growth is a new buffer of the target size plus a copy of the
// payload already read. The new buffer is the OWNER from that moment on, which is why the peek
// functions return it: swapping the pointer inside the loop without returning it would leave
// route/route.go handing over the old, smaller buffer, and every byte read after the growth would be
// dropped from the payload the sniffer approved.
//
// # Cost, stated honestly
//
// A flow that grows pays one extra pooled allocation and one copy of at most the initial size. That
// is the trade: flows that never exceed the initial size (and flows that send nothing at all) hold
// the small array; flows that do exceed it end up at exactly the capacity they would have had
// anyway, one allocation later.

// StreamSniffInitialBufferSize is the capacity the stream sniff starts with.
//
// # Why 2 KiB
//
// It is the smallest value that covers every first payload the sniffer set actually sees, measured
// on this tree (probes/S/payload_sizes, recorded in samples/S-payload-sizes.log):
//
//	SSH banner                                    21 B
//	DNS over TCP query                            33 B
//	minimal HTTP request                          41 B
//	BitTorrent handshake                          68 B
//	browser-like HTTP request                    379 B
//	HTTP request with 1.4 KiB of cookies         846 B
//	TLS ClientHello, Go 1.27 default (PQ)       1513 B
//	TLS ClientHello, longest legal single label 1573 B
//
// 1 KiB would grow on every modern TLS flow - the dominant shape - which turns the common path into
// pay-an-extra-allocation-instead-of-saving; 4 KiB covers the same payloads but gives up 2 KiB of
// the wait-state saving for no additional coverage. 2 KiB covers the measured maximum (1573 B, 475 B
// of headroom) and keeps the growth path for the shapes that really are bigger: ECH or multi-share
// hellos, HTTP requests with multi-KiB cookie jars, and 0-RTT early data appended to the first
// flight. Those flows are the ones that can afford one copy, and they still end at the same ceiling.
//
// The value is deliberately NOT derived from the build tags. The two profiles differ in the ceiling
// (8 KiB mobile, 16 KiB desktop); the first payload a client sends does not differ by platform, and
// a second number keyed on the same tag would be a policy nobody measured.
const StreamSniffInitialBufferSize = 2 * 1024

// NewStreamSniffBuffer returns the buffer a stream sniff starts with.
//
// It is a sized pooled buffer, not a bare make(): the same allocator bucket serves the growth target
// and the release path, so an unused sniff buffer at the end of a timed-out wait goes back to the
// pool exactly as buf.NewPacket() did.
func NewStreamSniffBuffer() *buf.Buffer {
	return buf.NewSize(StreamSniffInitialBufferSize)
}

// streamSniffGrowths counts the on-demand growths this process performed. It exists because the
// only way to tell "the small buffer is the default path" from "every flow grew" is to count, and
// the count has to be bounded: one atomic is not a per-connection history, and it is the number a
// measurement run needs in order to separate the two populations. Tests read it through
// StreamSniffGrowthCount; nothing on the data path reads it.
var streamSniffGrowths atomic.Uint64

// StreamSniffGrowthCount reports how many stream sniff buffers grew on demand since process start.
// A growth refused because the buffer is already at the ceiling is not counted.
func StreamSniffGrowthCount() uint64 {
	return streamSniffGrowths.Load()
}

// growStreamBuffer replaces current with a buffer of limit capacity carrying the same payload.
//
// It is called BEFORE the round's read, never after one, so no byte is ever dropped from a full
// buffer to make room: the payload is moved to the larger array and the next read writes into the
// free space behind it.
//
// The function is total on purpose. With today's allocator the copy cannot fail - the trigger is
// "current is full and limit > current.Cap()", so the payload (== current.Cap()) always fits in
// limit - but every refusal returns the ORIGINAL buffer with every byte it had and none of them is
// counted as a growth. A caller that gets a refusal then behaves exactly as the old code did at the
// ceiling: the read reports io.ErrShortBuffer and the sniff ends with the bytes it already has.
// Refusing is a fallback, not a success, and it is why a limit that cannot hold more must not release
// the buffer it was handed.
func growStreamBuffer(current *buf.Buffer, limit int) *buf.Buffer {
	if limit <= current.Cap() {
		// Nothing to grow to. Refusing here rather than replacing an equal-capacity buffer keeps the
		// helper total: a caller whose limit is not above the current capacity must never have its
		// buffer released by a call that changed nothing.
		return current
	}
	grown := buf.NewSize(limit)
	if grown.Cap() != limit {
		// The sized allocator returned a shorter array than asked for. Continuing with it would
		// truncate; keep the payload where it is.
		grown.Release()
		return current
	}
	written, err := grown.Write(current.Bytes())
	if err != nil || written != current.Len() {
		grown.Release()
		return current
	}
	current.Release()
	streamSniffGrowths.Add(1)
	return grown
}
