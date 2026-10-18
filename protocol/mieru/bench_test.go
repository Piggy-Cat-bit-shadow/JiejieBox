package mieru

import (
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	mierucommon "github.com/enfein/mieru/v3/apis/common"
)

// The benchmark compares this package's datagram framing with the official
// SDK wrappers it replaces on the same payload. It is the evidence behind the
// "one pooled buffer, one copy" claim in the design note; it is not a network
// throughput measurement.

var benchmarkDestination = M.SocksaddrFrom(netip.MustParseAddr("192.0.2.7"), 443)

var benchmarkPayload = make([]byte, 1200)

type discardConn struct{}

func (discardConn) Read(p []byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)        { return len(p), nil }
func (discardConn) Close() error                       { return nil }
func (discardConn) LocalAddr() net.Addr                { return nil }
func (discardConn) RemoteAddr() net.Addr               { return nil }
func (discardConn) SetDeadline(t time.Time) error      { return nil }
func (discardConn) SetReadDeadline(t time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(t time.Time) error { return nil }

func BenchmarkWriteUOTFrame(b *testing.B) {
	b.ReportAllocs()
	writer := io.Discard
	for range b.N {
		if err := writeUOTFrame(writer, benchmarkPayload, benchmarkDestination); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteUOTFrameSDK(b *testing.B) {
	b.ReportAllocs()
	tunnel := mierucommon.NewPacketOverStreamTunnel(discardConn{})
	frame := make([]byte, 3+7+len(benchmarkPayload))
	frame[3] = 0x01
	copy(frame[4:], []byte{192, 0, 2, 7})
	frame[8] = 0x01
	frame[9] = 0xbb
	copy(frame[10:], benchmarkPayload)
	for range b.N {
		if _, err := tunnel.Write(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// loopingReader serves one pre-built frame repeatedly without allocating in
// the read path, so the benchmark measures the decoder, not the source.
type loopingReader struct {
	frame []byte
	index int
}

func (r *loopingReader) Read(p []byte) (int, error) {
	if r.index >= len(r.frame) {
		r.index = 0
	}
	n := copy(p, r.frame[r.index:])
	r.index += n
	return n, nil
}

func benchmarkFrame(b *testing.B) *loopingReader {
	b.Helper()
	var frame []byte
	writer := &sliceWriter{}
	if err := writeUOTFrame(writer, benchmarkPayload, benchmarkDestination); err != nil {
		b.Fatal(err)
	}
	frame = writer.data
	return &loopingReader{frame: frame}
}

type sliceWriter struct {
	data []byte
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	return len(p), nil
}

func BenchmarkReadUOTFrame(b *testing.B) {
	b.ReportAllocs()
	framer := newUOTFramer(benchmarkFrame(b))
	buffer := make([]byte, 1500)
	for range b.N {
		n, _, err := framer.readFrame(buffer)
		if err != nil {
			b.Fatal(err)
		}
		if n != len(benchmarkPayload) {
			b.Fatalf("read %d bytes", n)
		}
	}
}

func BenchmarkReadUOTFrameSDK(b *testing.B) {
	b.ReportAllocs()
	reader := benchmarkFrame(b)
	tunnel := mierucommon.NewPacketOverStreamTunnel(&readOnlyConn{Reader: reader})
	buffer := make([]byte, 1500)
	for range b.N {
		n, err := tunnel.Read(buffer)
		if err != nil {
			b.Fatal(err)
		}
		if n != 3+7+len(benchmarkPayload) {
			b.Fatalf("read %d bytes", n)
		}
	}
}

type readOnlyConn struct {
	io.Reader
}

func (readOnlyConn) Write(p []byte) (int, error)        { return len(p), nil }
func (readOnlyConn) Close() error                       { return nil }
func (readOnlyConn) LocalAddr() net.Addr                { return nil }
func (readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (readOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (readOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (readOnlyConn) SetWriteDeadline(t time.Time) error { return nil }
