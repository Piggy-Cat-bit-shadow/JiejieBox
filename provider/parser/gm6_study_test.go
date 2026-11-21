package parser_test

// GM-6 (config-processing copies and refresh peaks) measurement harness.
//
// # Why this lives here and why it is opt-in
//
// The provider refresh pipeline is `document -> ParseSubscription -> UpdateNodes -> publication`.
// The parser package is the write boundary of this task, so the harness is a test of this package
// that drives a real `adapter/provider` adapter over a real `adapter/outbound` manager with one
// registered node type. It is opt-in (`-gm6study`) because a peak study is not a unit test: it
// starts a sampler, generates megabyte fixtures and is only meaningful in its own process.
//
// # What is measured, exactly
//
// `runtime.ReadMemStats` is sampled from a goroutine while the work runs. HeapAlloc is the number
// the Go runtime itself reports as allocated heap (live objects plus not-yet-collected garbage), so
// its maximum over the run is the peak this process actually reached. The sampler period is 500缂備胶鍋撳姗€顢?
// a spike fully contained between two samples AND followed by a collection is missed, which is why
// every phase also records its cumulative TotalAlloc delta as an upper bound, and why `-gm6gc off`
// runs the same phase with the collector disabled (then HeapAlloc at the phase boundary IS the
// whole cumulative allocation of the phase).
//
// # What the node payload is, and is not
//
// Nodes are real `option.TrojanOutboundOptions` documents (the same shape a subscription carries),
// but the registered constructor is a stub that keeps a value copy of the options instead of
// building TLS/dialer state. The four-mode numbers therefore describe the refresh WORKSPACE plus
// the applied option snapshot; the derived per-node runtime is reported separately from GM-1/GM-4
// and is identical in both the old and the new code, so it cancels in the before/after comparison.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	providerAdapter "github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

var (
	gm6Mode       = flag.String("gm6study", "", "GM-6 study: cold | unchanged | small | full | layers | held")
	gm6Nodes      = flag.Int("gm6nodes", 1000, "GM-6 study: nodes in the fixture document")
	gm6GC         = flag.String("gm6gc", "on", "GM-6 study: on (collector runs) | off (collector disabled during the measured phase)")
	gm6Repeat     = flag.Int("gm6repeat", 1, "GM-6 study: repetitions inside one process (each reported separately)")
	gm6Keep       = flag.Bool("gm6keep", false, "GM-6 study: print a human-readable phase table to stderr as well")
	gm6Changed    = flag.Int("gm6changed", 10, "GM-6 study: nodes changed by the 'small' mode")
	gm6SamplerUs  = flag.Int("gm6sampler-us", 500, "GM-6 study: sampler period in microseconds")
	gm6DocumentKB = flag.Int("gm6doc-kb", 0, "GM-6 study: extra padding appended to a document in KB (lifetime probe)")
)

// studyNodeOptions is the registered option type. Embedding the real Trojan options keeps the
// document shape, field set and option sizes identical to a product subscription.
type studyNodeOptions struct {
	option.TrojanOutboundOptions
}

// studyNode is the stub runtime object. A value copy of the options models a node that derived its
// own state from the configuration, without needing TLS or a live dialer.
type studyNode struct {
	outbound.Adapter
	options studyNodeOptions
}

func (n *studyNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("study stub does not dial")
}

func (n *studyNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("study stub does not listen")
}

type studyHarness struct {
	ctx      context.Context
	registry *outbound.Registry
	manager  *outbound.Manager
	provider *providerAdapter.Adapter
	logger   log.ContextLogger
}

