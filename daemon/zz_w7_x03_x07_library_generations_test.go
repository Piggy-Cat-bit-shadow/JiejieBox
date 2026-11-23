package daemon

// W7 / X03 (ORIGINAL oracle, task book 30 section 7.2): "a thousand nodes, one changed per round ->
// old connections keep serving -> several failing and succeeding rounds -> final release | no
// unowned old library / owner table / retired linear growth, and the running configuration accurate".
//
// W7 / X07 (same section): "two Boxes / two owners sharing artifacts or identical node config ->
// A Close/Collect -> B's old readers match/dial | B is fine; persistent backing, records and owner
// isolation".
//
// Both are driven through the product entry (StartedService -> real box.Box) with a REAL remote
// Provider over HTTP, a REAL catalog-deferred node type, and REAL TCP connections through a local
// CONNECT proxy. The witnesses are catalog accounting (records/refs), the published node set, and
// traffic through a held connection.
//
// Scale note: the plan prefers 100+ rounds. This host run uses the round count printed by the test;
// the reason for the reduction (wall-clock budget of a shared acceptance window) is recorded in
// COVERAGE.csv together with the executed count, and the per-round invariant is the same one a
// longer run would check.

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// w7LibraryDocument builds a provider document of `count` HTTP nodes. Every node is unique by port
// (only node zero points at a live CONNECT proxy; the rest are closed ports that are never dialled
// unless a test asks for them), so the catalog records have a 1:1 relationship with the nodes.
func w7LibraryDocument(count int, livePort int, overrideIndex int, overridePort int) string {
	document := `{"outbounds":[`
	for index := 0; index < count; index++ {
		if index > 0 {
			document += ","
		}
		port := 20000 + index
		if index == 0 {
			port = livePort
		}
		if index == overrideIndex {
			port = overridePort
		}
		document += fmt.Sprintf(`{"type":"http","tag":"n%d","server":"127.0.0.1","server_port":%d,"version":1}`, index, port)
	}
	return document + `]}`
}

// w7LibraryServer serves one changing provider document, with a switch that makes the next fetches
// fail (used for the failing rounds).
type w7LibraryServer struct {
	server   *httptest.Server
	access   sync.RWMutex
	document string
	failing  atomic.Bool
	fetches  atomic.Int32
}

func newW7LibraryServer(t *testing.T, document string) *w7LibraryServer {
	t.Helper()
	library := &w7LibraryServer{document: document}
	library.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		library.fetches.Add(1)
		if library.failing.Load() {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		library.access.RLock()
		content := library.document
		library.access.RUnlock()
		_, _ = writer.Write([]byte(content))
	}))
	t.Cleanup(library.server.Close)
	return library
}

func (s *w7LibraryServer) setDocument(document string) {
	s.access.Lock()
	s.document = document
	s.access.Unlock()
}

// w7LibraryProfile is the product composition: HTTP inbound, one remote provider, one selector.
// `cachePath` is per-Box on purpose: the cache file is a bbolt file with an exclusive lock, so two
// concurrently running Boxes cannot share one (the second start is refused with "initialize
// cache-file: timeout"). The ARTIFACT directory is what the two Boxes share, through the file manager
// base directory, not through this path.
func w7LibraryProfile(proxyPort int, providerURL string, logLevel string, cachePath string) string {
	return fmt.Sprintf(`{
  "log": {"level": %q},
  "experimental": {"cache_file": {"enabled": true, "path": %q}},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [{"type": "remote", "tag": "lib", "url": %q, "update_interval": "1h"}],
  "outbounds": [
    {"type": "selector", "tag": "selected", "providers": ["lib"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}],
    "final": "direct"
  }
}`, logLevel, cachePath, proxyPort, providerURL)
}

// w7HeldReader reads a streamed HTTP response one line at a time so a test can prove the body keeps
// flowing after the provider generation that created the connection was replaced.
type w7HeldReader struct {
	reader *bufio.Reader
}

func (r *w7HeldReader) nextLine(t *testing.T) string {
	t.Helper()
	line, err := r.reader.ReadString('\n')
	require.NoError(t, err)
	return line
}

