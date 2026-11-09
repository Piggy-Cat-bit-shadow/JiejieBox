package libbox

// Section 5 scale — a REAL Box with two remote providers and two URLTest groups over thousands of
// nodes, driven through the real service.
//
// The component-level budget tests use a synthetic registry; this one exercises the actual provider
// fetch, node publication, group membership and health round for 2,000 and 10,000 nodes. It is
// opt-in (SING_BOX_SCALE_TEST) because a normal package run must stay fast; the commands that
// produced the recorded numbers are in the report.
//
// The provider members are `http` proxies pointing back at the test server: a plain `direct` member
// is not accepted inside a provider document, and an http proxy lets every probe be counted without
// resolving any name (the group URL is an absolute URL the test server answers directly).

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scaleProviderServer serves two provider documents and counts health probes.
type scaleProviderServer struct {
	server    *httptest.Server
	probes    atomic.Int32
	documents map[string]string
}

func newScaleProviderServer(t *testing.T, nodes int) *scaleProviderServer {
	t.Helper()
	scale := &scaleProviderServer{documents: map[string]string{}}
	scale.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if document, loaded := scale.documents[strings.TrimPrefix(request.URL.Path, "/")]; loaded && !request.URL.IsAbs() {
			_, _ = writer.Write([]byte(document))
			return
		}
		if request.Method != http.MethodConnect {
			http.NotFound(writer, request)
			return
		}
		// The HTTP client tunnels the probe with CONNECT; answer the tunnel, read the request
		// inside it, and answer that. One CONNECT is one probe.
		scale.probes.Add(1)
		hijacker, canHijack := writer.(http.Hijacker)
		if !canHijack {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		_, _ = http.ReadRequest(buffered.Reader)
		_, _ = buffered.WriteString("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")
		_ = buffered.Flush()
	}))
	t.Cleanup(scale.server.Close)

	port := scale.server.Listener.Addr().(*net.TCPAddr).Port
	buildDocument := func(prefix string, count int) string {
		var builder strings.Builder
		builder.WriteString(`{"outbounds":[`)
		for index := 0; index < count; index++ {
			if index > 0 {
				builder.WriteByte(',')
			}
			fmt.Fprintf(&builder, `{"type":"http","tag":"%s%d","server":"127.0.0.1","server_port":%d}`, prefix, index, port)
		}
		builder.WriteString(`]}`)
		return builder.String()
	}
	half := nodes / 2
	scale.documents["p1"] = buildDocument("a", half)
	scale.documents["p2"] = buildDocument("b", nodes-half)
	return scale
}

// providerScaleConfig wires both providers into both groups.
func providerScaleConfig(providerBase string) string {
	return fmt.Sprintf(`{"log":{"level":"error"},
		"providers":[
			{"type":"remote","tag":"p1","url":%q,"update_interval":"1h"},
			{"type":"remote","tag":"p2","url":%q,"update_interval":"1h"}],
		"outbounds":[
			{"type":"direct","tag":"direct"},
			{"type":"urltest","tag":"g1","providers":["p1","p2"],"url":"http://probe.invalid/generate_204","interval":"10m"},
			{"type":"urltest","tag":"g2","providers":["p1","p2"],"url":"http://probe.invalid/generate_204","interval":"10m"}],
		"route":{"final":"direct"}}`,
		providerBase+"/p1", providerBase+"/p2")
}

// TestRealBoxProviderScale is the recorded 2,000 / 10,000-node run.
func TestRealBoxProviderScale(t *testing.T) {
	if os.Getenv("SING_BOX_SCALE_TEST") == "" {
		t.Skip("opt-in: set SING_BOX_SCALE_TEST=1 (SING_BOX_SCALE_NODES defaults to 2000)")
	}
	nodes := 2000
	if raw := os.Getenv("SING_BOX_SCALE_NODES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		nodes = parsed
	}
	if nodes < 2 || nodes%2 != 0 {
		t.Fatalf("node count must be even and >= 2, got %d", nodes)
	}

	scale := newScaleProviderServer(t, nodes)
	server := startDiagnoseTestCommandServer(t, providerScaleConfig(scale.server.URL))

	// Provider documents are fetched by the service; both groups then receive all nodes. A large
	// document is still being parsed by the provider's own startup refresh when the manual call
	// arrives, which answers "busy" instead of waiting, so poll until it publishes.
	refreshUntilPublished := func(providerTag string) {
		deadline := time.Now().Add(300 * time.Second)
		for {
			summary, err := server.RefreshProviders(providerTag, 60000)
			require.NoError(t, err, "refresh %s", providerTag)
			if strings.Contains(summary.Value, `"status":"published"`) {
				t.Logf("refresh %s: %s", providerTag, summary.Value)
				return
			}
			if strings.Contains(summary.Value, `"status":"busy"`) && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			t.Fatalf("refresh %s: %s", providerTag, summary.Value)
		}
	}
	refreshUntilPublished("p1")
	refreshUntilPublished("p2")

	// The refresh callbacks rebuild each group's inner member set. The round's result is the
	// authoritative membership check: it contains one entry per measured provider node.
	time.Sleep(500 * time.Millisecond)
	type roundRunner interface {
		URLTest(context.Context) (map[string]uint16, error)
	}
	instance := server.Instance()
	require.NotNil(t, instance)
	runners := make([]roundRunner, 2)
	for index, tag := range []string{"g1", "g2"} {
		outbound, loaded := instance.Box().Outbound().Outbound(tag)
		require.True(t, loaded)
		runner, isRunner := outbound.(roundRunner)
		require.True(t, isRunner)
		runners[index] = runner
	}

	// Manual round on both groups at once: the "manual load" half of the requirement.
	started := time.Now()
	results := make([]map[string]uint16, 2)
	errors := make([]error, 2)
	var group sync.WaitGroup
	for index := range runners {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			results[index], errors[index] = runners[index].URLTest(context.Background())
		}()
	}
	group.Wait()
	elapsed := time.Since(started)
	for index := range errors {
		require.NoError(t, errors[index], "group %d", index+1)
		t.Logf("group %d result=%d probes=%d", index+1, len(results[index]), scale.probes.Load())
		require.Len(t, results[index], nodes, "group %d must report every member", index+1)
	}
	probes := scale.probes.Load()
	require.GreaterOrEqual(t, probes, int32(nodes), "every node must have been probed at least once")
	// A measurement is two CONNECTs (warm-up + timed). Two groups sharing therefore cost about\n	// 2*nodes, while NOT sharing would cost 4*nodes; the extra allowance covers the startup rounds\n	// that run while provider nodes are published. 3*nodes still distinguishes the two.\n	require.LessOrEqual(t, probes, int32(3*nodes), "probes must stay near one shared measurement per node")
	t.Logf("nodes=%d groups=2 each_result=%d probes=%d elapsed=%s", nodes, len(results[0]), probes, elapsed.Round(time.Millisecond))
}
