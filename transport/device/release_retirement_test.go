package device

// Release after a session retirement: the properties the MASQUE caller depends on.
//
// These are the device-side halves of the "retire then release" contract proven in
// protocol/masque/session_release.go. They exist because a release that is only *offered* is what the
// auditors found: the facade could close a generation, but nothing established that the shapes which
// outlive a generation (a caller-owned queue, an attached return path, a live flow) survive it or
// block it. Each test here pins one of those rows.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// newRealStackDeferred builds a facade over the PRODUCTION userspace stack - no fake factory - so a
// release closes the same objects a real endpoint closes.
func newRealStackDeferred(t *testing.T, mtu uint32, address netip.Prefix) *deferredDevice {
	t.Helper()
	deferred, err := NewDeferred(Options{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		MTU:     mtu,
		Configuration: Configuration{
			MTU:     mtu,
			Address: []netip.Prefix{address},
		},
	})
	require.NoError(t, err)
	instance := deferred.(*deferredDevice)
	instance.SetPacketWriter(func(packetBuffers []*buf.Buffer) error {
		buf.ReleaseMulti(packetBuffers)
		return nil
	})
	require.NoError(t, instance.Start())
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

// TestDeferredReleaseRefusesAfterAQueueHandout is the queue row of the ownership table.
//
// A *tun.OutboundQueue belongs to its caller and the pinned sing-tun deliberately does not close
// caller-owned queues when the memory TUN closes: their drain goroutine lives until the owner's own
// Close. The facade cannot observe that Close, so it cannot prove the queue is gone - and releasing
// would leave that goroutine reading a closed TUN. The safe answer is a permanent refusal, which is
// what this pins: not a retry, not a timeout, an answer.
func TestDeferredReleaseRefusesAfterAQueueHandout(t *testing.T) {
	// The production stack, because the queue itself is a real *tun.OutboundQueue owned by the
	// pinned sing-tun package: a fake one would not exercise the lifetime this test is about.
	instance := newRealStackDeferred(t, 1280, netip.MustParsePrefix("10.42.0.2/32"))
	// A queue can only be handed out by a built generation, which is also why this handout implies
	// the release would otherwise succeed.
	queue := instance.NewOutboundQueue(func([]*buf.Buffer) {})
	require.NotNil(t, queue)
	require.True(t, instance.Built())

	err := instance.Release()
	require.Error(t, err, "Release destroyed a stack whose caller-owned queue could still use it")
	require.Contains(t, err.Error(), "caller-owned outbound queue")
	require.True(t, instance.Built(), "the refused release must leave the generation in place")
	require.Equal(t, 1, instance.Diagnostics().QueueHandouts)

	// The queue is still usable: nothing was torn down under it.
	require.NoError(t, queue.Close())

	// Closing the queue does NOT re-enable the release: the handout was never observable as closed,
	// so the refusal has to be permanent rather than a guess based on elapsed time.
	require.Error(t, instance.Release())
	require.Equal(t, 0, instance.Diagnostics().Releases)

	// Close is the hard path and still works, exactly as before.
	require.NoError(t, instance.Close())
}

// TestDeferredReturnPathAndStateSurviveARelease pins the "facade state is not generation state" row.
//
// The return path, the MTU and the assigned addresses live on the facade. A release replaces the
// generation behind them, so a caller that attached a return path before the build must still have it
// afterwards - and one attached while a generation exists must survive that generation's release.
func TestDeferredReturnPathAndStateSurviveARelease(t *testing.T) {
	instance := newTestDeferred(t, nil)
	returnPath := &fakeReturn{headroom: 24}
	require.NoError(t, instance.AttachReturn(returnPath))
	require.NoError(t, instance.UpdateConfiguration(Configuration{
		MTU:     1400,
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.7.7/32")},
	}))
	require.NoError(t, instance.Start())

	conn, err := instance.DialContext(context.Background(), N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.7.1"), 80))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, instance.Built())

	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
	require.Equal(t, 24, instance.FrontHeadroom(), "the attached return path's headroom was lost")
	inet4, _ := instance.PortAddresses()
	require.Equal(t, netip.MustParseAddr("10.42.7.7"), inet4, "the facade's assigned address was lost")
	require.EqualValues(t, 1400, instance.PortMTU())

	// The return path still consumes the next generation's inbound packet, and a rebuild carries the
	// configuration that was set while the generation existed.
	packet := buf.NewSize(24 + 20)
	packet.Resize(24, 0)
	require.NoError(t, instance.WriteInboundBuffers([]*buf.Buffer{packet}))
	require.True(t, instance.Built(), "the inbound packet after the release did not rebuild the stack")
	require.EqualValues(t, 1, returnPath.offered.Load())
	require.EqualValues(t, 2, instance.Generation(), "the rebuilt stack reused the released generation id")

	// Detach still works after the release/rebuild cycle: it is facade state, not stack state.
	require.NoError(t, instance.DetachReturn(returnPath))
	require.Zero(t, instance.FrontHeadroom())
}

// TestTheRebuiltStackUsesTheConfigurationSetWhileReleased is the MTU/address rebuild proof.
//
// A server can re-assign our inner address while a generation exists, and the release that follows a
// session retirement discards that generation. The next generation must be configured with the LATEST
// values, not the ones the released generation was built with - otherwise a rebuilt stack would send
// from an address the server no longer routes back to.
func TestTheRebuiltStackUsesTheConfigurationSetWhileReleased(t *testing.T) {
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

	sources := make(chan netip.Addr, 8)
	instance.SetPacketWriter(func(packetBuffers []*buf.Buffer) error {
		for _, packetBuffer := range packetBuffers {
			if source, _, _, valid := packetAddressesForTest(packetBuffer.Bytes()); valid {
				select {
				case sources <- source:
				default:
				}
			}
			packetBuffer.Release()
		}
		return nil
	})
	require.NoError(t, instance.Start())

	// Build with the first configuration and send one datagram, so the source address is observed on
	// the wire rather than inferred from facade state.
	packetConn, err := instance.ListenPacket(context.Background(),
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
	require.NoError(t, err)
	_, err = packetConn.WriteTo([]byte{0x00, 0x01}, &net.UDPAddr{IP: net.IPv4(10, 42, 0, 1), Port: 53})
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.42.0.2"), <-sources)
	require.NoError(t, packetConn.Close())
	require.EqualValues(t, 1280, instance.inner.device.PortMTU())

	// The server re-assigns while the generation exists, then the session retires.
	require.NoError(t, instance.UpdateConfiguration(Configuration{
		MTU:     1400,
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.9.9/32")},
	}))
	require.NoError(t, instance.Release())
	require.False(t, instance.Built())

	// The rebuild must take the newest configuration: the MTU on the new generation and the source
	// address of a packet it sends.
	packetConn, err = instance.ListenPacket(context.Background(),
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
	require.NoError(t, err)
	require.EqualValues(t, 1400, instance.inner.device.PortMTU(),
		"the rebuilt generation was built with the released generation's MTU")
	_, err = packetConn.WriteTo([]byte{0x00, 0x02}, &net.UDPAddr{IP: net.IPv4(10, 42, 0, 1), Port: 53})
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.42.9.9"), <-sources,
		"the rebuilt generation sent from the address of the session that owns it, not the old one")
	require.NoError(t, packetConn.Close())
}

// TestReleaseRacingDialWriteInboundAndClose is the interleaving gate for the release itself, against
// the production stack.
//
// The release must be safe while work is inside the stack: either it refuses (an operation is in
// flight) or it wins before the operation starts, in which case the operation retries on the reborn
// generation. What must never happen is a panic, a use-after-close, a hang, or a leaked in-flight
// count. Run with -race; the assertions are liveness plus the counter, because both interleavings are
// legitimate outcomes.
func TestReleaseRacingDialWriteInboundAndClose(t *testing.T) {
	instance := newRealStackDeferred(t, 1280, netip.MustParsePrefix("10.42.0.2/32"))

	var (
		wg       sync.WaitGroup
		stop     = make(chan struct{})
		progress atomic.Int64
		failures atomic.Int64
	)
	// Short-deadline TCP dials through the stack. They fail (nothing answers 10.42.0.1:9), and the
	// failure is not the subject: the window between them is where a release can win.
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				conn, err := instance.DialContext(ctx, N.NetworkTCP,
					M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 9))
				cancel()
				if err == nil {
					_, _ = conn.Write([]byte("race"))
					_ = conn.Close()
				} else if !errors.Is(err, errGenerationGone) && !errors.Is(err, context.DeadlineExceeded) {
					// A dial that was not merely slow and not merely addressing a generation that a
					// release replaced is a real failure, not an expected race outcome.
					failures.Add(1)
				}
				progress.Add(1)
			}
		}()
	}
	// A UDP socket writes through the stack while generations come and go.
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				packetConn, err := instance.ListenPacket(context.Background(),
					M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
				if err != nil {
					if !errors.Is(err, errGenerationGone) && !errors.Is(err, errDeviceClosed) {
						failures.Add(1)
					}
					progress.Add(1)
					continue
				}
				// A write that loses the race against the release is expected to fail; what must not
				// happen is a panic inside the stack.
				_, _ = packetConn.WriteTo([]byte{0x00, 0x01}, &net.UDPAddr{IP: net.IPv4(10, 42, 0, 1), Port: 53})
				_ = packetConn.Close()
				progress.Add(1)
			}
		}()
	}
	// An inbound packet is the shape with no local dial at all: a remote-initiated flow arrives
	// through the stack while the stack is being released.
	for worker := 0; worker < 2; worker++ {
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
				if err := instance.WriteInboundBuffers([]*buf.Buffer{packet}); err != nil {
					if !errors.Is(err, errGenerationGone) && !errors.Is(err, errDeviceClosed) {
						failures.Add(1)
					}
				}
				buf.ReleaseMulti([]*buf.Buffer{packet})
				progress.Add(1)
			}
		}()
	}

	// Wait until the storm is genuinely running, so the release loop cannot finish before anything
	// started and pass while proving nothing.
	require.Eventually(t, func() bool { return progress.Load() > 20 },
		10*time.Second, time.Millisecond)

	for round := 0; round < 200; round++ {
		_ = instance.Release()
	}
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the release race left a worker blocked")
	}
	require.Positive(t, progress.Load())
	require.Zero(t, failures.Load(), "an operation failed in a way the facade's retry does not cover")
	// No in-flight count may leak: after the workers stop, the facade must accept a release again.
	require.Zero(t, instance.InFlight())
	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
	diagnostics := instance.Diagnostics()
	t.Logf("release race under pressure: operations=%d builds=%d releases=%d queue-handouts=%d",
		progress.Load(), diagnostics.Builds, diagnostics.Releases, diagnostics.QueueHandouts)

	// The same race with a settling gap, which is the shape a retirement actually meets when the
	// tunnel has a moment to go quiet between two business demands. Here the release must WIN at least
	// once, so the interleaving "release closes the generation, the next demand builds the next one"
	// is exercised rather than merely tolerated.
	var settlingWG sync.WaitGroup
	settlingStop := make(chan struct{})
	for worker := 0; worker < 2; worker++ {
		settlingWG.Add(1)
		go func() {
			defer settlingWG.Done()
			for {
				select {
				case <-settlingStop:
					return
				case <-time.After(time.Millisecond):
				}
				packet := buf.NewSize(20)
				if err := instance.WriteInboundBuffers([]*buf.Buffer{packet}); err != nil {
					if !errors.Is(err, errGenerationGone) && !errors.Is(err, errDeviceClosed) {
						failures.Add(1)
					}
				}
				buf.ReleaseMulti([]*buf.Buffer{packet})
				progress.Add(1)
			}
		}()
	}
	releasesBefore := instance.Diagnostics().Releases
	require.Eventually(t, func() bool {
		for attempt := 0; attempt < 10; attempt++ {
			_ = instance.Release()
			if instance.Diagnostics().Releases > releasesBefore {
				return true
			}
		}
		return false
	}, 10*time.Second, time.Millisecond, "a release never won against a settling inbound worker")
	close(settlingStop)
	settlingWG.Wait()
	require.Zero(t, failures.Load())
	// Deterministic continuation: with the demand stopped, a release must succeed, and the next
	// inbound demand must rebuild the generation - the interleaving a retirement release creates.
	buildsBeforeRebuild := instance.Diagnostics().Builds
	require.NoError(t, instance.Release())
	require.False(t, instance.Built())
	packet := buf.NewSize(20)
	require.NoError(t, instance.WriteInboundBuffers([]*buf.Buffer{packet}))
	buf.ReleaseMulti([]*buf.Buffer{packet})
	require.True(t, instance.Built(), "the demand after a winning release did not rebuild the stack")
	require.Equal(t, buildsBeforeRebuild+1, instance.Diagnostics().Builds)

	// The queue shape raced against a release: whichever wins, neither may panic and the queue stays
	// closable. After a handout the release is refused permanently (see the deterministic test).
	var queueWG sync.WaitGroup
	queueWG.Add(2)
	var queueHanded atomic.Bool
	go func() {
		defer queueWG.Done()
		if queue := instance.NewOutboundQueue(func([]*buf.Buffer) {}); queue != nil {
			queueHanded.Store(true)
			_ = queue.Close()
		}
	}()
	go func() {
		defer queueWG.Done()
		_ = instance.Release()
	}()
	queueWG.Wait()
	require.True(t, queueHanded.Load(), "the queue creation did not produce a queue")
	require.NoError(t, instance.Close())
}

