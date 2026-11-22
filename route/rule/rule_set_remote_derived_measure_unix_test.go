//go:build unix

package rule

// M — independent measurement of the remote rule-set derived cache. Evidence only: this file adds
// no production behaviour and changes none.
//
// What is measured: the REAL integration, not a helper. Every arm drives production entry points
// (`StartContext` -> restoreStoredRuleSet -> publishStoredVersion, and `fetch` -> Stage ->
// publishStaged), with real HTTP through an httptest server. The only test-only switches are
// `enableDerivedRuleSetPlatform` (the product switch that makes the derived cache active on a Unix
// test host; the product value is false off iOS) and the package-level diagnostics counters, which
// record what the load actually did.
//
// One measured load per process. A second load in the same process inherits the first one's heap,
// its page cache and its mapped pages, so "cold" and "warm" can only be separated honestly by
// starting each sample from an identical process. The driver script
// (04-evidence/scripts/run-remote-derived-measure.sh) runs every sample serially.
//
// Environment (one process = one sample):
//
//	R_ARM        cold | warm | http200 | http304 | change | overlap | prepare
//	R_FORMAT     binary | source
//	R_ENTRIES    domains in the fixture (default 100000)
//	R_SAMPLE     sample index, recorded only
//	R_ROOT       fixture root; fixtures live in $R_ROOT/fixtures
//	R_OUT        JSONL file this sample appends one row to
//	R_STORE      filemanager base directory (the artifact store lives in $R_STORE/rule-set-artifacts)
//	R_AGGREGATE  when set, prints per-arm medians of that JSONL file (aggregate test)
//	R_SUMMARY    optional file the aggregate table is also written to
//	R_LOOP       iterations for the long-loop test (default 100)
//	R_LOOP_ENTRIES  domains per loop document (default 10000)
//
// Arms:
//
//	cold      empty store, StartContext against a server serving fixture A: download + parse + SRSM
//	          write + derived publish + map.
//	warm      StartContext against a store prepared with A, transport configured to FAIL if
//	          contacted: restore + derived hit, no request, no decode, no SRSM write.
//	http200   store prepared with A, one `fetch` of unchanged A: must hit the conversion published
//	          by the prepare.
//	http304   store prepared with A, `fetch` answered 304: no parse and no conversion.
//	change    store prepared with A, `fetch` serves B: miss, decode + SRSM write + publish, serves B.
//	overlap   like change, but the previous generation's rules slice is kept referenced: the peak
//	          and the live heap cover both generations, which is the shape of a live refresh.
//	prepare   performs a full cold StartContext into R_STORE and exits; NOT part of the medians.
//
// The fixture is a single default headless rule with `R_ENTRIES` domains ("d0-000000.remote-
// measure.example"), variant 0 = A, variant 1 = B. Both formats describe the SAME rules:
// `binary` is `srs.Write` (the format the product version declares today) and `source` is the
// source-JSON form, so the format is a property of the transported document, not of the rules.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

const (
	// rmeasSourceURL is the URL identity the store is keyed by. It is FIXED across processes on
	// purpose: the artifact key contains a digest of the source URL, so a per-process httptest URL
	// would make every sample a different store entry and no warm restore could ever hit. Requests
	// for this URL are routed to the local httptest server by rmeasTransport.
	rmeasSourceURL = "https://rules.remote-derived-measure.invalid/rule-set"
	// rmeasTag is fixed for the same reason: the store key is <tag>-<url digest>.
	rmeasTag = "remote-derived-measure"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func rmeasDomain(variant int, index int) string {
	return fmt.Sprintf("d%d-%06d.remote-measure.example", variant, index)
}

func rmeasFixtureRuleSet(entries int, variant int) option.PlainRuleSet {
	domains := make([]string, entries)
	for index := range domains {
		domains[index] = rmeasDomain(variant, index)
	}
	return option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type:           C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{Domain: badoption.Listable[string](domains)},
	}}}
}

func rmeasEncode(ruleSet option.PlainRuleSet, format string) ([]byte, error) {
	switch format {
	case C.RuleSetFormatBinary:
		var buffer bytes.Buffer
		if err := srs.Write(&buffer, ruleSet, C.RuleSetVersionCurrent); err != nil {
			return nil, err
		}
		return buffer.Bytes(), nil
	case C.RuleSetFormatSource:
		return singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
			Version: C.RuleSetVersionCurrent,
			Options: ruleSet,
		})
	default:
		return nil, E.New("unknown rule-set format ", format)
	}
}

func rmeasFormatName(t *testing.T) string {
	t.Helper()
	format := os.Getenv("R_FORMAT")
	if format == "" {
		format = C.RuleSetFormatBinary
	}
	require.Contains(t, []string{C.RuleSetFormatBinary, C.RuleSetFormatSource}, format)
	return format
}

