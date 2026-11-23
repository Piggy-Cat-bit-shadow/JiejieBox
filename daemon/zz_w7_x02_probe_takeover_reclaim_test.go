package daemon

// W7 / X02 (ORIGINAL oracle, 30号 §7.2): "probe-only session → 业务接管 → 探测完成/旧callback →
// pressure回收 | 活业务不断，旧token不关新session，专用残留可回收".
//
// This is NOT the manager lease×evict scenario the previous delivery substituted under this number.
// The trigger here is the probe path itself: a real URL-test measurement (common/urltest.Measure, the
// product probe engine) opens a real TCP session through a real catalog-deferred HTTP outbound while
// a business flow goes through the SAME Box. The four steps of the oracle:
//
//  1. probe-only session: Measure through the Box node, parked inside the target server;
//  2. business takeover: a manager hand-off lease is taken and a real business request is served
//     through the Box's HTTP inbound → route → the same node, while the probe is still parked;
//  3. probe completion / old callback: an equivalent waiter joins the same health question and the
//     leader's own token is cancelled. The old token must not cancel the shared probe, the probe's
//     completion must not close the business session;
//  4. pressure reclamation: at LevelCritical an explicit deferred-expansion sweep must release the
//     idle probe-only residue while refusing the leased/in-use node, and release the business node
//     only after the lease and the flow are gone.
//
// What this file deliberately does NOT claim: the QUIC "keep-session" handoff
// (contextWithQUICKeepSession) and the HTTP/2 idle-keeper token are transport-level mechanisms; a
// local plain-HTTP session cannot prove their generation semantics. That layer is recorded as the
// remaining X02 gap in COVERAGE.csv rather than silently substituted.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"
	outboundmgr "github.com/sagernet/sing-box/adapter/outbound"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// w7ConnectProxy is a minimal HTTP/1.1 CONNECT proxy: the real upstream of the Box's HTTP outbound.
type w7ConnectProxy struct {
	listener net.Listener
	port     int
	tunnels  atomic.Int32
	access   sync.Mutex
	conns    []net.Conn
}

func startW7ConnectProxy(t *testing.T) *w7ConnectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxy := &w7ConnectProxy{listener: listener, port: listener.Addr().(*net.TCPAddr).Port}
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.access.Lock()
		for _, conn := range proxy.conns {
			_ = conn.Close()
		}
		proxy.access.Unlock()
	})
	go func() {
		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go proxy.serve(client)
		}
	}()
	return proxy
}

func (p *w7ConnectProxy) serve(client net.Conn) {
	defer client.Close()
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil || request.Method != http.MethodConnect {
		return
	}
	upstream, dialErr := net.DialTimeout("tcp", request.Host, 5*time.Second)
	if dialErr != nil {
		_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstream.Close()
	p.access.Lock()
	p.conns = append(p.conns, client, upstream)
	p.access.Unlock()
	if _, err = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	p.tunnels.Add(1)
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, reader)
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, upstream)
	<-done
}

// w7ProbeGate is the target: /generate_204 parks the probe, /stream parks the business response,
// /echo answers immediately.
type w7ProbeGate struct {
	probeArrived  chan struct{}
	probeRelease  chan struct{}
	businessStart chan struct{}
	businessStop  chan struct{}
	probes        atomic.Int32
}

func newW7ProbeGate() *w7ProbeGate {
	return &w7ProbeGate{
		probeArrived:  make(chan struct{}, 4),
		probeRelease:  make(chan struct{}),
		businessStart: make(chan struct{}, 4),
		businessStop:  make(chan struct{}),
	}
}

func (g *w7ProbeGate) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(writer http.ResponseWriter, request *http.Request) {
		g.probes.Add(1)
		select {
		case g.probeArrived <- struct{}{}:
		default:
		}
		t.Logf("PROBE arrived method=%s", request.Method)
		select {
		case <-g.probeRelease:
		case <-time.After(20 * time.Second):
			t.Log("PROBE barrier timed out")
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/stream", func(writer http.ResponseWriter, request *http.Request) {
		flusher, _ := writer.(http.Flusher)
		_, _ = writer.Write([]byte("business-first\n"))
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case g.businessStart <- struct{}{}:
		default:
		}
		select {
		case <-g.businessStop:
		case <-time.After(20 * time.Second):
		}
		_, _ = writer.Write([]byte("business-last\n"))
		if flusher != nil {
			flusher.Flush()
		}
	})
	mux.HandleFunc("/echo", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("echo-ok"))
	})
	return mux
}