// TestReleaseAndCloseRaceEachOther is the shutdown interleaving: the product closes the device through
// the scope while a retirement release may be running on the session goroutine. Both paths must be
// safe together - exactly one of them closes the generation, and neither blocks.
func TestReleaseAndCloseRaceEachOther(t *testing.T) {
	for round := 0; round < 20; round++ {
		instance := newRealStackDeferred(t, 1280, netip.MustParsePrefix("10.42.0.2/32"))
		// Build it, then race the two teardown paths.
		packetConn, err := instance.ListenPacket(context.Background(),
			M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
		require.NoError(t, err)
		require.NoError(t, packetConn.Close())

		var wg sync.WaitGroup
		wg.Add(2)
		released := make(chan error, 1)
		go func() {
			defer wg.Done()
			released <- instance.Release()
		}()
		go func() {
			defer wg.Done()
			_ = instance.Close()
		}()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Release and Close deadlocked")
		}
		// Either Release won (nil) or Close did (errDeviceClosed). Both are correct; a nil from a
		// device that was already closed would be the bug.
		releaseErr := <-released
		require.True(t, releaseErr == nil || errors.Is(releaseErr, errDeviceClosed),
			"unexpected Release result while racing Close: %v", releaseErr)
		require.False(t, instance.Built())
	}
}

// packetAddressesForTest extracts the source and destination of a bare IPv4/IPv6 packet. It is a
// minimal reader for the measurement below; the production parser lives in the MASQUE transport and
// is exercised by its own tests.
func packetAddressesForTest(packet []byte) (source netip.Addr, destination netip.Addr, protocol uint8, valid bool) {
	if len(packet) < 20 {
		return netip.Addr{}, netip.Addr{}, 0, false
	}
	switch packet[0] >> 4 {
	case 4:
		source = netip.AddrFrom4([4]byte{packet[12], packet[13], packet[14], packet[15]})
		destination = netip.AddrFrom4([4]byte{packet[16], packet[17], packet[18], packet[19]})
		return source, destination, packet[9], true
	case 6:
		if len(packet) < 40 {
			return netip.Addr{}, netip.Addr{}, 0, false
		}
		var sourceBytes, destinationBytes [16]byte
		copy(sourceBytes[:], packet[8:24])
		copy(destinationBytes[:], packet[24:40])
		return netip.AddrFrom16(sourceBytes), netip.AddrFrom16(destinationBytes), packet[6], true
	default:
		return netip.Addr{}, netip.Addr{}, 0, false
	}
}