func rmeasEntries(t *testing.T) int {
	t.Helper()
	return rmeasPositiveEnv(t, "R_ENTRIES", 100_000)
}

func rmeasIntEnv(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "%s", name)
	return value
}

func rmeasPositiveEnv(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := rmeasIntEnv(t, name, fallback)
	require.Positive(t, value, "%s", name)
	return value
}

func rmeasFixturePath(root string, entries int, format string, variant int) string {
	extension := "srs"
	if format == C.RuleSetFormatSource {
		extension = "json"
	}
	return filepath.Join(root, "fixtures", fmt.Sprintf("domain-%d-%s.%s", entries, string(rune('a'+variant)), extension))
}

func rmeasFixtureContent(t *testing.T, root string, entries int, format string, variant int) []byte {
	t.Helper()
	content, err := os.ReadFile(rmeasFixturePath(root, entries, format, variant))
	require.NoError(t, err, "fixture for entries=%d format=%s variant=%d; run TestRemoteDerivedMeasureFixtures first", entries, format, variant)
	require.NotEmpty(t, content)
	return content
}

// TestRemoteDerivedMeasureFixtures writes the shared fixtures once. The reference worktree reads the
// same files, so both sides measure the identical bytes.
func TestRemoteDerivedMeasureFixtures(t *testing.T) {
	root := os.Getenv("R_ROOT")
	if root == "" {
		t.Skip("set R_ROOT to generate the measurement fixtures")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "fixtures"), 0o755))
	entriesList := []int{10_000, 100_000}
	if raw := os.Getenv("R_ENTRIES_LIST"); raw != "" {
		entriesList = nil
		for _, field := range strings.FieldsFunc(raw, func(character rune) bool {
			return character == ',' || character == ' ' || character == '\t'
		}) {
			value, err := strconv.Atoi(field)
			require.NoError(t, err)
			entriesList = append(entriesList, value)
		}
	}
	for _, entries := range entriesList {
		for _, format := range []string{C.RuleSetFormatBinary, C.RuleSetFormatSource} {
			for _, variant := range []int{0, 1} {
				path := rmeasFixturePath(root, entries, format, variant)
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					t.Logf("FIXTURE exists entries=%d format=%s variant=%d bytes=%d", entries, format, variant, info.Size())
					continue
				}
				content, err := rmeasEncode(rmeasFixtureRuleSet(entries, variant), format)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, content, 0o644))
				t.Logf("FIXTURE wrote entries=%d format=%s variant=%d bytes=%d", entries, format, variant, len(content))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP server and transport
// ---------------------------------------------------------------------------

// rmeasServer serves one payload for every request. The payload is swapped between fetches in the
// long-loop test; the measured arms set it once before the load.
type rmeasServer struct {
	server   *httptest.Server
	payload  atomic.Value // []byte
	status   atomic.Int32 // 200 or 304
	hits     atomic.Int32
	lastEtag atomic.Value // string
}

func rmeasStartServer(t *testing.T) *rmeasServer {
	t.Helper()
	served := &rmeasServer{}
	served.status.Store(http.StatusOK)
	served.lastEtag.Store("")
	served.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		served.hits.Add(1)
		etag, _ := served.lastEtag.Load().(string)
		if etag != "" {
			writer.Header().Set("Etag", etag)
		}
		if served.status.Load() == http.StatusNotModified {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		content, _ := served.payload.Load().([]byte)
		writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = writer.Write(content)
	}))
	t.Cleanup(served.server.Close)
	return served
}

func (s *rmeasServer) serve(content []byte) {
	s.status.Store(http.StatusOK)
	s.payload.Store(append([]byte(nil), content...))
}

func (s *rmeasServer) serveNotModified(etag string) {
	s.status.Store(http.StatusNotModified)
	s.lastEtag.Store(etag)
}

// rmeasTransport is a real net/http transport that routes every request to the local test server
// (the request URL stays the fixed store identity) and counts the requests. `fail` is the warm
// arm's seam: a restore that touches the network fails the load instead of silently downloading.
type rmeasTransport struct {
	base   *http.Transport
	target *url.URL
	hits   atomic.Int32
	fail   bool
}

func rmeasNewTransport(t *testing.T, server *rmeasServer, fail bool) *rmeasTransport {
	t.Helper()
	target, err := url.Parse(server.server.URL)
	require.NoError(t, err)
	return &rmeasTransport{
		base:   &http.Transport{Proxy: nil},
		target: target,
		fail:   fail,
	}
}