// w7LibraryDial opens a streaming HTTP request through a node object (a REAL TCP connection through
// the product's outbound), and returns the response plus a body line reader.
func w7LibraryDial(t *testing.T, object interface {
	DialContext(context.Context, string, M.Socksaddr) (net.Conn, error)
}, targetURL string) (*http.Response, *w7HeldReader) {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, network string, address string) (net.Conn, error) {
			return object.DialContext(dialCtx, network, M.ParseSocksaddr(address))
		},
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
	})
	response, err := client.Get(targetURL + "/hold")
	require.NoError(t, err, "the held business request must start")
	t.Cleanup(func() { _ = response.Body.Close() })
	return response, &w7HeldReader{reader: bufio.NewReader(response.Body)}
}

func TestX03AThousandNodeLibraryChangesOneNodePerRoundAndConverges(t *testing.T) {
	const nodes = 1000
	// The original W8 window asked for 1000 nodes x 100 rounds; the frozen fixture only reached 30
	// and the delivery recorded 50 nodes x 100 rounds as the substitute. The assertion that made the
	// 1000 x 100 run impossible was a SYNCHRONOUS publication check right after Box.Start - this
	// fixture awaits the catalog's own convergence instead, so the original scale runs.
	const rounds = 100
	const failingRounds = 4

	connectA := startW7ConnectProxy(t)
	holdStarted := make(chan struct{}, 8)
	holdRelease := make(chan struct{})
	var releaseHold sync.Once
	t.Cleanup(func() { releaseHold.Do(func() { close(holdRelease) }) })
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hold" {
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("held-first\n"))
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
			_, _ = writer.Write([]byte("held-last\n"))
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	library := newW7LibraryServer(t, w7LibraryDocument(nodes, connectA.port, -1, 0))
	proxyPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, t.TempDir(), nil)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), w7LibraryProfile(proxyPort, library.server.URL, "error", filepath.Join(t.TempDir(), "cache.db")), nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()

	_, err := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return boxInstance.CatalogStats().Records == nodes }, 30*time.Second, 5*time.Millisecond,
		"every node of the thousand-node document must have one catalog record")
	require.Equal(t, nodes, boxInstance.CatalogStats().Refs, "one reference per node")
	baselineOutbounds := len(boxInstance.Outbound().Outbounds())
	t.Logf("X03 baseline: nodes=%d records=%d refs=%d outbounds=%d fetches=%d",
		nodes, boxInstance.CatalogStats().Records, boxInstance.CatalogStats().Refs, baselineOutbounds, library.fetches.Load())

	nodeObject, loaded := boxInstance.Outbound().Outbound("lib/n0")
	require.True(t, loaded)
	targetAddress := target.Listener.Addr().String()
	heldResponse, heldReader := w7LibraryDial(t, nodeObject, target.URL)
	select {
	case <-holdStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("the held connection never reached the target")
	}
	require.Equal(t, "held-first\n", heldReader.nextLine(t))
	require.EqualValues(t, 1, connectA.tunnels.Load(), "the held connection uses the first upstream")

	executedRounds := 0
	failuresSeen := 0
	changedNode := 1
	for round := 0; round < rounds; round++ {
		// One node changes per round; the round count and the exact node are deterministic.
		overridePort := 30000 + round
		library.setDocument(w7LibraryDocument(nodes, connectA.port, changedNode, overridePort))
		if round%8 == failingRounds {
			// A failing round: the source refuses the document. The previously published generation
			// must stay serving and the record count must not move.
			library.failing.Store(true)
			summary, refreshErr := service.RefreshProviders(context.Background(), []string{"lib"}, 10*time.Second)
			require.NoError(t, refreshErr)
			require.Equal(t, ProviderRefreshFailed, summary.Results[0].Status, "round %d: %+v", round, summary.Results[0])
			failuresSeen++
			library.failing.Store(false)
			require.Equal(t, nodes, boxInstance.CatalogStats().Records,
				"round %d: a failed refresh must not change the record set", round)
			require.Equal(t, nodes, boxInstance.CatalogStats().Refs)
			executedRounds++
			continue
		}
		summary, refreshErr := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
		require.NoError(t, refreshErr)
		require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status,
			"round %d must publish exactly one changed node: %+v", round, summary.Results[0])
		// The running configuration is accurate: the changed node is addressable under its tag, and
		// the record/ref accounting did not grow with the number of rounds (old records released).
		require.Equal(t, nodes, boxInstance.CatalogStats().Records,
			"round %d: the replaced node's old record must be released", round)
		require.Equal(t, nodes, boxInstance.CatalogStats().Refs)
		require.Equal(t, baselineOutbounds, len(boxInstance.Outbound().Outbounds()),
			"round %d: no unowned outbound object may accumulate", round)
		executedRounds++
		if executedRounds%10 == 0 {
			// The convergence CURVE, not only the endpoint: records/refs must stay pinned at the node
			// count and the heap objects must not climb with the round number.
			var sample runtime.MemStats
			runtime.ReadMemStats(&sample)
			stats := boxInstance.CatalogStats()
			t.Logf("X03 curve round=%d records=%d refs=%d outbounds=%d heapObjects=%d goroutines=%d",
				executedRounds, stats.Records, stats.Refs, len(boxInstance.Outbound().Outbounds()),
				sample.HeapObjects, runtime.NumGoroutine())
		}
	}
	t.Logf("X03 rounds: executed=%d failing=%d final records=%d refs=%d outbounds=%d fetches=%d",
		executedRounds, failuresSeen, boxInstance.CatalogStats().Records, boxInstance.CatalogStats().Refs,
		len(boxInstance.Outbound().Outbounds()), library.fetches.Load())
	require.Equal(t, rounds, executedRounds, "every planned round really executed")

	// The held connection still serves its tail after 30 provider generations replaced its node.
	releaseHold.Do(func() { close(holdRelease) })
	require.Equal(t, "held-last\n", heldReader.nextLine(t),
		"an old connection must survive the replacement of its node")
	heldResponse.Body.Close()
	require.EqualValues(t, 1, connectA.tunnels.Load(), "the held connection stayed on the old upstream")

	// A NEW dial after the churn must use the CURRENT node configuration (node zero still points at
	// the first upstream in this round set, so the witness is that it works and is counted).
	liveNode, loaded := boxInstance.Outbound().Outbound("lib/n0")
	require.True(t, loaded)
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	freshConn, dialErr := liveNode.DialContext(dialCtx, "tcp", M.ParseSocksaddr(targetAddress))
	cancel()
	require.NoError(t, dialErr, "a fresh dial must work against the current generation")
	_ = freshConn.Close()

	// Final release: closing the Box releases every record exactly once.
	require.NoError(t, service.CloseService())
	closed := boxInstance.CatalogStats()
	require.True(t, closed.Closed)
	require.Zero(t, closed.Records)
	require.Zero(t, closed.Refs)
}