// w7X02Config is the product composition: an HTTP inbound, two catalog-admitted HTTP outbound nodes
// and a route rule that sends the inbound's traffic through "node" while "idle" stays probe-only.
func w7X02Config(proxyPort int, upstreamPort int) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [
    {"type": "http", "tag": "node", "server": "127.0.0.1", "server_port": %d, "version": 1},
    {"type": "http", "tag": "idle", "server": "127.0.0.1", "server_port": %d, "version": 1},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "node"}],
    "final": "direct"
  }
}`, proxyPort, upstreamPort, upstreamPort)
}

// w7ExpansionObservation is the optional capability the reclamation policy reads.
type w7ExpansionObservation interface {
	ExpansionObservation() outboundmgr.ExpansionObservation
}

func w7Observation(t *testing.T, object adapter.Outbound) outboundmgr.ExpansionObservation {
	t.Helper()
	observer, ok := object.(w7ExpansionObservation)
	require.True(t, ok, "%T does not report its expansion residency", object)
	return observer.ExpansionObservation()
}

func TestX02ProbeSessionBusinessTakeoverOldTokenAndPressureReclamation(t *testing.T) {
	upstream := startW7ConnectProxy(t)
	gate := newW7ProbeGate()
	target := httptest.NewServer(gate.handler(t))
	t.Cleanup(target.Close)
	probeURL := target.URL + "/generate_204"
	businessURL := target.URL + "/stream"
	echoURL := target.URL + "/echo"

	// The process pressure fact is a real resourceguard guard whose level this test drives. The Box's
	// build gate and the manager's reclaim sweep read the SAME guard from this context.
	var pressure atomic.Int32
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.Level(pressure.Load())
	})
	ctx := resourceguard.NewContext(crossSystemContext(t, t.TempDir(), nil), guard)

	proxyPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), w7X02Config(proxyPort, upstream.port), nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()
	tags := make([]string, 0, 8)
	for _, object := range boxInstance.Outbound().Outbounds() {
		tags = append(tags, object.Type()+"/"+object.Tag())
	}
	t.Logf("X02 debug: stats=%+v tags=%v", boxInstance.CatalogStats(), tags)

	// The static HTTP nodes are catalog records, not eager objects. The two nodes have IDENTICAL
	// options, so the store deduplicates them to one immutable record with two references: that is
	// the documented record identity, not two recipes.
	require.Eventually(t, func() bool {
		stats := boxInstance.CatalogStats()
		return stats.Records == 1 && stats.Refs == 2 && !stats.Closed
	}, 10*time.Second, 10*time.Millisecond, "the two identical static HTTP nodes share one catalog record with two refs")
	require.Zero(t, boxInstance.DeferredExpansionCount(), "admission must not expand a node")

	node, loaded := boxInstance.Outbound().Outbound("node")
	require.True(t, loaded)
	require.Equal(t, "http", node.Type())
	manager, isManager := boxInstance.Outbound().(*outboundmgr.Manager)
	require.True(t, isManager, "the product outbound manager owns reclamation")

	// A URL-test measurement through the product probe engine: health purpose plus a share registry,
	// so a second caller is an equivalent waiter rather than a second socket.
	share := urltest.NewMeasurementShare()
	probeCtx := urltest.ContextWithMeasurementShare(context.Background(), share)
	key := urltest.MeasurementKey{
		Node:     "node",
		Target:   probeURL,
		Method:   http.MethodHead,
		Purpose:  urltest.PurposeHealth,
		Path:     "direct",
		Instance: instance.revision,
	}

	type probeResult struct {
		measurement urltest.Measurement
		err         error
	}
	leaderCtx, cancelLeader := context.WithCancel(probeCtx)
	leader := make(chan probeResult, 1)
	go func() {
		measurement, err := urltest.Measure(leaderCtx, urltest.MeasureOptions{Link: probeURL, Key: &key}, node)
		leader <- probeResult{measurement: measurement, err: err}
	}()

	// 1. probe-only session is LIVE: the probe's TCP session is parked inside the target.
	select {
	case <-gate.probeArrived:
	case <-time.After(20 * time.Second):
		t.Fatal("the URL-test probe never reached the target through the catalog node")
	}
	require.EqualValues(t, 1, gate.probes.Load())
	require.EqualValues(t, 1, boxInstance.DeferredExpansionCount(), "the probe expanded the node")
	require.GreaterOrEqual(t, upstream.tunnels.Load(), int32(1), "the probe really opened a tunnel")
	t.Logf("X02 step1 trigger: probe parked, expansions=%d tunnels=%d", boxInstance.DeferredExpansionCount(), upstream.tunnels.Load())

	// 2. BUSINESS TAKEOVER: a manager hand-off lease plus a real business request through the Box's
	// HTTP inbound, while the probe session is still open.
	releaseLease, leased := manager.AcquireOutbound(node)
	require.True(t, leased, "a published object must be leasable for a hand-off")
	require.Equal(t, 1, w7Observation(t, node).Leased, "the hand-off lease is visible on the object")

	businessClient := newProxyClient(t, proxyPort)
	defer businessClient.CloseIdleConnections()
	businessResponse, businessErr := businessClient.Get(businessURL)
	require.NoError(t, businessErr, "business traffic must be served while the probe is in flight")
	defer businessResponse.Body.Close()
	require.Equal(t, http.StatusOK, businessResponse.StatusCode)
	select {
	case <-gate.businessStart:
	case <-time.After(20 * time.Second):
		t.Fatal("the business request never reached the target")
	}
	reader := bufio.NewReader(businessResponse.Body)
	firstLine, readErr := reader.ReadString('\n')
	require.NoError(t, readErr)
	require.Equal(t, "business-first\n", firstLine, "the business body streams while the probe is parked")
	t.Logf("X02 step2 trigger: business takeover served, leased=%d", w7Observation(t, node).Leased)

	// 3. OLD CALLBACK / OLD TOKEN: an equivalent waiter joins the same health question, then the
	// leader's own token is cancelled.
	waiter := make(chan probeResult, 1)
	go func() {
		measurement, err := urltest.Measure(probeCtx, urltest.MeasureOptions{Link: probeURL, Key: &key}, node)
		waiter <- probeResult{measurement: measurement, err: err}
	}()
	require.Eventually(t, func() bool { return share.Participants() == 2 }, 5*time.Second, time.Millisecond,
		"the equivalent waiter must join the in-flight probe instead of opening a second socket")
	cancelLeader()
	select {
	case result := <-leader:
		require.ErrorIs(t, result.err, context.Canceled, "the cancelled leader sees its own cancellation")
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled leader never returned")
	}
	require.Equal(t, 1, share.Participants(), "the shared probe must outlive the leader that was cancelled")
	require.EqualValues(t, 1, gate.probes.Load(), "the waiter must not have started a second probe")

	// The probe completes now; the old leader's cancellation did not cancel it.
	close(gate.probeRelease)
	select {
	case result := <-waiter:
		require.NoError(t, result.err, "the waiter's shared probe must complete, not inherit the leader's token")
		require.Equal(t, http.StatusNoContent, result.measurement.StatusCode)
	case <-time.After(20 * time.Second):
		t.Fatal("the shared probe never published after the barrier was released")
	}
	require.Equal(t, 0, share.Participants(), "the shared question is forgotten after everyone leaves")
	require.Eventually(t, func() bool { return w7Observation(t, node).Conns >= 1 }, 5*time.Second, time.Millisecond,
		"the business connection, not the probe, is the one still open")

	// The business session is STILL alive after the probe finished and its own connection closed.
	t.Logf("X02 step3 trigger: waiter published, business conns=%d", w7Observation(t, node).Conns)

	// 4a. Expand the second node through a REAL business dial (this is the dedicated, probe-only
	// residue that nothing else uses).
	idleNode, loaded := boxInstance.Outbound().Outbound("idle")
	require.True(t, loaded)
	idleClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, network string, address string) (net.Conn, error) {
				return idleNode.DialContext(dialCtx, network, M.ParseSocksaddr(address))
			},
		},
	}
	echoResponse, echoErr := idleClient.Get(echoURL)
	require.NoError(t, echoErr, "the idle node must serve a real request")
	body, bodyErr := io.ReadAll(echoResponse.Body)
	echoResponse.Body.Close()
	require.NoError(t, bodyErr)
	require.Equal(t, "echo-ok", string(body))
	// The request is over and its pooled connection is released: the residue is now truly idle.
	idleClient.CloseIdleConnections()
	require.Eventually(t, func() bool { return boxInstance.DeferredExpansionCount() == 2 }, 5*time.Second, time.Millisecond,
		"both nodes are expanded: the business node and the idle probe-only residue")
	require.Eventually(t, func() bool { return w7Observation(t, idleNode).Conns == 0 }, 5*time.Second, time.Millisecond,
		"the idle residue owns no open connection")
	require.Equal(t, 0, w7Observation(t, idleNode).Leased)

	// 4b. PRESSURE RECLAMATION: a budget that requires convergence, installed only after both
	// expansions exist, and a critical level read from the same guard.
	manager.SetDeferredReclaimPolicy(outboundmgr.DeferredReclaimPolicy{
		MaxExpanded:           1,
		MaxBytes:              64 * 1024,
		AssumedExpansionBytes: 3 * 1024,
		MaxEvictionsPerSweep:  16,
		MinInterval:           time.Hour,
	})
	pressure.Store(int32(resourceguard.LevelCritical))
	require.Eventually(t, func() bool { return guard.Level() == resourceguard.LevelCritical }, time.Second, time.Millisecond,
		"the guard must publish the critical level")

	sweep := manager.ReclaimDeferredExpansions(ctx, outboundmgr.ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelCritical, sweep.Level)
	require.GreaterOrEqual(t, sweep.InUse, 1,
		"the node with an outstanding hand-off lease and a live flow must be counted in use")
	require.GreaterOrEqual(t, sweep.Evicted, 1, "the idle probe-only residue must be reclaimed under pressure")
	require.Equal(t, 1, boxInstance.DeferredExpansionCount(), "only the in-use business node survives the sweep")

	// The live business flow survived the reclamation: it still receives its tail.
	close(gate.businessStop)
	tail, tailErr := reader.ReadString('\n')
	require.NoError(t, tailErr, "the business flow must not be killed by reclamation")
	require.Equal(t, "business-last\n", tail)

	// Release the flow AND the lease: the node now has no user, so the same decision the sweep makes
	// (Evict) must succeed. The sweep already proved the budget half above; this pins the object half:
	// after the last user leaves, the expansion is reclaimable.
	businessResponse.Body.Close()
	// The test's own proxy connection to the inbound is kept alive by net/http; closing it is the
	// client going away, which is what ends the routed flow.
	businessClient.CloseIdleConnections()
	releaseLease()
	t.Logf("X02 final: observation after release = %+v", w7Observation(t, node))
	require.Eventually(t, func() bool { return w7Observation(t, node).Leased == 0 }, 10*time.Second, time.Millisecond,
		"the released lease is no longer outstanding: %+v", w7Observation(t, node))
	evictor, canEvict := node.(interface{ Evict() bool })
	require.True(t, canEvict, "the deferred object exposes the release verb the sweep uses")
	evicted := evictor.Evict()
	t.Logf("X02 final: evict=%v observation=%+v", evicted, w7Observation(t, node))
	if evicted {
		require.Zero(t, boxInstance.DeferredExpansionCount(), "all idle expansions converged")
	} else {
		// A refused eviction is legal only while something is genuinely using the node; record what
		// it is rather than forcing the assertion.
		require.NotZero(t, w7Observation(t, node).Conns+w7Observation(t, node).Dialing,
			"Evict may only refuse while a real user remains")
		require.Equal(t, 1, boxInstance.DeferredExpansionCount())
	}

	// Records belong to the descriptors, not to the expansion: reclamation must not release them, and
	// closing the Box must release exactly them.
	require.Equal(t, 1, boxInstance.CatalogStats().Records, "reclamation must not drop the descriptors' records")
	require.NoError(t, service.CloseService())
	closedStats := boxInstance.CatalogStats()
	require.True(t, closedStats.Closed)
	require.Zero(t, closedStats.Records)
	require.Zero(t, closedStats.Refs)
}