func (t *rmeasTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.hits.Add(1)
	if t.fail {
		return nil, E.New("measurement transport refused the request: this arm must not contact the server")
	}
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme = t.target.Scheme
	rewritten.URL.Host = t.target.Host
	rewritten.Host = t.target.Host
	return t.base.RoundTrip(rewritten)
}

func (t *rmeasTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

func (t *rmeasTransport) Reset() { t.base.CloseIdleConnections() }

type rmeasHTTPClientManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m *rmeasHTTPClientManager) DefaultTransport() adapter.HTTPTransport { return m.transport }

// ---------------------------------------------------------------------------
// Process context, rule set, and verification
// ---------------------------------------------------------------------------

func rmeasContext(t *testing.T, storeDir string) context.Context {
	t.Helper()
	require.NotEmpty(t, storeDir, "R_STORE")
	require.NoError(t, os.MkdirAll(storeDir, 0o755))
	// The temp directory is where writeRuleSetMmapFile puts the transient SRSM. It is OUTSIDE the
	// store so a store copy between samples cannot carry stale temp files.
	tempDir := storeDir + "-tmp"
	require.NoError(t, os.MkdirAll(tempDir, 0o755))
	ctx := service.ContextWithDefaultRegistry(context.Background())
	return filemanager.WithDefault(ctx, storeDir, tempDir, -1, -1)
}

func rmeasInstallTransport(ctx context.Context, transport adapter.HTTPTransport) {
	service.MustRegister[adapter.HTTPClientManager](ctx, &rmeasHTTPClientManager{transport: transport})
}

func rmeasNewRuleSet(t *testing.T, ctx context.Context, format string) *RemoteRuleSet {
	t.Helper()
	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), rmeasTag, option.RuleSet{
		Type:          C.RuleSetTypeRemote,
		Format:        format,
		RemoteOptions: option.RemoteRuleSet{URL: rmeasSourceURL},
	})
	require.NoError(t, err)
	return ruleSet
}

func rmeasMatchRule(t *testing.T, rules []adapter.HeadlessRule, domain string) bool {
	t.Helper()
	return matchAnyHeadlessRule(rules, &adapter.InboundContext{Domain: domain})
}

func rmeasRequireMember(t *testing.T, rules []adapter.HeadlessRule, entries int, variant int) {
	t.Helper()
	require.True(t, rmeasMatchRule(t, rules, rmeasDomain(variant, entries-1)),
		"the loaded generation must match its own last member (variant %d)", variant)
	require.False(t, rmeasMatchRule(t, rules, "absent.remote-measure.example"),
		"the loaded generation must not match a non-member")
}

// rmeasSink keeps the measured generation reachable after the load, so the post-arm live heap is the
// steady cost of a generation that is still installed rather than of a freed one.
var rmeasSink any

// ---------------------------------------------------------------------------
// Instruments
// ---------------------------------------------------------------------------

type rmeasSampler struct {
	stop chan struct{}
	done chan struct{}
	peak atomic.Uint64
}

func rmeasStartSampler(interval time.Duration) *rmeasSampler {
	sampler := &rmeasSampler{stop: make(chan struct{}), done: make(chan struct{})}
	if current, ok := rmeasReadRSS(); ok {
		sampler.peak.Store(current)
	}
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.stop:
				return
			case <-ticker.C:
				if current, ok := rmeasReadRSS(); ok {
					for {
						peak := sampler.peak.Load()
						if current <= peak || sampler.peak.CompareAndSwap(peak, current) {
							break
						}
					}
				}
			}
		}
	}()
	return sampler
}

func (s *rmeasSampler) finish() uint64 {
	close(s.stop)
	<-s.done
	return s.peak.Load()
}

func rmeasReadRSS() (uint64, bool) {
	content, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}

func rmeasRSS(t *testing.T) uint64 {
	t.Helper()
	current, ok := rmeasReadRSS()
	require.True(t, ok, "/proc/self/statm is unavailable on this host")
	return current
}

func rmeasHeap() (live uint64, objects int64, total uint64) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc, int64(stats.HeapObjects), stats.TotalAlloc
}

func rmeasFDCount() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

// rmeasStoreStats reports the disk the versioned store actually holds, split into the source
// artifacts the remote path publishes and the derived conversions the cache publishes.
func rmeasStoreStats(baseDir string) (bytes int64, files int, sourceBytes int64, derivedBytes int64) {
	_ = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		bytes += info.Size()
		files++
		relative, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			sourceBytes += info.Size()
			return nil
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) >= 3 && parts[0] == derivedStoreDirectory && strings.HasPrefix(parts[1], "derived-") {
			derivedBytes += info.Size()
		} else {
			sourceBytes += info.Size()
		}
		return nil
	})
	return bytes, files, sourceBytes, derivedBytes
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

