package device

// The deferred-device rules that the phase measurement made worth keeping: nothing is built until a
// real demand, a demand that arrives while a build is running shares ONE generation, a build that
// fails or that finishes after Close is never published, and an explicit Release refuses while an
// operation is still inside the stack.
//
// The fake inner device is deliberately a `Device` with a nil embedded implementation: the facade
// may call only the methods overridden here. A method reached without an override panics, which is
// how an accidental new dependency on the inner device would show up.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type fakeDevice struct {
	// Device is nil: only the overridden methods may be called.
	Device

	access     sync.Mutex
	configures []Configuration
	starts     int
	closed     atomic.Int32
	writer     PacketWriter
	dialCalls  int
	// dialGate, when non-nil, blocks DialContext until it is closed.
	dialGate chan struct{}
	// writeCalls records the inbound batches the facade handed to this device.
	writeCalls atomic.Int32
}

func (f *fakeDevice) NewOutboundQueue(func([]*buf.Buffer)) *tun.OutboundQueue {
	return new(tun.OutboundQueue)
}

func (f *fakeDevice) getWriter() PacketWriter {
	f.access.Lock()
	defer f.access.Unlock()
	return f.writer
}

// fakeRawDevice is a Device that also implements rawInboundWriter, which is what the production stack
// device does. rawWrites vs writeCalls is the observable difference between the two inbound paths.
type fakeRawDevice struct {
	fakeDevice
	rawWrites atomic.Int32
}

func (f *fakeRawDevice) writeRawBuffers([]*buf.Buffer) error {
	f.rawWrites.Add(1)
	return nil
}

func (f *fakeRawDevice) waitWriteRawBuffers([]*buf.Buffer) error {
	f.rawWrites.Add(1)
	return nil
}

func (f *fakeDevice) SetPacketWriter(writer PacketWriter) {
	f.access.Lock()
	defer f.access.Unlock()
	f.writer = writer
}

func (f *fakeDevice) UpdateConfiguration(configuration Configuration) error {
	f.access.Lock()
	defer f.access.Unlock()
	f.configures = append(f.configures, configuration)
	return nil
}

func (f *fakeDevice) Start() error {
	f.access.Lock()
	defer f.access.Unlock()
	f.starts++
	return nil
}

func (f *fakeDevice) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	f.access.Lock()
	f.dialCalls++
	gate := f.dialGate
	f.access.Unlock()
	if gate != nil {
		<-gate
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (f *fakeDevice) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

func (f *fakeDevice) WriteInboundBuffers([]*buf.Buffer) error {
	f.writeCalls.Add(1)
	return nil
}

func (f *fakeDevice) Close() error {
	f.closed.Add(1)
	return nil
}

func (f *fakeDevice) configureCount() int {
	f.access.Lock()
	defer f.access.Unlock()
	return len(f.configures)
}

func (f *fakeDevice) startCount() int {
	f.access.Lock()
	defer f.access.Unlock()
	return f.starts
}

// newTestDeferred builds a facade whose inner devices are the supplied fake.
func newTestDeferred(t *testing.T, factory func(Options) (Device, error)) *deferredDevice {
	t.Helper()
	if factory == nil {
		factory = func(Options) (Device, error) { return &fakeDevice{}, nil }
	}
	deferred, err := NewDeferred(Options{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		MTU:     1280,
		Configuration: Configuration{
			MTU:     1280,
			Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
		},
	})
	require.NoError(t, err)
	instance := deferred.(*deferredDevice)
	instance.factory = factory
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func TestDeferredBuildsNothingUntilARealDemand(t *testing.T) {
	var builds atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		builds.Add(1)
		return &fakeDevice{}, nil
	})
	require.False(t, instance.Built(), "construction built the inner device")
	require.NoError(t, instance.UpdateConfiguration(Configuration{
		MTU:     1280,
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
	}))
	require.NoError(t, instance.Start())
	require.False(t, instance.Built(), "configuration or Start built the inner device")
	require.Zero(t, builds.Load())

	conn, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, instance.Built())
	require.EqualValues(t, 1, builds.Load(), "the first demand did not build exactly one generation")

	_, err = instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.NoError(t, err)
	require.EqualValues(t, 1, builds.Load(), "a second demand built a second generation")
	require.EqualValues(t, 1, instance.Generation())
}