func newStudyHarness(t testing.TB) *studyHarness {
	t.Helper()
	registry := outbound.NewRegistry()
	outbound.Register[studyNodeOptions](registry, "trojan", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options studyNodeOptions) (adapter.Outbound, error) {
		return &studyNode{Adapter: outbound.NewAdapter("trojan", tag, []string{"tcp"}, nil), options: options}, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	ctx = service.ContextWith[adapter.OutboundRegistry](ctx, registry)
	history := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	p := providerAdapter.NewAdapter(ctx, nil, manager, factory, logger, "study", "remote", option.ProviderHealthCheckOptions{})
	if err := p.Start(); err != nil {
		t.Fatalf("start the study provider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return &studyHarness{ctx: ctx, registry: registry, manager: manager, provider: &p, logger: logger}
}

func (h *studyHarness) publish(t testing.TB, document string) []option.Outbound {
	t.Helper()
	outbounds, err := parser.ParseSubscription(h.ctx, document, nil, "study")
	if err != nil {
		t.Fatalf("parse the initial document: %v", err)
	}
	if err := h.provider.UpdateNodes(outbounds); err != nil {
		t.Fatalf("publish the initial document: %v", err)
	}
	if err := h.provider.UpdateGroups(); err != nil {
		t.Fatalf("publish the initial groups: %v", err)
	}
	return outbounds
}

// studyDocument renders the same shape a native sing-box subscription export has: a top-level
// object whose `outbounds` array carries real Trojan nodes.
func studyDocument(nodes int, revision int, changed int) string {
	builder := &strings.Builder{}
	builder.Grow(64 + nodes*190)
	builder.WriteString(`{"outbounds":[`)
	for index := 0; index < nodes; index++ {
		if index > 0 {
			builder.WriteString(`,`)
		}
		serverPort := 20000 + index%1000
		password := "study-" + strconv.Itoa(revision)
		if changed > 0 && index < changed {
			serverPort++
			password += "-changed"
		}
		builder.WriteString(`{"type":"trojan","tag":"node-`)
		fmt.Fprintf(builder, "%05d", index)
		builder.WriteString(`","server":"198.51.100.`)
		builder.WriteString(strconv.Itoa(7 + index%200))
		builder.WriteString(`","server_port":`)
		builder.WriteString(strconv.Itoa(serverPort))
		builder.WriteString(`,"password":"`)
		builder.WriteString(password)
		builder.WriteString(`","tls":{"enabled":true,"insecure":true,"server_name":"cold.example"}}`)
	}
	builder.WriteString(`]}`)
	return builder.String()
}

// studyPaddedDocument is the document plus a valid but ignored top-level key whose value is a long
// string. It is used to test whether the parsed node set retains the document: a value kept only
// through substrings of a larger allocation keeps the whole allocation alive.
func studyPaddedDocument(nodes int, revision int, paddingBytes int) string {
	base := studyDocument(nodes, revision, 0)
	if paddingBytes == 0 {
		return base
	}
	// Insert the ignored key before the closing brace.
	padding := strings.Repeat("A", paddingBytes)
	return `{"padding":"` + padding + `",` + base[1:]
}

type statsReading struct {
	HeapAlloc   uint64 `json:"heap_alloc"`
	HeapObjects uint64 `json:"heap_objects"`
	HeapInuse   uint64 `json:"heap_inuse"`
	TotalAlloc  uint64 `json:"total_alloc"`
	NumGC       uint32 `json:"num_gc"`
}

func readStats() statsReading {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return statsReading{
		HeapAlloc:   memory.HeapAlloc,
		HeapObjects: memory.HeapObjects,
		HeapInuse:   memory.HeapInuse,
		TotalAlloc:  memory.TotalAlloc,
		NumGC:       memory.NumGC,
	}
}

// sampler reads the heap counters while the work runs. ReadMemStats stops the world, so the period
// is a real cost; 500缂備胶鍋撳姗€顢?was chosen so a one-millisecond-scale workspace spike is caught while the
// study's own slowdown stays in the low percent.
type sampler struct {
	period  time.Duration
	stop    chan struct{}
	done    chan struct{}
	access  sync.Mutex
	peak    statsReading
	samples int64
	phase   string
	peakAt  string
}

func startSampler(period time.Duration) *sampler {
	instance := &sampler{period: period, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(instance.done)
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-instance.stop:
				return
			case <-ticker.C:
				reading := readStats()
				instance.access.Lock()
				if reading.HeapAlloc > instance.peak.HeapAlloc {
					instance.peak = reading
					instance.peakAt = instance.phase
				}
				instance.samples++
				instance.access.Unlock()
			}
		}
	}()
	return instance
}

// setPhase labels the work that is running so the maximum can be attributed to a phase rather than
// reported as an unlabelled moment between two boundary readings.
func (s *sampler) setPhase(name string) {
	s.access.Lock()
	s.phase = name
	s.access.Unlock()
}

func (s *sampler) finish() (statsReading, int64, string) {
	close(s.stop)
	<-s.done
	s.access.Lock()
	defer s.access.Unlock()
	return s.peak, s.samples, s.peakAt
}

type phase struct {
	Name        string `json:"name"`
	HeapAlloc   uint64 `json:"heap_alloc"`
	HeapObjects uint64 `json:"heap_objects"`
	HeapInuse   uint64 `json:"heap_inuse"`
	AllocDelta  uint64 `json:"alloc_delta"`
	NumGC       uint32 `json:"num_gc"`
}

type studyResult struct {
	Schema       string        `json:"schema"`
	Mode         string        `json:"mode"`
	Discipline   string        `json:"discipline"`
	Nodes        int           `json:"nodes"`
	DocumentLen  int           `json:"document_bytes"`
	Changed      int           `json:"changed"`
	GoVersion    string        `json:"go_version"`
	Baseline     statsReading  `json:"baseline"`
	Peak         statsReading  `json:"peak"`
	SamplerUs    int           `json:"sampler_period_us"`
	Samples      int64         `json:"samples"`
	PeakPhase    string        `json:"peak_phase_reached"`
	Phases       []phase       `json:"phases"`
	Endpoint     statsReading  `json:"endpoint"`
	PeakMinusEnd uint64        `json:"peak_minus_endpoint"`
	TotalAlloc   uint64        `json:"total_alloc_delta"`
	GCCount      uint32        `json:"gc_count"`
	Reused       int           `json:"reused_verified"`
	Replaced     int           `json:"replaced_verified"`
	ReusedOK     bool          `json:"reuse_semantics_ok"`
	ParseReused  int           `json:"parse_reused"`
	ParseReplace int           `json:"parse_replaced"`
	DurationUs   int64         `json:"duration_us"`
	Notes        []string      `json:"notes"`
	Elapsed      time.Duration `json:"-"`
}

// collectPeak runs work between a settled baseline and a settled endpoint, recording a reading at
// every phase boundary and the sampler maximum while it ran.
//
// The objects each phase must keep alive are passed to KeepAlive explicitly: a phase reading that
// let the compiler prove a value dead would measure a world the product never sees.
func (r *studyResult) phase(name string, totalBefore uint64, keepAlive ...any) {
	reading := readStats()
	r.Phases = append(r.Phases, phase{
		Name:        name,
		HeapAlloc:   reading.HeapAlloc,
		HeapObjects: reading.HeapObjects,
		HeapInuse:   reading.HeapInuse,
		AllocDelta:  reading.TotalAlloc - totalBefore,
		NumGC:       reading.NumGC,
	})
	for _, value := range keepAlive {
		runtime.KeepAlive(value)
	}
}

func settleHeap() statsReading {
	runtime.GC()
	runtime.GC()
	return readStats()
}

func TestGM6Study(t *testing.T) {
	if *gm6Mode == "" {
		t.Skip("GM-6 peak study is opt-in: pass -gm6study cold|unchanged|small|full|layers|held")
	}
	for repetition := 0; repetition < *gm6Repeat; repetition++ {
		result := runGM6Study(t)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("GM6RESULT %s\n", encoded)
		if *gm6Keep {
			fmt.Fprintf(os.Stderr, "GM-6 %s nodes=%d doc=%dB baseline=%dB peak=%dB endpoint=%dB peak-end=%dB samples=%d\n",
				result.Mode, result.Nodes, result.DocumentLen, result.Baseline.HeapAlloc, result.Peak.HeapAlloc,
				result.Endpoint.HeapAlloc, result.PeakMinusEnd, result.Samples)
			for _, entry := range result.Phases {
				fmt.Fprintf(os.Stderr, "  %-16s heap=%9d objects=%8d inuse=%9d alloc_delta=%9d gc=%d\n",
					entry.Name, entry.HeapAlloc, entry.HeapObjects, entry.HeapInuse, entry.AllocDelta, entry.NumGC)
			}
		}
	}
}

func runGM6Study(t *testing.T) studyResult {
	t.Helper()
	if *gm6Mode == "layers" {
		return runGM6Layers(t)
	}
	if strings.HasPrefix(*gm6Mode, "held") {
		return runGM6Held(t)
	}
	result := studyResult{
		Schema:     "gm6/1",
		Mode:       *gm6Mode,
		Discipline: *gm6GC,
		Nodes:      *gm6Nodes,
		Changed:    *gm6Changed,
		GoVersion:  runtime.Version(),
		SamplerUs:  *gm6SamplerUs,
	}
	harness := newStudyHarness(t)
	revision := 1
	document := studyDocument(result.Nodes, revision, 0)
	var previous map[string]adapter.Outbound
	var previousOptions []option.Outbound
	if result.Mode != "cold" {
		previousOptions = harness.publish(t, document)
		previous = snapshotOutbounds(harness)
		if result.Mode == "unchanged" {
			result.Notes = append(result.Notes, "the refresh document is byte-identical to the published one")
		}
	}
	// The second document is generated (or re-read as text) outside the measured window: the study
	// measures processing, not fixture synthesis. It stays referenced through the window because a
	// provider keeps the published option tree it diffed against.
	nextRevision := revision
	changed := 0
	switch result.Mode {
	case "small":
		changed = *gm6Changed
	case "full":
		nextRevision, changed = 2, result.Nodes
	}
	refresh := studyDocument(result.Nodes, nextRevision, changed)
	result.DocumentLen = len(refresh)
	result.Changed = changed

	baseline := settleHeap()
	result.Baseline = baseline
	totalBefore := baseline.TotalAlloc
	previousGC := baseline.NumGC
	if *gm6GC == "off" {
		debug.SetGCPercent(-1)
		result.Notes = append(result.Notes, "collector disabled for the measured window: HeapAlloc at a boundary is cumulative allocation since the baseline")
	}
	samplerInstance := startSampler(time.Duration(*gm6SamplerUs) * time.Microsecond)
	started := time.Now()

	// Phase 1: the document becomes a string. The provider's own read path produces a []byte and
	// converts it once; that conversion is part of the product's cost and is included.
	samplerInstance.setPhase("document")
	result.phase("document", totalBefore, refresh)
	var documentBytes []byte
	documentString := refresh
	result.phase("document_string", totalBefore, documentString)
	documentBytes = []byte(documentString)
	result.phase("document_bytes", totalBefore, documentBytes)
	runtime.KeepAlive(documentBytes)

	// Phase 2: the format-detection chain. This is the copy-under-study.
	samplerInstance.setPhase("parse")
	outbounds, parseErr := parser.ParseSubscription(harness.ctx, documentString, nil, "study")
	if parseErr != nil {
		t.Fatalf("parse the refresh document: %v", parseErr)
	}
	result.phase("parse", totalBefore, outbounds, documentString)
	for index := range outbounds {
		if previousOptions == nil || index >= len(previousOptions) {
			break
		}
		if reflect.DeepEqual(outbounds[index], previousOptions[index]) {
			result.ParseReused++
		} else {
			result.ParseReplace++
		}
	}

	samplerInstance.setPhase("update_nodes")
	// Phase 3: the apply. Both the previously published objects and the freshly parsed option tree
	// are alive here, which is the state a refresh actually has.
	updateErr := harness.provider.UpdateNodes(outbounds)
	result.phase("update_nodes", totalBefore, outbounds, harness.provider)
	if updateErr != nil {
		// A partially failed update is a real outcome the measurement must keep; the study only
		// fails when the document could not be applied at all.
		result.Notes = append(result.Notes, "update_nodes returned: "+updateErr.Error())
	}
	samplerInstance.setPhase("publication")
	// Phase 4: the publication barrier. The retired objects parked by the manager are released
	// here, so this boundary is the "old and new coexist" end.
	groupErr := harness.provider.UpdateGroups()
	result.phase("publication", totalBefore, harness.provider)
	if groupErr != nil {
		result.Notes = append(result.Notes, "publication returned: "+groupErr.Error())
	}

	// Phase 4b: the cache write. The remote provider re-serialises the whole applied option set to
	// write its cache path and its saved-subscription record (provider/remote/remote.go:751 for the
	// not-modified path, :823 for a committed one). That is a full document-sized copy on every
	// refresh; it is measured here because the same bytes are written by the product.
	samplerInstance.setPhase("cache_marshal")
	cached, marshalErr := json.Marshal(option.Options{Outbounds: outbounds})
	if marshalErr != nil {
		t.Fatalf("re-serialise the applied options: %v", marshalErr)
	}
	result.phase("cache_marshal", totalBefore, cached)
	cached = nil

	// Semantics guard: an unchanged refresh must keep every published object.
	current := snapshotOutbounds(harness)
	if previous != nil {
		for tag, object := range previous {
			if current[tag] == object {
				result.Reused++
			} else {
				result.Replaced++
			}
		}
		switch result.Mode {
		case "unchanged":
			result.ReusedOK = result.Replaced == 0 && result.Reused == result.Nodes
		case "small":
			result.ReusedOK = result.Replaced == changed && result.Reused == result.Nodes-changed
		case "full":
			result.ReusedOK = result.Reused == 0 && result.Replaced == result.Nodes
		}
		if !result.ReusedOK {
			result.Notes = append(result.Notes, fmt.Sprintf("reuse semantics: reused=%d replaced=%d (expected %d/%d)", result.Reused, result.Replaced, result.Nodes-changed, changed))
		}
	} else {
		result.ReusedOK = len(current) == result.Nodes
	}

	peak, samples, peakAt := samplerInstance.finish()
	samplerInstance.setPhase("endpoint")
	if *gm6GC == "off" {
		debug.SetGCPercent(100)
	}
	result.Elapsed = time.Since(started)
	result.Peak = peak
	result.Samples = samples
	result.PeakPhase = peakAt
	if result.PeakPhase == "" {
		result.PeakPhase = lastPeakPhase(result.Phases, peak.HeapAlloc)
	}
	endpoint := settleHeap()
	result.Endpoint = endpoint
	result.GCCount = endpoint.NumGC - previousGC
	result.TotalAlloc = endpoint.TotalAlloc - totalBefore
	result.PeakMinusEnd = 0
	if peak.HeapAlloc > endpoint.HeapAlloc {
		result.PeakMinusEnd = peak.HeapAlloc - endpoint.HeapAlloc
	}
	result.DurationUs = result.Elapsed.Microseconds()
	// The fixture string is released here; anything still alive because a parsed node aliases it is
	// a property of the parser, not of the harness.
	refresh = ""
	documentString = ""
	documentBytes = nil
	runtime.KeepAlive(harness.provider)
	return result
}

func lastPeakPhase(phases []phase, peak uint64) string {
	for index := len(phases) - 1; index >= 0; index-- {
		if phases[index].HeapAlloc == peak {
			return phases[index].Name
		}
	}
	return "between_phases"
}

func snapshotOutbounds(harness *studyHarness) map[string]adapter.Outbound {
	snapshot := make(map[string]adapter.Outbound)
	for _, object := range harness.provider.Outbounds() {
		snapshot[object.Tag()] = object
	}
	return snapshot
}

// runGM6Layers attributes the PRE-REFACTOR reader's allocation layer by layer, using the same
// exported primitives it composed (generic badjson tree, filtered re-serialisation, whole-document
// typed decode). It is kept after the rewrite as the ledger that explains which copy was big and
// therefore why the streaming reader was chosen; it does not measure the streaming path.
func runGM6Layers(t *testing.T) studyResult {
	t.Helper()
	result := studyResult{
		Schema:     "gm6/1",
		Mode:       "layers",
		Discipline: *gm6GC,
		Nodes:      *gm6Nodes,
		GoVersion:  runtime.Version(),
		SamplerUs:  *gm6SamplerUs,
	}
	ctx := studyLayerContext()
	document := studyDocument(result.Nodes, 1, 0)
	result.DocumentLen = len(document)
	baseline := settleHeap()
	result.Baseline = baseline
	totalBefore := baseline.TotalAlloc
	previousGC := baseline.NumGC
	if *gm6GC == "off" {
		debug.SetGCPercent(-1)
	}
	samplerInstance := startSampler(time.Duration(*gm6SamplerUs) * time.Microsecond)
	started := time.Now()

	result.phase("document", totalBefore, document)
	inputBytes := []byte(document)
	result.phase("document_bytes", totalBefore, inputBytes)
	var tree badjson.JSONObject
	if err := tree.UnmarshalJSONContext(ctx, inputBytes); err != nil {
		t.Fatalf("build the generic tree: %v", err)
	}
	result.phase("generic_tree", totalBefore, &tree, inputBytes)
	outboundsValue, loaded := tree.Get("outbounds")
	if !loaded {
		t.Fatal("the generic tree lost the outbounds key")
	}
	array, isArray := outboundsValue.(badjson.JSONArray)
	if !isArray {
		t.Fatalf("outbounds is %T", outboundsValue)
	}
	kept := make(badjson.JSONArray, 0, len(array))
	for _, element := range array {
		object, isObject := element.(*badjson.JSONObject)
		if !isObject {
			continue
		}
		typeValue, hasType := object.Get("type")
		typeName, isString := typeValue.(string)
		if !hasType || !isString {
			continue
		}
		switch typeName {
		case "direct", "block", "dns", "selector", "urltest":
			continue
		}
		kept = append(kept, object)
	}
	tree.Put("outbounds", kept)
	result.phase("generic_filter", totalBefore, &tree)
	remarshaled, err := tree.MarshalJSONContext(ctx)
	if err != nil {
		t.Fatalf("re-marshal the filtered document: %v", err)
	}
	result.phase("remarshal", totalBefore, remarshaled)
	decoded, err := singjson.UnmarshalExtendedContext[studyTypedDocument](ctx, remarshaled)
	if err != nil {
		t.Fatalf("decode the re-marshaled document: %v", err)
	}
	result.phase("typed_decode", totalBefore, decoded, remarshaled)
	peak, samples, peakAt := samplerInstance.finish()
	samplerInstance.setPhase("endpoint")
	if *gm6GC == "off" {
		debug.SetGCPercent(100)
	}
	result.Elapsed = time.Since(started)
	result.Peak = peak
	result.Samples = samples
	result.PeakPhase = peakAt
	if result.PeakPhase == "" {
		result.PeakPhase = lastPeakPhase(result.Phases, peak.HeapAlloc)
	}
	endpoint := settleHeap()
	result.Endpoint = endpoint
	result.GCCount = endpoint.NumGC - previousGC
	result.TotalAlloc = endpoint.TotalAlloc - totalBefore
	if peak.HeapAlloc > endpoint.HeapAlloc {
		result.PeakMinusEnd = peak.HeapAlloc - endpoint.HeapAlloc
	}
	result.DurationUs = result.Elapsed.Microseconds()
	document = ""
	inputBytes = nil
	remarshaled = nil
	runtime.KeepAlive(decoded)
	return result
}

// studyTypedDocument is the typed destination of the legacy decode: outbounds and endpoints only.
type studyTypedDocument struct {
	Outbounds []option.Outbound `json:"outbounds"`
	Endpoints []option.Endpoint `json:"endpoints"`
}

func studyLayerContext() context.Context {
	registry := outbound.NewRegistry()
	outbound.Register[studyNodeOptions](registry, "trojan", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, _ string, _ studyNodeOptions) (adapter.Outbound, error) {
		return nil, nil
	})
	return service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
}

