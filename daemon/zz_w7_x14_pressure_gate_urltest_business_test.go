package daemon

// W7 / X14 (ORIGINAL oracle, task book 30 section 7.2): "pressure tightening + buildgate queueing +
// URLTest foreground/background + business flows | no starvation / permit leak / live-flow kill;
// suppress new work rather than break old work".
//
// The previous delivery substituted a rule-set reload/Collect scenario under this number. This test
// drives the four required participants AT THE SAME TIME through the product composition:
//
//   - pressure tightening: a real resourceguard.Guard in the Box context, driven to LevelCritical;
//   - buildgate queueing: two real remote providers refreshed concurrently. At Critical the gate
//     admits one build; the second must be DEFERRED (retryable, no publish, fail fast) rather than
//     queued forever, and its retry after recovery must succeed (no starvation, no permit leak);
//   - URLTest foreground/background: real urltest.Measure calls against the Box's own coordinator,
//     saturated at its 10-slot limit. A queued BACKGROUND probe must be overtaken by a later
//     FOREGROUND probe (priority), and the background probe must still run later (no starvation);
//   - real TCP/UDP business flows: a streaming TCP request through the Box's HTTP inbound -> route ->
//     provider node -> real CONNECT upstream, and a real UDP echo through the product's direct
//     outbound. Both stay alive across the pressure transition and the provider republication.
//
// Assertions are the oracle's: no starvation (deferred refresh later succeeds, background probe later
// completes), no permit leak (build gate Inflight returns to zero), no live-flow kill (the streaming
// TCP flow and the UDP echo survive the whole critical phase).

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// w7ProviderDoc is one provider document of N HTTP nodes pointing at the local CONNECT upstream.
func w7ProviderDoc(connectPort int, count int) string {
	document := `{"outbounds":[`
	for index := 0; index < count; index++ {
		if index > 0 {
			document += ","
		}
		document += fmt.Sprintf(`{"type":"http","tag":"n%d","server":"127.0.0.1","server_port":%d,"version":1}`, index, connectPort)
	}
	return document + `]}`
}

// w7DocumentServer serves one provider document and can hold the NEXT response, which is how the
// first build is kept inside its reservation while a second refresh arrives.
type w7DocumentServer struct {
	server   *httptest.Server
	access   sync.RWMutex
	document string
	holdNext atomic.Bool
	arrived  chan struct{}
	release  chan struct{}
}

func newW7DocumentServer(t *testing.T, document string) *w7DocumentServer {
	t.Helper()
	holder := &w7DocumentServer{document: document, arrived: make(chan struct{}, 4), release: make(chan struct{})}
	holder.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if holder.holdNext.Load() {
			select {
			case holder.arrived <- struct{}{}:
			default:
			}
			select {
			case <-holder.release:
			case <-time.After(20 * time.Second):
			}
		}
		holder.access.RLock()
		content := holder.document
		holder.access.RUnlock()
		_, _ = writer.Write([]byte(content))
	}))
	t.Cleanup(holder.server.Close)
	return holder
}

func (s *w7DocumentServer) setDocument(document string) {
	s.access.Lock()
	s.document = document
	s.access.Unlock()
}

// startW7UDPEcho is the real UDP business peer: it echoes every datagram.
func startW7UDPEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	address, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			read, from, readErr := conn.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = conn.WriteToUDP(buffer[:read], from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

// w7UDPBusinessRound sends one datagram through the PRODUCT's direct outbound and requires the echo.
func w7UDPBusinessRound(t *testing.T, object adapter.Outbound, destination *net.UDPAddr) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address, isAddress := netip.AddrFromSlice(destination.IP)
	require.True(t, isAddress)
	destinationAddress := M.SocksaddrFrom(address.Unmap(), uint16(destination.Port))
	packetConn, err := object.ListenPacket(ctx, destinationAddress)
	require.NoError(t, err)
	defer packetConn.Close()
	payload := []byte("x14-udp-business")
	// The product returns a real UDP packet connection. Go's UDP WriteTo accepts only a concrete
	// *net.UDPAddr; a sing Socksaddr would be rejected with EINVAL.
	_, err = packetConn.WriteTo(payload, destination)
	require.NoError(t, err)
	echo := make([]byte, 64)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	read, _, err := packetConn.ReadFrom(echo)
	require.NoError(t, err, "the UDP business flow must survive the pressure phase")
	require.Equal(t, string(payload), string(echo[:read]))
}