func TestDeferredAppliesStoredStateToTheFirstGeneration(t *testing.T) {
	inner := &fakeDevice{}
	instance := newTestDeferred(t, func(Options) (Device, error) { return inner, nil })
	writer := func(packetBuffers []*buf.Buffer) error { return nil }
	instance.SetPacketWriter(writer)
	// A configuration that arrives BEFORE the stack exists must be applied at the build, in the same
	// order the endpoint uses: configuration, then Start.
	require.NoError(t, instance.UpdateConfiguration(Configuration{
		MTU:     1400,
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.9.9/32")},
	}))
	require.NoError(t, instance.Start())
	_, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.9.1"), 80))
	require.NoError(t, err)

	require.Equal(t, 1, inner.configureCount())
	require.EqualValues(t, 1400, inner.configures[0].MTU)
	require.Equal(t, netip.MustParsePrefix("10.42.9.9/32"), inner.configures[0].Address[0])
	require.Equal(t, 1, inner.startCount())
	require.NotNil(t, inner.writer, "the packet writer set before the build was not installed")
	require.EqualValues(t, 1400, instance.PortMTU())
	inet4, _ := instance.PortAddresses()
	require.Equal(t, netip.MustParseAddr("10.42.9.9"), inet4)
}

func TestDeferredBuildFailureIsNotPublishedAndCanBeRetried(t *testing.T) {
	var attempts atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		if attempts.Add(1) == 1 {
			return nil, errFakeBuild
		}
		return &fakeDevice{}, nil
	})
	_, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.ErrorIs(t, err, errFakeBuild)
	require.False(t, instance.Built(), "a failed build was published")

	conn, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, instance.Built())
	require.EqualValues(t, 2, attempts.Load())
}

// TestDeferredBuildRacingCloseIsDiscarded is the publish window: a build that finishes after Close
// must be closed and must never become the current generation.
func TestDeferredBuildRacingCloseIsDiscarded(t *testing.T) {
	gate := make(chan struct{})
	built := &fakeDevice{}
	var builds atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		builds.Add(1)
		<-gate
		return built, nil
	})
	result := make(chan error, 1)
	go func() {
		_, err := instance.DialContext(context.Background(), N.NetworkTCP,
			M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
		result <- err
	}()
	// Wait until the factory is actually inside its build, so Close provably races it.
	require.Eventually(t, func() bool { return builds.Load() == 1 }, 2*time.Second, time.Millisecond)
	// Close must NOT wait for the build: it returns while the factory is still inside it.
	//
	// The signal has to come AFTER Close returns, and it has to be bounded. Signalling before the
	// call made the assertion below racy (the dial could reach a deferred that was still open) and
	// left a goroutine that could block in Close forever once the test had finished - under
	// `-race` on a loaded machine that turned into a package-wide timeout. Waiting on a result
	// channel with a deadline both proves the property and cannot leak.
	closeResult := make(chan error, 1)
	go func() { closeResult <- instance.Close() }()
	select {
	case closeErr := <-closeResult:
		require.NoError(t, closeErr)
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for an in-flight build instead of discarding it")
	}
	_, closedErr := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.2"), 1))
	require.ErrorIs(t, closedErr, errDeviceClosed)
	close(gate)
	require.Error(t, <-result, "a dial completed against a build that Close discarded")
	require.False(t, instance.Built())
	require.Eventually(t, func() bool { return built.closed.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the discarded generation was not closed")
}

func TestDeferredReleaseRefusesInFlightOperationAndRebuilds(t *testing.T) {
	gate := make(chan struct{})
	first := &fakeDevice{dialGate: gate}
	var generations atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		if generations.Add(1) == 1 {
			return first, nil
		}
		return &fakeDevice{}, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = instance.DialContext(context.Background(), N.NetworkTCP,
			M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	}()
	require.Eventually(t, func() bool { return instance.InFlight() == 1 }, 2*time.Second, time.Millisecond)
	require.Error(t, instance.Release(), "Release succeeded with a dial inside the stack")
	close(gate)
	<-done
	require.Zero(t, instance.InFlight())

	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
	require.EqualValues(t, 1, first.closed.Load())

	conn, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, instance.Built())
	require.EqualValues(t, 2, instance.Generation(), "the rebuilt stack did not get a new generation")
}

func TestDeferredCloseBeforeAnyDemandBuildsNothing(t *testing.T) {
	var builds atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		builds.Add(1)
		return &fakeDevice{}, nil
	})
	require.NoError(t, instance.Close())
	require.Zero(t, builds.Load(), "Close built a stack")
	require.Error(t, instance.Start())
	_, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.Error(t, err)
}