type rmeasRow struct {
	Arm               string  `json:"arm"`
	Format            string  `json:"format"`
	Entries           int     `json:"entries"`
	Sample            int     `json:"sample"`
	Variant           string  `json:"variant"`
	Millis            float64 `json:"millis"`
	TotalAlloc        uint64  `json:"total_alloc"`
	LiveHeap          uint64  `json:"live_heap"`
	RSSBefore         uint64  `json:"rss_before"`
	RSSPeak           uint64  `json:"rss_peak"`
	RSSAfter          uint64  `json:"rss_after"`
	Hits              int64   `json:"hits"`
	Misses            int64   `json:"misses"`
	SourceDecodes     int64   `json:"source_decodes"`
	SRSMWrites        int64   `json:"srsm_writes"`
	HeapBuilds        int64   `json:"heap_builds"`
	SourceBytes       int64   `json:"source_bytes"`
	StoreBytes        int64   `json:"store_bytes"`
	StoreFiles        int     `json:"store_files"`
	SourceStoreBytes  int64   `json:"source_store_bytes"`
	DerivedStoreBytes int64   `json:"derived_store_bytes"`
	ServerHits        int32   `json:"server_hits"`
	TransportHits     int32   `json:"transport_hits"`
	FDs               int     `json:"fds"`
	Goroutines        int     `json:"goroutines"`
	Err               string  `json:"err,omitempty"`
}

func rmeasDiagnostics(row *rmeasRow) {
	row.Hits, row.Misses, row.SourceDecodes, row.SRSMWrites, row.HeapBuilds = remoteDerivedDiagnostics()
}

func rmeasEmit(t *testing.T, row rmeasRow) {
	t.Helper()
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Logf("MEASURE-ENCODE-ERROR %v", err)
		return
	}
	t.Logf("MEASURE %s", encoded)
	output := os.Getenv("R_OUT")
	if output == "" {
		return
	}
	file, openErr := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if openErr != nil {
		t.Logf("MEASURE-WRITE-ERROR %v", openErr)
		return
	}
	defer file.Close()
	if _, writeErr := file.WriteString(string(encoded) + "\n"); writeErr != nil {
		t.Logf("MEASURE-WRITE-ERROR %v", writeErr)
	}
}

// rmeasRunOne measures exactly one action with the same instruments for every arm: wall time, total
// allocation, live heap after two collections (attribution only), and peak RSS sampled every 2 ms.
func rmeasRunOne(t *testing.T, row *rmeasRow, action func() error) error {
	t.Helper()
	runtime.GC()
	runtime.GC()
	liveBefore, _, totalBefore := rmeasHeap()
	row.RSSBefore = rmeasRSS(t)
	sampler := rmeasStartSampler(2 * time.Millisecond)
	started := time.Now()
	actionErr := action()
	elapsed := time.Since(started)
	row.RSSPeak = sampler.finish()
	// The generation stays live while the post-load heap is read: that is the steady cost.
	runtime.GC()
	runtime.GC()
	liveAfter, _, totalAfter := rmeasHeap()
	row.RSSAfter = rmeasRSS(t)
	row.Millis = float64(elapsed.Nanoseconds()) / 1e6
	row.TotalAlloc = totalAfter - totalBefore
	if liveAfter > liveBefore {
		row.LiveHeap = liveAfter - liveBefore
	}
	if actionErr != nil {
		row.Err = actionErr.Error()
	}
	return actionErr
}

// ---------------------------------------------------------------------------
// Measured sample
// ---------------------------------------------------------------------------