// runGM6Held tests whether the parsed node set keeps the document allocation alive.
//
// The document carries `-gm6doc-kb` of extra bytes that the reader never stores: a skipped link
// line for the share-link form, or an ignored top-level string for the JSON form. If the parsed
// nodes alias substrings of the document, the whole allocation (padding included) stays reachable
// after the harness drops its own reference, and the settled live heap grows with the padding. If
// the reader allocated fresh strings, the document dies and the padding is reclaimed.
func runGM6Held(t *testing.T) studyResult {
	t.Helper()
	link := strings.HasSuffix(*gm6Mode, "link") || *gm6Mode == "held"
	result := studyResult{Schema: "gm6/1", Mode: *gm6Mode, Discipline: "forced", Nodes: *gm6Nodes, GoVersion: runtime.Version()}
	ctx := studyLayerContext()
	var document string
	if link {
		document = studyLinkDocument(result.Nodes, *gm6DocumentKB*1024)
	} else {
		document = studyPaddedDocument(result.Nodes, 1, *gm6DocumentKB*1024)
	}
	result.DocumentLen = len(document)
	baseline := settleHeap()
	totalBefore := baseline.TotalAlloc
	outbounds, err := parser.ParseSubscription(ctx, document, nil, "study")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result.phase("parsed", totalBefore, outbounds, document)
	// Drop every reference the harness itself holds to the document, then settle: what remains
	// reachable is what the parsed node set owns.
	document = ""
	settled := settleHeap()
	result.Endpoint = settled
	result.Peak = settled
	result.TotalAlloc = settled.TotalAlloc - totalBefore
	result.PeakMinusEnd = settled.HeapAlloc
	runtime.KeepAlive(outbounds)
	runtime.KeepAlive(ctx)
	return result
}

// studyLinkDocument renders a share-link list plus an ignored padding line.
func studyLinkDocument(nodes int, paddingBytes int) string {
	builder := &strings.Builder{}
	builder.Grow(nodes*90 + paddingBytes)
	for index := 0; index < nodes; index++ {
		if index > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("trojan://study@198.51.100.")
		builder.WriteString(strconv.Itoa(7 + index%200))
		builder.WriteString(":443?security=tls&sni=cold.example#node-")
		fmt.Fprintf(builder, "%05d", index)
	}
	if paddingBytes > 0 {
		builder.WriteString("\n")
		builder.WriteString(strings.Repeat("P", paddingBytes))
	}
	return builder.String()
}