func TestDeferredRejectsSystemOptions(t *testing.T) {
	_, err := NewDeferred(Options{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		System:  true,
	})
	require.Error(t, err, "the system device must keep its construction stage")
}

// fakeReturn consumes every packet it is offered and reports a headroom, which is what a TUN
// return path does for a packet that belongs to the host.
type fakeReturn struct {
	headroom int
	offered  atomic.Int32
}

func (r *fakeReturn) ReturnHeadroom() int { return r.headroom }

func (r *fakeReturn) ReturnPackets(packets [][]byte) [][]byte {
	r.offered.Add(int32(len(packets)))
	return nil
}

// TestDeferredReturnPathSurvivesTheBuild is the ownership rule that a wrapper can silently break: the
// return path is attached to the STABLE facade, so it must keep working when the stack behind it is
// built later - and the packet it consumes must not be handed to the inner device as well.
func TestDeferredReturnPathSurvivesTheBuild(t *testing.T) {
	inner := &fakeDevice{}
	instance := newTestDeferred(t, func(Options) (Device, error) { return inner, nil })
	returnPath := &fakeReturn{headroom: 32}
	require.NoError(t, instance.AttachReturn(returnPath))
	require.Equal(t, 32, instance.FrontHeadroom(), "the attached return path's headroom is not visible")

	packet := buf.NewSize(32 + 20)
	packet.Resize(32, 0)
	require.EqualValues(t, 20, common.Must1(packet.Write(make([]byte, 20))))
	require.NoError(t, instance.WriteInboundBuffers([]*buf.Buffer{packet}))
	require.True(t, instance.Built(), "an inbound packet did not build the stack")
	require.EqualValues(t, 1, returnPath.offered.Load())
	require.Zero(t, inner.writeCalls.Load(), "the return path consumed the packet and it was still written")

	unconsumed := &fakeReturn{headroom: 0}
	require.NoError(t, instance.DetachReturn(returnPath))
	require.NoError(t, instance.AttachReturn(unconsumed))
	packet = buf.NewSize(20)
	require.EqualValues(t, 20, common.Must1(packet.Write(make([]byte, 20))))
	require.NoError(t, instance.WriteInboundBuffers([]*buf.Buffer{packet}))
	require.EqualValues(t, 1, returnPath.offered.Load(), "a detached return path was still consulted")
	require.EqualValues(t, 1, unconsumed.offered.Load())
}

