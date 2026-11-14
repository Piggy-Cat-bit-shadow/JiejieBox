package anytls

import (
	"context"
	"crypto/tls"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// silentServer accepts TCP connections and then sends nothing.
//
// That is the exact state the device profile showed: a TLS client that has sent its ClientHello
// and is parked in readHandshakeBytes waiting for a ServerHello that a congested tunnel is not
// delivering. The client has already allocated its TLS state and its post-quantum key share by
// then, which is what makes this the expensive shape to bound.
type silentServer struct {
	listener net.Listener
	accepted atomic.Int64
	conns    sync.Map
}

func newSilentServer(t *testing.T) *silentServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &silentServer{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			server.accepted.Add(1)
			server.conns.Store(conn, struct{}{})
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		server.conns.Range(func(key, _ any) bool {
			key.(net.Conn).Close()
			return true
		})
	})
	return server
}

// tlsDialOut dials and starts a real TLS handshake, which is what the production DialOut does
// behind a NetworkExtension. The handshake is expected to block; the creation's deadline (kept from
// the first caller) cancels it.
func tlsDialOut(address string, inFlight *atomic.Int64, peak *atomic.Int64) singanytls.DialOutFunc {
	return func(ctx context.Context) (net.Conn, error) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		dialer := net.Dialer{}
		raw, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		secured := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "anytls.test"})
		if err = secured.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		return secured, nil
	}
}

// peakHeapSampler records the highest HeapAlloc observed while a burst is running.
type peakHeapSampler struct {
	peak atomic.Uint64
	done chan struct{}
}

func newPeakHeapSampler() *peakHeapSampler {
	sampler := &peakHeapSampler{done: make(chan struct{})}
	go func() {
		var stats runtime.MemStats
		for {
			select {
			case <-sampler.done:
				return
			case <-time.After(2 * time.Millisecond):
			}
			runtime.ReadMemStats(&stats)
			for {
				observed := sampler.peak.Load()
				if stats.HeapAlloc <= observed || sampler.peak.CompareAndSwap(observed, stats.HeapAlloc) {
					break
				}
			}
		}
	}()
	return sampler
}

func (s *peakHeapSampler) stop() uint64 {
	close(s.done)
	return s.peak.Load()
}

// burstWithTLSHandshakes starts `burst` concurrent AnyTLS dials whose DialOut performs a real,
// never-completing TLS handshake, and reports the peak heap, the peak concurrent handshakes and the
// number of physical connections the silent server accepted.
func burstWithTLSHandshakes(t *testing.T, guard *resourceguard.Guard, burst int, wait time.Duration) (uint64, int64, int) {
	t.Helper()
	server := newSilentServer(t)
	var inFlight, peak atomic.Int64
	options := singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  tlsDialOut(server.listener.Addr().String(), &inFlight, &peak),
	}
	if guard != nil {
		options.AdmitCreation = admitSessionCreation(guard, "tls-burst")
	}
	client, err := singanytls.NewClient(options)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	sampler := newPeakHeapSampler()

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The handshake never completes, so this returns when the creation deadline expires.
			_, _ = client.DialContext(ctx, M.Socksaddr{Fqdn: "example.com", Port: 443})
		}()
	}
	wg.Wait()
	peakHeap := sampler.stop()
	runtime.GC()
	// Report the MARGINAL peak, not the process baseline: the two runs start from the same
	// steady state, so subtracting the pre-burst HeapAlloc is what makes them comparable.
	marginal := uint64(0)
	if peakHeap > baseline.HeapAlloc {
		marginal = peakHeap - baseline.HeapAlloc
	}
	return marginal, peak.Load(), int(server.accepted.Load())
}

// TestTLSHandshakeBurstIsBoundedBySessionCoalescing replaces the pre-coalescing amplification test.
//
// # Why the assertion changed
//
// The old test measured 200 concurrent TLS handshakes against an 8-slot admission limit, and proved
// that the limit lowered the peak handshake count and the peak heap. With session coalescing, that
// burst performs ONE handshake whether or not a guard exists: the expensive transient is removed
// rather than paced. Keeping the old assertion would fail on a better system, so the test now pins
// the new bound - one creation, one physical connection - for both shapes, and keeps logging the
// marginal heap so the numbers remain on record.
func TestTLSHandshakeBurstIsBoundedBySessionCoalescing(t *testing.T) {
	const burst = 200
	const handshakeWindow = 400 * time.Millisecond

	unguardedHeap, unguardedPeak, unguardedConns := burstWithTLSHandshakes(t, nil, burst, handshakeWindow)

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
	guardedHeap, guardedPeak, guardedConns := burstWithTLSHandshakes(t, guard, burst, handshakeWindow)

	t.Logf("unguarded: peak heap %d B, peak concurrent handshakes %d, server connections %d",
		unguardedHeap, unguardedPeak, unguardedConns)
	t.Logf("guarded:   peak heap %d B, peak concurrent handshakes %d, server connections %d",
		guardedHeap, guardedPeak, guardedConns)

	require.LessOrEqual(t, unguardedPeak, int64(1),
		"the unguarded burst performed %d concurrent handshakes; coalescing must keep it at one", unguardedPeak)
	require.Equal(t, 1, unguardedConns,
		"the unguarded burst opened %d physical connections", unguardedConns)
	require.LessOrEqual(t, guardedPeak, int64(1))
	require.LessOrEqual(t, guardedConns, 1)
	require.EqualValues(t, 1, guard.Stats().Gate.Admitted,
		"the guarded burst was charged %d creations", guard.Stats().Gate.Admitted)
}