// TestRemoteDerivedMeasureSample runs exactly one measured load (or the unmeasured prepare) in a
// fresh process. Every assertion below is about what the production path DID, not about timing:
// timing is the measurement, the counters are the proof of which path ran.
func TestRemoteDerivedMeasureSample(t *testing.T) {
	root := os.Getenv("R_ROOT")
	if root == "" {
		t.Skip("set R_ROOT to run the remote derived measurement harness")
	}
	arm := os.Getenv("R_ARM")
	require.NotEmpty(t, arm, "R_ARM")
	format := rmeasFormatName(t)
	entries := rmeasEntries(t)
	sample := rmeasIntEnv(t, "R_SAMPLE", 0)
	storeDir := os.Getenv("R_STORE")
	require.NotEmpty(t, storeDir, "R_STORE")

	enableDerivedRuleSetPlatform(t)
	ctx := rmeasContext(t, storeDir)

	payloadA := rmeasFixtureContent(t, root, entries, format, 0)
	payloadB := rmeasFixtureContent(t, root, entries, format, 1)

	row := rmeasRow{Arm: arm, Format: format, Entries: entries, Sample: sample}
	defer func() {
		if row.Err == "" && t.Failed() {
			row.Err = "assertion failed"
		}
		row.FDs = rmeasFDCount()
		row.Goroutines = runtime.NumGoroutine()
		row.StoreBytes, row.StoreFiles, row.SourceStoreBytes, row.DerivedStoreBytes = rmeasStoreStats(storeDir)
		rmeasEmit(t, row)
	}()

	switch arm {
	case "prepare", "cold":
		server := rmeasStartServer(t)
		server.serve(payloadA)
		transport := rmeasNewTransport(t, server, false)
		rmeasInstallTransport(ctx, transport)
		ruleSet := rmeasNewRuleSet(t, ctx, format)
		row.Variant = "a"
		row.SourceBytes = int64(len(payloadA))
		resetRemoteDerivedDiagnostics()
		require.NoError(t, rmeasRunOne(t, &row, func() error {
			return ruleSet.StartContext(ctx, adapter.NewHTTPStartContext())
		}))
		rmeasDiagnostics(&row)
		row.ServerHits = server.hits.Load()
		row.TransportHits = transport.hits.Load()
		rules := ruleSet.currentRules()
		require.NotEmpty(t, rules)
		rmeasRequireMember(t, rules, entries, 0)
		require.EqualValues(t, 1, row.ServerHits, "a cold start downloads once")
		require.EqualValues(t, 1, row.Misses)
		require.EqualValues(t, 1, row.SourceDecodes)
		require.EqualValues(t, 1, row.SRSMWrites)
		require.Zero(t, row.Hits)
		require.Zero(t, row.HeapBuilds)
		rmeasSink = rules
		_ = ruleSet.Close()

	case "warm":
		// No server is started for this arm: any request is a failure by construction.
		transport := rmeasNewTransport(t, rmeasStartServer(t), true)
		rmeasInstallTransport(ctx, transport)
		ruleSet := rmeasNewRuleSet(t, ctx, format)
		row.Variant = "a"
		row.SourceBytes = int64(len(payloadA))
		resetRemoteDerivedDiagnostics()
		require.NoError(t, rmeasRunOne(t, &row, func() error {
			return ruleSet.StartContext(ctx, adapter.NewHTTPStartContext())
		}))
		rmeasDiagnostics(&row)
		row.ServerHits = 0
		row.TransportHits = transport.hits.Load()
		require.Zero(t, row.TransportHits, "a warm restore must not contact the server at all")
		require.EqualValues(t, 1, row.Hits, "the stored version's conversion must be reused")
		require.Zero(t, row.Misses)
		require.Zero(t, row.SourceDecodes, "a derived hit must not parse the source")
		require.Zero(t, row.SRSMWrites, "a derived hit must not write an SRSM")
		require.Zero(t, row.HeapBuilds)
		rules := ruleSet.currentRules()
		rmeasRequireMember(t, rules, entries, 0)
		rmeasSink = rules
		_ = ruleSet.Close()

	case "http200", "http304", "change", "overlap":
		server := rmeasStartServer(t)
		servingB := arm == "change" || arm == "overlap"
		if servingB {
			server.serve(payloadB)
		} else if arm == "http304" {
			server.serveNotModified(`"measure-etag"`)
		} else {
			server.serve(payloadA)
		}
		offline := rmeasNewTransport(t, server, true)
		rmeasInstallTransport(ctx, offline)
		ruleSet := rmeasNewRuleSet(t, ctx, format)
		resetRemoteDerivedDiagnostics()
		require.NoError(t, ruleSet.StartContext(ctx, adapter.NewHTTPStartContext()), "restore of the prepared store")
		require.Zero(t, offline.hits.Load(), "the prepared store must restore without a request")
		hits, misses, decodes, writes, _ := remoteDerivedDiagnostics()
		require.EqualValues(t, 1, hits, "the prepared store must restore through its conversion")
		require.Zero(t, misses)
		require.Zero(t, decodes)
		require.Zero(t, writes)

		// Only the overlap arm keeps the previous generation referenced across the commit: the
		// change arm lets the old slice become unreachable at the swap, exactly as a refresh does.
		var previousRules []adapter.HeadlessRule
		if arm == "overlap" {
			previousRules = ruleSet.currentRules()
			require.NotEmpty(t, previousRules)
			rmeasSink = previousRules
		}

		live := rmeasNewTransport(t, server, false)
		ruleSet.httpClient = &http.Client{Transport: live, Timeout: remoteRuleSetHTTPTimeout}
		row.Variant = "a"
		if servingB {
			row.Variant = "b"
			row.SourceBytes = int64(len(payloadB))
		} else {
			row.SourceBytes = int64(len(payloadA))
		}
		resetRemoteDerivedDiagnostics()
		require.NoError(t, rmeasRunOne(t, &row, func() error {
			return ruleSet.fetch(ctx, true)
		}))
		rmeasDiagnostics(&row)
		row.ServerHits = server.hits.Load()
		row.TransportHits = live.hits.Load()
		require.EqualValues(t, 1, row.ServerHits, "one fetch is one request")
		rules := ruleSet.currentRules()
		require.NotEmpty(t, rules)
		switch arm {
		case "http200":
			require.EqualValues(t, 1, row.Hits, "unchanged content must hit its conversion")
			require.Zero(t, row.Misses)
			require.Zero(t, row.SourceDecodes)
			require.Zero(t, row.SRSMWrites)
			require.Zero(t, row.HeapBuilds)
			rmeasRequireMember(t, rules, entries, 0)
		case "http304":
			require.Zero(t, row.Hits)
			require.Zero(t, row.Misses)
			require.Zero(t, row.SourceDecodes)
			require.Zero(t, row.SRSMWrites)
			require.Zero(t, row.HeapBuilds)
			rmeasRequireMember(t, rules, entries, 0)
		default:
			require.EqualValues(t, 1, row.Misses, "changed content must miss")
			require.Zero(t, row.Hits)
			require.EqualValues(t, 1, row.SourceDecodes)
			require.EqualValues(t, 1, row.SRSMWrites)
			require.Zero(t, row.HeapBuilds)
			rmeasRequireMember(t, rules, entries, 1)
			require.False(t, rmeasMatchRule(t, rules, rmeasDomain(0, entries-1)),
				"the new generation must not keep serving the previous document")
		}
		if arm == "overlap" {
			require.True(t, rmeasMatchRule(t, previousRules, rmeasDomain(0, entries-1)),
				"the previous generation must still be readable while the new one is installed")
			rmeasSink = previousRules
		} else {
			rmeasSink = rules
		}
		_ = ruleSet.Close()

	default:
		t.Fatalf("unknown arm %q", arm)
	}
}