// TestDeferredRealStackBuildsOnDemandAndRebuilds is the same contract against the production Go
// stack: no stack at construction, one after the first socket, a releasable generation, and a working
// one after the rebuild.
func TestDeferredRealStackBuildsOnDemandAndRebuilds(t *testing.T) {
	deferred, err := NewDeferred(Options{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		MTU:     1280,
		Configuration: Configuration{
			MTU:     1280,
			Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
		},
	})
	require.NoError(t, err)
	instance := deferred.(*deferredDevice)
	t.Cleanup(func() { _ = instance.Close() })
	require.False(t, instance.Built())
	require.NoError(t, instance.UpdateConfiguration(Configuration{
		MTU:     1280,
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
	}))
	require.NoError(t, instance.Start())
	require.False(t, instance.Built(), "configuration or Start built the production stack")

	written := make(chan struct{}, 1)
	instance.SetPacketWriter(func(packetBuffers []*buf.Buffer) error {
		buf.ReleaseMulti(packetBuffers)
		select {
		case written <- struct{}{}:
		default:
		}
		return nil
	})
	packetConn, err := instance.ListenPacket(context.Background(),
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
	require.NoError(t, err)
	require.True(t, instance.Built())
	require.EqualValues(t, 1, instance.Generation())
	_, err = packetConn.WriteTo([]byte{0x00, 0x01}, &net.UDPAddr{IP: net.IPv4(10, 42, 0, 1), Port: 53})
	require.NoError(t, err)
	select {
	case <-written:
	case <-time.After(5 * time.Second):
		t.Fatal("no packet reached the writer through the freshly built stack")
	}
	require.NoError(t, packetConn.Close())
	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
	require.EqualValues(t, 1, instance.Generation())

	packetConn, err = instance.ListenPacket(context.Background(),
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
	require.NoError(t, err, "the rebuilt stack could not create a socket")
	require.True(t, instance.Built())
	require.EqualValues(t, 2, instance.Generation())
	require.NoError(t, packetConn.Close())
}

var errFakeBuild = &fakeBuildError{}

type fakeBuildError struct{}

func (*fakeBuildError) Error() string { return "fake inner build failure" }

var _ = tun.Return((*fakeReturn)(nil))

// TestDeferredPublishesTheWriterThatWasCurrentAtPublish is the deterministic half of V's publish-order
// finding: SetPacketWriter runs WHILE the factory is still working, and the value it stores must be the
// one the new generation gets. The old order snapshotted the writer before the build and applied it
// after the publish, so this call was overwritten by the stale value.
func TestDeferredPublishesTheWriterThatWasCurrentAtPublish(t *testing.T) {
	gate := make(chan struct{})
	inner := &fakeDevice{}
	entered := make(chan struct{})
	instance := newTestDeferred(t, func(Options) (Device, error) {
		close(entered)
		<-gate
		return inner, nil
	})
	firstWriter := func(packetBuffers []*buf.Buffer) error { return nil }
	secondWriter := func(packetBuffers []*buf.Buffer) error { return nil }
	instance.SetPacketWriter(firstWriter)

	result := make(chan error, 1)
	go func() {
		_, err := instance.DialContext(context.Background(), N.NetworkTCP,
			M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
		result <- err
	}()
	<-entered
	// The build has started but has not published. This is the exact window the old order lost.
	instance.SetPacketWriter(secondWriter)
	close(gate)
	require.NoError(t, <-result)

	installed := inner.getWriter()
	require.NotNil(t, installed)
	require.Equal(t, reflect.ValueOf(secondWriter).Pointer(), reflect.ValueOf(installed).Pointer(),
		"the generation was published with a writer that had already been superseded")
}

// TestDeferredConcurrentInboundDuringBuildNeverFallsBack is the other half: an inbound packet that
// arrives while the build is publishing must take the RAW path, because the generation it sees must
// already carry its raw writer. A generation whose raw field was filled in after the publish would
// silently take the fallback path here.
func TestDeferredConcurrentInboundDuringBuildNeverFallsBack(t *testing.T) {
	inner := &fakeRawDevice{}
	var builds atomic.Int32
	instance := newTestDeferred(t, func(Options) (Device, error) {
		builds.Add(1)
		return inner, nil
	})
	instance.SetPacketWriter(func(packetBuffers []*buf.Buffer) error { return nil })

	var inboundCalls atomic.Int32
	var inboundErrors atomic.Bool
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				packet := buf.NewSize(20)
				err := instance.WriteInboundBuffers([]*buf.Buffer{packet})
				if errors.Is(err, errGenerationGone) {
					// The release closed the generation between the build and the operation. The
					// facade already retried internally; a second attempt from here covers the case
					// where the release won again, and is the documented caller-side retry.
					for attempt := 0; attempt < 8 && errors.Is(err, errGenerationGone); attempt++ {
						err = instance.WriteInboundBuffers([]*buf.Buffer{packet})
					}
				}
				if err != nil {
					inboundErrors.Store(true)
				}
				buf.ReleaseMulti([]*buf.Buffer{packet})
				inboundCalls.Add(1)
			}
		}()
	}
	// Wait until the storm is actually running: without this the release loop can finish and stop the
	// workers before any of them is scheduled, and the test would pass while measuring nothing.
	require.Eventually(t, func() bool { return inboundCalls.Load() > 0 }, 5*time.Second, time.Millisecond)
	// Release refuses while a build is running, which is a legitimate answer here: the loop exists to
	// make the release and the first build interleave, not to demand that it wins the race.
	for round := 0; round < 200; round++ {
		_ = instance.Release()
	}
	close(stop)
	wg.Wait()
	require.Positive(t, inboundCalls.Load())
	require.False(t, inboundErrors.Load(), "an inbound write failed while generations were being rebuilt")
	// More than one build is legitimate: a release that wins against an idle generation makes the
	// next inbound packet build the next one. What must hold is that no generation was ever visible
	// with an uninitialised raw writer.
	require.Positive(t, builds.Load())
	require.EqualValues(t, builds.Load(), instance.Diagnostics().Builds)
	require.Zero(t, inner.writeCalls.Load(), "an inbound packet took the return-path-applying fallback")
	require.Positive(t, inner.rawWrites.Load(), "no inbound packet reached the raw writer")
	require.NoError(t, instance.Release())
}

