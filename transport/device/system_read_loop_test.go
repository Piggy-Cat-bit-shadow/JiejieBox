package device

import (
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// The system device's read loop must survive a transient read error, and must not spin on a
// permanent one.
//
// # The two halves of one predicate, which is why one file pins both
//
// LX 4537a1ac is "a transient error ends the datapath": the loop returns, nothing restarts it -
// `Start` runs once per device lifetime - so the process stays up, the tunnel still reports
// connected, and every later packet is black-holed until the configuration is reloaded.
//
// LX 3f0958bf is the opposite: `continue` on every error turns a permanent one into a spin, at 100%
// of a core with a log line per iteration.
//
// A read loop can only be wrong in one of those two directions, so a single predicate decides both
// and a test that asserted only one would pass on the other failure. These tests assert the split.
//
// # Why the error set comes from the dependency
//
// `tun.IsRecoverableReadError` is the contract the TUN implementations are written against:
// `ENOBUFS` and `ENOMEM` are what Darwin's `recvmsg_x` and Linux's batch reads report when the queue
// is full (sing-tun tun_darwin.go:420-437, tun_read_error.go:11-13). `protocol/bridge/backend_linux.go`
// already applies exactly this predicate and backoff to the same `BatchRead`; this file was the one
// place that did not, so two datapaths disagreed about what an `ENOBUFS` meant.

// systemReadFakeTun feeds a scripted sequence of read results to the read loop, then blocks so the
// loop stays alive and observable rather than racing a terminal error.
type systemReadFakeTun struct {
	access   sync.Mutex
	failures []error
	packet   []byte
	served   bool

	readAttempts chan struct{}
	release      chan struct{}
}

func newSystemReadFakeTun(packet []byte, failures ...error) *systemReadFakeTun {
	return &systemReadFakeTun{
		failures:     failures,
		packet:       packet,
		readAttempts: make(chan struct{}, 64),
		release:      make(chan struct{}),
	}
}

func (f *systemReadFakeTun) Read(buffer []byte) (int, error) {
	f.access.Lock()
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		f.access.Unlock()
		f.readAttempts <- struct{}{}
		return 0, err
	}
	if !f.served {
		f.served = true
		f.access.Unlock()
		written := copy(buffer[tun.PacketOffset:], f.packet)
		f.readAttempts <- struct{}{}
		return tun.PacketOffset + written, nil
	}
	f.access.Unlock()
	f.readAttempts <- struct{}{}
	<-f.release
	return 0, os.ErrClosed
}

// consumedReads reports how many times the loop called Read and then stopped, which is the
// observation that separates "it stopped" from "it spun".
func (f *systemReadFakeTun) consumedReads() int {
	count := 0
	for {
		select {
		case <-f.readAttempts:
			count++
		default:
			return count
		}
	}
}

func (f *systemReadFakeTun) Write(packet []byte) (int, error) { return len(packet), nil }

func (f *systemReadFakeTun) Name() (string, error) { return "faketun0", nil }

func (f *systemReadFakeTun) Start() error { return nil }

func (f *systemReadFakeTun) UpdateRouteOptions(tun.Options) error { return nil }

func (f *systemReadFakeTun) Close() error {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
	return nil
}

func newReadLoopDevice(recorder logger.ContextLogger, capture *packetCapture) *systemDevice {
	instance := &systemDevice{
		options: Options{
			Context: context.Background(),
			Logger:  recorder,
			MTU:     1500,
		},
	}
	instance.SetPacketWriter(capture.write)
	return instance
}

// runReadLoop starts the production read loop over the fixture and returns a channel closed when it
// returns. The caller is responsible for releasing the fixture.
func runReadLoop(t *testing.T, instance *systemDevice, fakeTun *systemReadFakeTun) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		instance.readLoop(fakeTun, 1500)
	}()
	t.Cleanup(func() {
		_ = fakeTun.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the read loop did not return after the fixture was released")
		}
	})
	return done
}

func newReadLoopHarness(t *testing.T, fakeTun *systemReadFakeTun) (*packetCapture, chan struct{}) {
	t.Helper()
	capture := &packetCapture{}
	instance := newReadLoopDevice(log.NewNOPFactory().Logger(), capture)
	return capture, runReadLoop(t, instance, fakeTun)
}

// ipv4UDPPacket builds one small well-formed IPv4/UDP packet.
func ipv4UDPPacket(t *testing.T) []byte {
	t.Helper()
	packet := make([]byte, header.IPv4MinimumSize+header.UDPMinimumSize)
	ipHeader := header.IPv4(packet)
	ipHeader.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(packet)),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     netip.MustParseAddr("10.0.0.2"),
		DstAddr:     netip.MustParseAddr("93.184.216.34"),
	})
	ipHeader.SetChecksum(0)
	ipHeader.SetChecksum(^ipHeader.CalculateChecksum())
	return packet
}

// TestSystemReadLoopSurvivesATransientReadError is the direct statement of LX 4537a1ac.
//
// The assertion is not "the loop logged something": it is that a REAL packet queued behind the
// error still reaches the device's packet writer. That is the difference between a datapath that
// recovered and one that merely stayed alive.
func TestSystemReadLoopSurvivesATransientReadError(t *testing.T) {
	for _, transient := range []error{syscall.ENOBUFS, syscall.ENOMEM, syscall.EAGAIN, syscall.EINTR, tun.ErrTooManySegments} {
		t.Run(transient.Error(), func(t *testing.T) {
			fakeTun := newSystemReadFakeTun(ipv4UDPPacket(t), transient)
			capture, done := newReadLoopHarness(t, fakeTun)

			require.Eventually(t, func() bool { return capture.count() >= 1 }, 10*time.Second, time.Millisecond,
				"a recoverable read error (%v) ended the read loop: nothing restarts it, so every "+
					"later packet is dropped until the configuration is reloaded", transient)

			select {
			case <-done:
				t.Fatalf("the read loop returned on %v, so the device is a permanent black hole", transient)
			default:
			}
		})
	}
}