// ---------------------------------------------------------------------------
// Aggregate
// ---------------------------------------------------------------------------

// TestRemoteDerivedMeasureAggregate turns the one-row-per-process JSONL into per-arm medians.
func TestRemoteDerivedMeasureAggregate(t *testing.T) {
	path := os.Getenv("R_AGGREGATE")
	if path == "" {
		t.Skip("set R_AGGREGATE=<jsonl> to aggregate the samples")
	}
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	type groupKey struct {
		arm     string
		format  string
		entries int
	}
	type entry struct {
		millis     float64
		totalAlloc uint64
		liveHeap   uint64
		rssPeak    uint64
		hits       int64
		misses     int64
		decodes    int64
		writes     int64
		heapBuilds int64
		source     int64
		store      int64
		files      int
		failed     bool
	}
	groups := map[groupKey][]entry{}
	var order []groupKey
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}
		var row rmeasRow
		require.NoError(t, json.Unmarshal([]byte(line), &row), line)
		key := groupKey{arm: row.Arm, format: row.Format, entries: row.Entries}
		if _, loaded := groups[key]; !loaded {
			order = append(order, key)
		}
		groups[key] = append(groups[key], entry{
			millis: row.Millis, totalAlloc: row.TotalAlloc, liveHeap: row.LiveHeap,
			rssPeak: row.RSSPeak, hits: row.Hits, misses: row.Misses,
			decodes: row.SourceDecodes, writes: row.SRSMWrites, heapBuilds: row.HeapBuilds,
			source: row.SourceBytes, store: row.StoreBytes, files: row.StoreFiles,
			failed: row.Err != "",
		})
	}
	formatBytes := func(value int64) string {
		switch {
		case value >= 1<<20:
			return fmt.Sprintf("%.2fMiB", float64(value)/(1<<20))
		case value >= 1<<10:
			return fmt.Sprintf("%.1fKiB", float64(value)/(1<<10))
		default:
			return fmt.Sprintf("%dB", value)
		}
	}
	medianInt := func(values []int64) int64 {
		sorted := append([]int64(nil), values...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[len(sorted)/2]
	}
	medianUint := func(values []uint64) uint64 {
		sorted := append([]uint64(nil), values...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[len(sorted)/2]
	}
	medianFloat := func(values []float64) float64 {
		sorted := append([]float64(nil), values...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[len(sorted)/2]
	}
	var builder strings.Builder
	builder.WriteString("| arm | format | entries | n | time p50 | alloc p50 | live p50 | rss-peak p50 | hits | misses | decodes | writes | heap | source | store | files | errs |\n")
	builder.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, key := range order {
		samples := groups[key]
		times := make([]float64, 0, len(samples))
		allocs := make([]uint64, 0, len(samples))
		lives := make([]uint64, 0, len(samples))
		rss := make([]uint64, 0, len(samples))
		var hits, misses, decodes, writes, heapBuilds []int64
		var source, store int64
		var files int
		failures := 0
		for _, sample := range samples {
			if sample.failed {
				failures++
			}
			times = append(times, sample.millis)
			allocs = append(allocs, sample.totalAlloc)
			lives = append(lives, sample.liveHeap)
			rss = append(rss, sample.rssPeak)
			hits = append(hits, sample.hits)
			misses = append(misses, sample.misses)
			decodes = append(decodes, sample.decodes)
			writes = append(writes, sample.writes)
			heapBuilds = append(heapBuilds, sample.heapBuilds)
			if sample.source > source {
				source = sample.source
			}
			if sample.store > store {
				store = sample.store
			}
			if sample.files > files {
				files = sample.files
			}
		}
		builder.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %.2fms | %s | %s | %s | %d | %d | %d | %d | %d | %s | %s | %d | %d |\n",
			key.arm, key.format, key.entries, len(samples), medianFloat(times),
			formatBytes(int64(medianUint(allocs))), formatBytes(int64(medianUint(lives))),
			formatBytes(int64(medianUint(rss))),
			medianInt(hits), medianInt(misses), medianInt(decodes), medianInt(writes), medianInt(heapBuilds),
			formatBytes(source), formatBytes(store), files, failures))
	}
	table := builder.String()
	t.Logf("\n%s", table)
	if summary := os.Getenv("R_SUMMARY"); summary != "" {
		require.NoError(t, os.WriteFile(summary, []byte(table), 0o644))
	}
}