// TestDeferredReleaseRefusesWhileABuildIsRunning covers the second finding: a construction is an
// operation too. Without buildInFlight, Release found nothing published, reported success, and the
// publish a moment later contradicted it.
func TestDeferredReleaseRefusesWhileABuildIsRunning(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{})
	inner := &fakeDevice{}
	instance := newTestDeferred(t, func(Options) (Device, error) {
		close(entered)
		<-gate
		return inner, nil
	})
	result := make(chan error, 1)
	go func() {
		_, err := instance.DialContext(context.Background(), N.NetworkTCP,
			M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
		result <- err
	}()
	<-entered
	require.Error(t, instance.Release(), "Release succeeded while a build was still running")
	close(gate)
	require.NoError(t, <-result)
	require.True(t, instance.Built())
	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
}

// TestDeferredQueueFailureIsDiagnosed is the third finding: NewOutboundQueue cannot return an error,
// so a nil must not be the only thing a caller gets. The cause is recorded and counted.
func TestDeferredQueueFailureIsDiagnosed(t *testing.T) {
	instance := newTestDeferred(t, func(Options) (Device, error) { return nil, errFakeBuild })
	queue := instance.NewOutboundQueue(func([]*buf.Buffer) {})
	require.Nil(t, queue)
	diagnostics := instance.Diagnostics()
	require.Equal(t, 1, diagnostics.QueueFailures)
	require.ErrorIs(t, diagnostics.LastError, errFakeBuild)
	require.False(t, diagnostics.Built)

	// The same API on a healthy generation returns a queue and records nothing.
	healthy := newTestDeferred(t, nil)
	queue = healthy.NewOutboundQueue(func([]*buf.Buffer) {})
	require.NotNil(t, queue)
	require.Zero(t, healthy.Diagnostics().QueueFailures)
}