func TestX14PressureBuildGateURLTestPriorityAndLiveBusinessFlows(t *testing.T) {
	connectA := startW7ConnectProxy(t)
	connectB := startW7ConnectProxy(t)
	echoTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("echo-ok"))
	}))
	t.Cleanup(echoTarget.Close)

	holdStarted := make(chan struct{}, 4)
	holdRelease := make(chan struct{})
	var releaseHold sync.Once
	t.Cleanup(func() { releaseHold.Do(func() { close(holdRelease) }) })
	holdTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/probe":
			writer.WriteHeader(http.StatusNoContent)
		case "/hold":
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("hold-first\n"))
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case holdStarted <- struct{}{}:
			default:
			}
			select {
			case <-holdRelease:
			case <-time.After(30 * time.Second):
			}
			_, _ = writer.Write([]byte("hold-last\n"))
			if flusher != nil {
				flusher.Flush()
			}
		default:
			_, _ = writer.Write([]byte("echo-ok"))
		}
	}))
	t.Cleanup(holdTarget.Close)

	documentsA := newW7DocumentServer(t, w7ProviderDoc(connectA.port, 3))
	documentsB := newW7DocumentServer(t, w7ProviderDoc(connectB.port, 2))

	var pressure atomic.Int32
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.Level(pressure.Load())
	})
	// The embedder installs the reservation with the PRODUCTION shape (2 slots, 8 queued, 30 s wait)
	// and the Box keeps it: that is the documented contract for an embedder-owned gate, and it is
	// what lets this test read the same gate the providers use.
	gate := buildgate.NewWithPressure(resourceguard.Config{Limit: 2, Queue: 8, Wait: 30 * time.Second},
		func() resourceguard.Level { return resourceguard.Level(pressure.Load()) })
	ctx := buildgate.ContextWithGate(resourceguard.NewContext(crossSystemContext(t, t.TempDir(), nil), guard), gate)

	proxyPort := freeLoopbackPort(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [
    {"type": "remote", "tag": "slowA", "url": %q, "update_interval": "1h"},
    {"type": "remote", "tag": "slowB", "url": %q, "update_interval": "1h"}
  ],
  "outbounds": [
    {"type": "selector", "tag": "selected", "providers": ["slowA", "slowB"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}],
    "final": "direct"
  }
}`, proxyPort, documentsA.server.URL, documentsB.server.URL)

	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()

	installedGate := buildgate.FromContext(instance.ctx)
	require.NotNil(t, installedGate, "the Box must keep the embedder's build reservation")
	require.Same(t, gate, installedGate, "the providers build through the same reservation this test reads")
	coordinator := urltest.CoordinatorFromContext(instance.ctx)
	require.NotNil(t, coordinator, "the Box must install the URL-test coordinator")

	// First publication of both providers at LevelNormal: both builds admitted (limit 2).
	_, err := service.RefreshProviders(context.Background(), []string{"slowA", "slowB"}, 5*time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, loaded := boxInstance.Outbound().Outbound("slowA/n0")
		return loaded
	}, 10*time.Second, 10*time.Millisecond, "provider A's first document must be published")
	require.Equal(t, resourceguard.LevelNormal, gate.Level())
	require.Zero(t, gate.Stats().Inflight, "no build slot may stay taken after a refresh batch")

	// A real TCP business flow that stays open for the whole pressure/build phase.
	businessClient := newProxyClient(t, proxyPort)
	defer businessClient.CloseIdleConnections()
	holdResponse, holdErr := businessClient.Get(holdTarget.URL + "/hold")
	require.NoError(t, holdErr, "the streaming TCP business flow must start")
	defer holdResponse.Body.Close()
	select {
	case <-holdStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("the streaming business flow never reached the target through the provider node")
	}
	holdReader := newLineReader(holdResponse.Body)
	require.Equal(t, "hold-first\n", holdReader.nextLine(t))

	// A real UDP business flow through the product's direct outbound.
	directOutbound, loaded := boxInstance.Outbound().Outbound("direct")
	require.True(t, loaded)
	udpDestination := startW7UDPEcho(t)
	w7UDPBusinessRound(t, directOutbound, udpDestination)

	// ---- URLTest foreground/background under saturation, while the business flows are live. ----
	// Saturate the Box coordinator's 10 measurement slots.
	releases := make([]func(), 0, 10)
	for index := 0; index < 10; index++ {
		release, acquireErr := coordinator.Acquire(context.Background())
		require.NoError(t, acquireErr)
		releases = append(releases, release)
	}
	probeNode, loaded := boxInstance.Outbound().Outbound("slowA/n0")
	require.True(t, loaded)
	probeTarget := holdTarget.URL + "/probe"
	backgroundDone := make(chan error, 1)
	backgroundCtx := adapter.ContextWithBackgroundProbe(instance.ctx)
	go func() {
		_, measureErr := urltest.Measure(backgroundCtx, urltest.MeasureOptions{Link: probeTarget}, probeNode)
		backgroundDone <- measureErr
	}()
	// Give the background probe time to reach the queue, then start the foreground probe.
	time.Sleep(100 * time.Millisecond)
	foregroundDone := make(chan error, 1)
	go func() {
		_, measureErr := urltest.Measure(instance.ctx, urltest.MeasureOptions{Link: probeTarget}, probeNode)
		foregroundDone <- measureErr
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-foregroundDone:
		t.Fatalf("the foreground probe ran while every slot was held: %v", err)
	case err := <-backgroundDone:
		t.Fatalf("the background probe ran while every slot was held: %v", err)
	default:
	}
	// One slot frees: the FOREGROUND probe must be admitted ahead of the earlier background probe.
	releases[0]()
	select {
	case err := <-foregroundDone:
		require.NoError(t, err, "the foreground probe completes when a slot frees")
	case <-time.After(15 * time.Second):
		t.Fatal("the foreground probe starved behind queued background work")
	}
	select {
	case err := <-backgroundDone:
		t.Fatalf("the background probe overtook the foreground probe: %v", err)
	default:
	}
	for _, release := range releases[1:] {
		release()
	}
	select {
	case err := <-backgroundDone:
		require.NoError(t, err, "the background probe is aged in, not starved")
	case <-time.After(15 * time.Second):
		t.Fatal("the background probe never ran after the slots were released")
	}
	t.Log("X14 trigger: foreground probe admitted before the queued background probe; both completed")

	// ---- Pressure tightening + buildgate queueing, with both business flows still live. ----
	pressure.Store(int32(resourceguard.LevelCritical))
	require.Eventually(t, func() bool { return guard.Level() == resourceguard.LevelCritical }, 2*time.Second, time.Millisecond,
		"the guard must publish critical pressure")
	require.Equal(t, resourceguard.LevelCritical, gate.Level(), "the build reservation follows the same pressure fact")

	// Refresh A is admitted as the single critical build and parks inside the document download.
	documentsA.setDocument(w7ProviderDoc(connectA.port, 4))
	documentsA.holdNext.Store(true)
	refreshA := make(chan *ProviderRefreshSummary, 1)
	go func() {
		summary, refreshErr := service.RefreshProviders(context.Background(), []string{"slowA"}, 10*time.Second)
		if refreshErr != nil {
			refreshA <- nil
			return
		}
		refreshA <- summary
	}()
	select {
	case <-documentsA.arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("the first critical build never reached its document download")
	}
	require.Equal(t, 1, gate.Stats().Inflight, "exactly the one critical build slot is held")

	// Refresh B arrives while the only slot is held: it must be DEFERRED, retryable, and it must not
	// publish anything or park forever.
	deferredStart := time.Now()
	deferredSummary, deferredErr := service.RefreshProviders(context.Background(), []string{"slowB"}, 10*time.Second)
	deferredElapsed := time.Since(deferredStart)
	require.NoError(t, deferredErr)
	require.Len(t, deferredSummary.Results, 1)
	require.Equal(t, ProviderRefreshFailed, deferredSummary.Results[0].Status,
		"the second build must be reported as a deferred failure, not as a publish: %+v", deferredSummary.Results[0])
	require.Contains(t, deferredSummary.Results[0].Reason, "deferred",
		"the refusal must say it is the build reservation: %q", deferredSummary.Results[0].Reason)
	require.Less(t, deferredElapsed, 5*time.Second, "a deferral must fail fast instead of queueing forever")
	t.Logf("X14 trigger: build deferred in %v: %s", deferredElapsed, deferredSummary.Results[0].Reason)

	// Neither business flow was killed and no publish slipped through while the gate was closed.
	w7UDPBusinessRound(t, directOutbound, udpDestination)
	select {
	case <-holdStarted:
		t.Fatal("the streaming business flow restarted; it was killed instead of coexisting")
	default:
	}

	// Release the held download: the admitted build publishes, and the slot returns to zero.
	documentsA.holdNext.Store(false)
	close(documentsA.release)
	select {
	case summary := <-refreshA:
		require.NotNil(t, summary, "the admitted refresh must return a summary")
		require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status)
	case <-time.After(20 * time.Second):
		t.Fatal("the admitted build never finished")
	}
	require.Eventually(t, func() bool { return gate.Stats().Inflight == 0 }, 15*time.Second, time.Millisecond,
		"the build slot must be returned on the publish path (no permit leak)")
	_, publishedNewNode := boxInstance.Outbound().Outbound("slowA/n3")
	require.True(t, publishedNewNode, "the admitted build really published its new document")

	// The live streaming flow survived the whole critical window and still gets its tail.
	releaseHold.Do(func() { close(holdRelease) })
	require.Equal(t, "hold-last\n", holdReader.nextLine(t), "the live TCP flow was not killed by pressure or deferral")
	w7UDPBusinessRound(t, directOutbound, udpDestination)

	// ---- Recovery: no starvation. The deferred provider's retry succeeds once pressure falls. ----
	pressure.Store(int32(resourceguard.LevelNormal))
	require.Eventually(t, func() bool { return gate.Level() == resourceguard.LevelNormal }, 5*time.Second, time.Millisecond,
		"the reservation recovers after pressure falls")
	documentsB.setDocument(w7ProviderDoc(connectB.port, 3))
	recoveredSummary, recoveredErr := service.RefreshProviders(context.Background(), []string{"slowB"}, 10*time.Second)
	require.NoError(t, recoveredErr)
	require.Equal(t, ProviderRefreshPublished, recoveredSummary.Results[0].Status,
		"the deferred provider must publish on retry, not stay starved: %+v", recoveredSummary.Results[0])
	require.Eventually(t, func() bool { return gate.Stats().Inflight == 0 }, 10*time.Second, time.Millisecond,
		"no build slot may leak after recovery")

	// Close: every record belongs to a descriptor and is released by the scope drain.
	require.NoError(t, service.CloseService())
	closed := boxInstance.CatalogStats()
	require.True(t, closed.Closed)
	require.Zero(t, closed.Records, "every provider descriptor released its record")
	require.Zero(t, closed.Refs)
	require.Zero(t, gate.Stats().Inflight, "no build slot leaked across the whole test")
}

// w7LineReader reads one line at a time from a streaming body without buffering the rest.
type w7LineReader struct {
	buffer []byte
	body   io.Reader
}

func newLineReader(body io.Reader) *w7LineReader {
	return &w7LineReader{body: body}
}

func (r *w7LineReader) nextLine(t *testing.T) string {
	t.Helper()
	window := make([]byte, 1)
	for {
		read, err := r.body.Read(window)
		if read > 0 {
			r.buffer = append(r.buffer, window[:read]...)
			if window[0] == '\n' {
				line := string(r.buffer)
				r.buffer = nil
				return line
			}
			continue
		}
		if err != nil {
			t.Fatalf("streaming body ended before the next line: %q: %v", string(r.buffer), err)
		}
	}
}