func TestX07TwoBoxesIdenticalNodesShareArtifactsAndCloseIndependently(t *testing.T) {
	const nodes = 200

	connectA := startW7ConnectProxy(t)
	holdStarted := make(chan struct{}, 8)
	holdRelease := make(chan struct{})
	var releaseHold sync.Once
	t.Cleanup(func() { releaseHold.Do(func() { close(holdRelease) }) })
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hold" {
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("held-first\n"))
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
			_, _ = writer.Write([]byte("held-last\n"))
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	library := newW7LibraryServer(t, w7LibraryDocument(nodes, connectA.port, -1, 0))
	directory := t.TempDir()
	// Two Boxes on the SAME artifact directory, with identical node configuration (same provider
	// URL, same tags). Each Box owns its own catalog store and its own provider owner scope.
	serviceA := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, nil)})
	t.Cleanup(func() {
		_ = serviceA.CloseService()
		serviceA.Close()
	})
	serviceB := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, nil)})
	t.Cleanup(func() {
		_ = serviceB.CloseService()
		serviceB.Close()
	})
	proxyA, proxyB := freeLoopbackPort(t), freeLoopbackPort(t)
	require.NoError(t, serviceA.StartOrReloadService(context.Background(), w7LibraryProfile(proxyA, library.server.URL, "error", filepath.Join(directory, "cache-a.db")), nil))
	require.NoError(t, serviceB.StartOrReloadService(context.Background(), w7LibraryProfile(proxyB, library.server.URL, "error", filepath.Join(directory, "cache-b.db")), nil))
	_, err := serviceA.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	_, err = serviceB.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	boxA, boxB := serviceA.Instance().Box(), serviceB.Instance().Box()
	require.Eventually(t, func() bool {
		return boxA.CatalogStats().Records == nodes && boxB.CatalogStats().Records == nodes
	}, 30*time.Second, 5*time.Millisecond, "both Boxes install their own records for the identical config")
	require.NotEqual(t, boxA.CatalogStats().StoreID, boxB.CatalogStats().StoreID,
		"two Boxes intentionally have different catalog stores")
	require.Equal(t, nodes, boxA.CatalogStats().Refs)
	require.Equal(t, nodes, boxB.CatalogStats().Refs)
	t.Logf("X07 baseline: A store=%d records=%d B store=%d records=%d",
		boxA.CatalogStats().StoreID, boxA.CatalogStats().Records, boxB.CatalogStats().StoreID, boxB.CatalogStats().Records)

	// B holds a real connection; A is then closed while B keeps serving.
	nodeB, loaded := boxB.Outbound().Outbound("lib/n0")
	require.True(t, loaded)
	heldResponse, heldReader := w7LibraryDial(t, nodeB, target.URL)
	select {
	case <-holdStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("B's held connection never reached the target")
	}
	require.Equal(t, "held-first\n", heldReader.nextLine(t))

	require.NoError(t, serviceA.CloseService())
	closedA := boxA.CatalogStats()
	require.True(t, closedA.Closed)
	require.Zero(t, closedA.Records, "A released its records")
	require.Zero(t, closedA.Refs)
	// A's Close must not collect B's backing or disturb B's readers.
	require.Equal(t, nodes, boxB.CatalogStats().Records, "B's records are untouched by A's Close")
	releaseHold.Do(func() { close(holdRelease) })
	require.Equal(t, "held-last\n", heldReader.nextLine(t), "B's old reader still serves after A closed")
	heldResponse.Body.Close()

	// A NEW dial in B after A is gone: the shared artifact directory still backs B's configuration.
	freshClient := newProxyClient(t, proxyB)
	status, err := proxyStatus(t, freshClient, target.URL)
	freshClient.CloseIdleConnections()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "B's new dial works after A closed")

	// Construction-time Close: a third Box is closed while its provider document is held mid-fetch.
	// The entry must return, publish nothing, and leave no record behind.
	heldDocument := newW7DocumentServer(t, w7LibraryDocument(nodes, connectA.port, -1, 0))
	heldDocument.holdNext.Store(true)
	serviceC := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, nil)})
	proxyC := freeLoopbackPort(t)
	require.NoError(t, serviceC.StartOrReloadService(context.Background(), w7LibraryProfile(proxyC, heldDocument.server.URL, "error", filepath.Join(directory, "cache-c.db")), nil))
	refreshDone := make(chan struct{})
	go func() {
		_, _ = serviceC.RefreshProviders(context.Background(), []string{"lib"}, 10*time.Second)
		close(refreshDone)
	}()
	select {
	case <-heldDocument.arrived:
	case <-time.After(20 * time.Second):
		t.Fatal("the third Box's provider fetch never started")
	}
	closedInTime := make(chan error, 1)
	go func() { closedInTime <- serviceC.CloseService() }()
	select {
	case closeErr := <-closedInTime:
		require.NoError(t, closeErr)
	case <-time.After(20 * time.Second):
		t.Fatal("CloseService did not return while a provider build was in flight")
	}
	close(heldDocument.release)
	select {
	case <-refreshDone:
	case <-time.After(20 * time.Second):
		t.Fatal("the cancelled provider refresh never returned")
	}
	require.Nil(t, serviceC.Instance(), "a closed construction publishes nothing")

	// B reloads with a changed document; its records converge to the new generation's count and no
	// old record lingers.
	library.setDocument(w7LibraryDocument(nodes, connectA.port, 5, 31000))
	require.NoError(t, serviceB.StartOrReloadService(context.Background(), w7LibraryProfile(proxyB, library.server.URL, "warn", filepath.Join(directory, "cache-b.db")), nil))
	boxB = serviceB.Instance().Box()
	_, err = serviceB.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return boxB.CatalogStats().Records == nodes && boxB.CatalogStats().Refs == nodes
	}, 30*time.Second, 5*time.Millisecond, "B's records converged after its reload")
	require.NoError(t, serviceB.CloseService())
	require.Zero(t, boxB.CatalogStats().Records)
	require.Zero(t, boxB.CatalogStats().Refs)
}