// TestSystemReadLoopStopsOnAPermanentReadError is the other half, and the reason the fix is not
// "always continue".
//
// The observation is the loop's RETURN, not a read count: a spinning loop never returns, so the
// deadline is what separates "it stopped" from "it spun". A read counter would have to race the
// loop, and the count it produced would prove nothing the deadline does not.
func TestSystemReadLoopStopsOnAPermanentReadError(t *testing.T) {
	fakeTun := newSystemReadFakeTun(nil, syscall.EINVAL)
	capture, done := newReadLoopHarness(t, fakeTun)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the read loop did not return on a permanent read error: it spins, burning a core " +
			"and a log line per iteration")
	}
	require.Zero(t, capture.count())
}

// TestSystemReadLoopLogsARecoverableErrorAtDebugAndAFatalOneAtError pins the level split, so a full
// queue cannot become a log storm and a dead datapath cannot become silent.
//
// This is asserted HERE rather than on `readErrorIsFatal` because the predicate is pure: it decides,
// it does not log. The loops are what choose a level.
func TestSystemReadLoopLogsARecoverableErrorAtDebugAndAFatalOneAtError(t *testing.T) {
	t.Run("recoverable stays at Debug", func(t *testing.T) {
		recorder := &readLoopLevelRecorder{ContextLogger: log.NewNOPFactory().Logger()}
		fakeTun := newSystemReadFakeTun(ipv4UDPPacket(t), syscall.ENOBUFS)
		instance := newReadLoopDevice(recorder, &packetCapture{})
		done := runReadLoop(t, instance, fakeTun)

		require.Eventually(t, func() bool { return recorder.debugs.Load() > 0 }, 10*time.Second, time.Millisecond,
			"a recoverable read error must be reported, or a persistently full queue is invisible")
		require.Zero(t, recorder.errors.Load(),
			"a recoverable read error must not be logged at Error, or a full queue becomes a log storm")

		_ = fakeTun.Close()
		<-done
	})

	t.Run("fatal is reported at Error", func(t *testing.T) {
		recorder := &readLoopLevelRecorder{ContextLogger: log.NewNOPFactory().Logger()}
		fakeTun := newSystemReadFakeTun(nil, syscall.EINVAL)
		instance := newReadLoopDevice(recorder, &packetCapture{})
		done := runReadLoop(t, instance, fakeTun)

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the read loop did not return on a permanent read error")
		}
		require.Equal(t, int64(1), recorder.errors.Load(),
			"a fatal read error must be reported at Error, so a dead datapath is visible")
		require.Zero(t, recorder.debugs.Load())
	})
}

// TestReadErrorIsFatalClassifiesTheWholeSurface is the table all three loops depend on.
//
// It is stated separately because `readLoop`, `readLoopLinux` and `readLoopDarwin` all route through
// it: if the predicate drifts, the loops drift with it, and the tests above would only notice for
// the errors they happen to script.
func TestReadErrorIsFatalClassifiesTheWholeSurface(t *testing.T) {
	instance := &systemDevice{
		options: Options{
			Context: context.Background(),
			Logger:  log.NewNOPFactory().Logger(),
			MTU:     1500,
		},
	}

	for _, testCase := range []struct {
		name  string
		err   error
		fatal bool
	}{
		{"net.ErrClosed", net.ErrClosed, true},
		{"io.EOF", io.EOF, true},
		{"os.ErrClosed", os.ErrClosed, true},
		{"EBADF is the darwin close path", syscall.EBADF, true},
		{"EINVAL is not recoverable for a tun read", syscall.EINVAL, true},
		{"EPERM is not recoverable for a tun read", syscall.EPERM, true},
		{"ENOBUFS is a full queue, not a teardown", syscall.ENOBUFS, false},
		{"ENOMEM is a full queue, not a teardown", syscall.ENOMEM, false},
		{"EAGAIN is retryable", syscall.EAGAIN, false},
		{"EINTR is retryable", syscall.EINTR, false},
		{"ErrTooManySegments is retryable", tun.ErrTooManySegments, false},
		{"wrapped ENOBUFS is still recoverable", E.Cause(syscall.ENOBUFS, "batch read packet"), false},
		{"wrapped EPERM is still fatal", E.Cause(syscall.EPERM, "batch read packet"), true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.fatal, instance.readErrorIsFatal(testCase.err),
				"readErrorIsFatal(%v)", testCase.err)
		})
	}
}

// readLoopLevelRecorder counts the two levels the read loop chooses between, so a test can assert
// that a recoverable error does not produce an Error line and a fatal one does.
type readLoopLevelRecorder struct {
	logger.ContextLogger
	errors atomic.Int64
	debugs atomic.Int64
}

func (r *readLoopLevelRecorder) Error(args ...any) { r.errors.Add(1) }
func (r *readLoopLevelRecorder) Debug(args ...any) { r.debugs.Add(1) }
func (r *readLoopLevelRecorder) ErrorContext(ctx context.Context, args ...any) {
	r.errors.Add(1)
}
func (r *readLoopLevelRecorder) DebugContext(ctx context.Context, args ...any) {
	r.debugs.Add(1)
}

var _ tun.Tun = (*systemReadFakeTun)(nil)