// ---------------------------------------------------------------------------
// Long-term / directory growth check
// ---------------------------------------------------------------------------

type rmeasLoopRow struct {
	Iteration     int     `json:"iteration"`
	Mode          string  `json:"mode"`
	Fetches       int     `json:"fetches"`
	Millis        float64 `json:"millis"`
	Hits          int64   `json:"hits"`
	Misses        int64   `json:"misses"`
	SourceDecodes int64   `json:"source_decodes"`
	SRSMWrites    int64   `json:"srsm_writes"`
	StoreBytes    int64   `json:"store_bytes"`
	StoreFiles    int     `json:"store_files"`
	SourceBytes   int64   `json:"source_store_bytes"`
	DerivedBytes  int64   `json:"derived_store_bytes"`
	LiveHeap      uint64  `json:"live_heap"`
	RSS           uint64  `json:"rss"`
	FDs           int     `json:"fds"`
	Goroutines    int     `json:"goroutines"`
	Err           string  `json:"err,omitempty"`
}

// TestRemoteDerivedMeasureLongLoop drives the real fetch path N times through a repeating
// identical/new/A->B->A cycle, releasing the previous readers every iteration, and records the
// store's file count and bytes, the source-vs-derived split, the cache counters and the FD/goroutine
// counts. It reports trends; it does not claim "no leak".
func TestRemoteDerivedMeasureLongLoop(t *testing.T) {
	root := os.Getenv("R_ROOT")
	if root == "" {
		t.Skip("set R_ROOT to run the long-loop check")
	}
	iterations := rmeasPositiveEnv(t, "R_LOOP", 100)
	entries := rmeasPositiveEnv(t, "R_LOOP_ENTRIES", 10_000)
	format := rmeasFormatName(t)
	storeDir := os.Getenv("R_STORE")
	require.NotEmpty(t, storeDir, "R_STORE")
	output := os.Getenv("R_OUT")

	enableDerivedRuleSetPlatform(t)
	ctx := rmeasContext(t, storeDir)
	server := rmeasStartServer(t)
	payloadA := rmeasFixtureContent(t, root, entries, format, 0)
	payloadB := rmeasFixtureContent(t, root, entries, format, 1)
	server.serve(payloadA)
	transport := rmeasNewTransport(t, server, false)
	rmeasInstallTransport(ctx, transport)
	ruleSet := rmeasNewRuleSet(t, ctx, format)
	require.NoError(t, ruleSet.StartContext(ctx, adapter.NewHTTPStartContext()), "initial load")
	require.NotEmpty(t, ruleSet.currentRules())

	emit := func(row rmeasLoopRow) {
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Logf("LOOP-ENCODE-ERROR %v", err)
			return
		}
		if output != "" {
			file, openErr := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if openErr != nil {
				t.Logf("LOOP-WRITE-ERROR %v", openErr)
			} else {
				_, _ = file.WriteString(string(encoded) + "\n")
				_ = file.Close()
			}
		}
	}
	var rows []rmeasLoopRow
	for iteration := 0; iteration < iterations; iteration++ {
		mode := [...]string{"identical", "new", "aba"}[iteration%3]
		row := rmeasLoopRow{Iteration: iteration, Mode: mode}
		hitsBefore, missesBefore, decodesBefore, writesBefore, _ := remoteDerivedDiagnostics()
		started := time.Now()
		switch mode {
		case "identical":
			row.Fetches = 1
			require.NoError(t, ruleSet.fetch(ctx, true))
		case "new":
			row.Fetches = 1
			fresh, err := rmeasEncode(rmeasFixtureRuleSet(entries, iteration+2), format)
			require.NoError(t, err)
			server.serve(fresh)
			require.NoError(t, ruleSet.fetch(ctx, true))
		case "aba":
			row.Fetches = 3
			server.serve(payloadA)
			require.NoError(t, ruleSet.fetch(ctx, true))
			server.serve(payloadB)
			require.NoError(t, ruleSet.fetch(ctx, true))
			server.serve(payloadA)
			require.NoError(t, ruleSet.fetch(ctx, true))
		}
		row.Millis = float64(time.Since(started).Nanoseconds()) / 1e6
		hitsAfter, missesAfter, decodesAfter, writesAfter, _ := remoteDerivedDiagnostics()
		row.Hits = hitsAfter - hitsBefore
		row.Misses = missesAfter - missesBefore
		row.SourceDecodes = decodesAfter - decodesBefore
		row.SRSMWrites = writesAfter - writesBefore
		// Release every reader but the live generation, exactly as a caller that dropped its
		// reference would, then let the mappings' leases be released before reading the disk.
		rmeasSink = nil
		runtime.GC()
		runtime.GC()
		live, _, _ := rmeasHeap()
		row.LiveHeap = live
		row.RSS = rmeasRSS(t)
		row.FDs = rmeasFDCount()
		row.Goroutines = runtime.NumGoroutine()
		row.StoreBytes, row.StoreFiles, row.SourceBytes, row.DerivedBytes = rmeasStoreStats(storeDir)
		rows = append(rows, row)
		emit(row)
		if iteration%10 == 0 || iteration == iterations-1 {
			t.Logf("LOOP iter=%d mode=%s fetches=%d millis=%.2f hits=%d misses=%d store_files=%d store_bytes=%d source_bytes=%d derived_bytes=%d live_heap=%d rss=%d fds=%d goroutines=%d",
				row.Iteration, row.Mode, row.Fetches, row.Millis, row.Hits, row.Misses,
				row.StoreFiles, row.StoreBytes, row.SourceBytes, row.DerivedBytes,
				row.LiveHeap, row.RSS, row.FDs, row.Goroutines)
		}
	}
	require.NoError(t, ruleSet.Close())

	// Decile summary: the trend, not a claim.
	t.Logf("| block | files p50 | store bytes p50 | source bytes p50 | derived bytes p50 | live heap p50 | rss p50 | fds p50 | goroutines p50 | hits | misses |")
	t.Logf("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	block := 10
	for start := 0; start < len(rows); start += block {
		end := start + block
		if end > len(rows) {
			end = len(rows)
		}
		var files, storeBytes, sourceBytes, derivedBytes, heap, rss, fds, goroutines []int64
		var hits, misses int64
		for _, row := range rows[start:end] {
			files = append(files, int64(row.StoreFiles))
			storeBytes = append(storeBytes, row.StoreBytes)
			sourceBytes = append(sourceBytes, row.SourceBytes)
			derivedBytes = append(derivedBytes, row.DerivedBytes)
			heap = append(heap, int64(row.LiveHeap))
			rss = append(rss, int64(row.RSS))
			fds = append(fds, int64(row.FDs))
			goroutines = append(goroutines, int64(row.Goroutines))
			hits += row.Hits
			misses += row.Misses
		}
		median := func(values []int64) int64 {
			sorted := append([]int64(nil), values...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			return sorted[len(sorted)/2]
		}
		formatBytes := func(value int64) string {
			switch {
			case value >= 1<<20:
				return fmt.Sprintf("%.2fMiB", float64(value)/(1<<20))
			case value >= 1<<10:
				return fmt.Sprintf("%.1fKiB", float64(value)/(1<<10))
			default:
				return fmt.Sprintf("%dB", value)
			}
		}
		t.Logf("| %d-%d | %d | %s | %s | %s | %s | %s | %d | %d | %d | %d |",
			start, end-1, median(files), formatBytes(median(storeBytes)), formatBytes(median(sourceBytes)),
			formatBytes(median(derivedBytes)), formatBytes(median(heap)), formatBytes(median(rss)),
			median(fds), median(goroutines), hits, misses)
	}
}
