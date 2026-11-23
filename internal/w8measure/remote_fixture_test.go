package w8measure

// Shared fixture for the remote rule-set / provider HTTP path.
//
// The point of this file is that the measured load is the PRODUCT load:
//
//	NewRemoteRuleSet(...) / NewProviderRemote(...)
//	StartContext(...)        -> restore or fetch, exactly as the Box calls it
//	RuleSetUpdater           -> the product's periodic refresh (200 -> 304)
//	adapter.CacheFile        -> the product's legacy single-slot cache, implemented by
//	                            this fixture so a fault can be injected at the interface
//	                            without touching a real disk
//
// The HTTP server is a real net/http server. The transport is a real http.Transport whose
// requests are rewritten to the local server, because the store key contains the source
// URL digest: a per-process httptest URL would make every sample a different store entry
// and no restore could ever hit.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route/rule"
	E "github.com/sagernet/sing/common/exceptions"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// The URL is fixed across processes on purpose: the artifact key is derived from it.
const (
	w8RuleSetURL = "https://rules.w8-measure.invalid/rule-set"
	w8ProviderURL = "https://nodes.w8-measure.invalid/subscription"
)

func w8Domain(variant int, index int) string {
	return fmt.Sprintf("d%d-%06d.w8-measure.example", variant, index)
}

func w8RuleSetDocument(entries int, variant int) option.PlainRuleSet {
	domains := make([]string, entries)
	for index := range domains {
		domains[index] = w8Domain(variant, index)
	}
	return option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type:           C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{Domain: badoption.Listable[string](domains)},
	}}}
}

// w8EncodeRuleSet produces the bytes the product's `format` declares. `binary` is the
// .srs format a remote rule-set declares by default.
func w8EncodeRuleSet(t *testing.T, ruleSet option.PlainRuleSet, format string) []byte {
	t.Helper()
	switch format {
	case C.RuleSetFormatBinary:
		var buffer bytes.Buffer
		require.NoError(t, srs.Write(&buffer, ruleSet, C.RuleSetVersionCurrent))
		return buffer.Bytes()
	case C.RuleSetFormatSource:
		content, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
			Version: C.RuleSetVersionCurrent,
			Options: ruleSet,
		})
		require.NoError(t, err)
		return content
	default:
		t.Fatalf("unknown rule-set format %q", format)
		return nil
	}
}

// w8ProviderDocument produces a subscription body with `nodes` nodes. It uses the
// `direct` outbound type so a node needs no external server: what is measured is the
// document fetch, decode and publication, not the tunnel.
func w8ProviderDocument(nodes int, port int) []byte {
	type directOptions struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}
	document := struct {
		Outbounds []directOptions `json:"outbounds"`
	}{Outbounds: make([]directOptions, nodes)}
	for index := range document.Outbounds {
		document.Outbounds[index] = directOptions{Type: C.TypeDirect, Tag: fmt.Sprintf("node-%06d-%d", index, port)}
	}
	encoded, _ := json.Marshal(document)
	return encoded
}

// TestW8FixtureRoundTrip proves the fixture really carries the entry count it claims.
// The binary rule-set format compresses domain lists heavily, so the payload size is not
// evidence; decoding is.
func TestW8FixtureRoundTrip(t *testing.T) {
	for _, entries := range []int{1000, 10000} {
		payload := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), C.RuleSetFormatBinary)
		decoded, err := srs.Read(bytes.NewReader(payload), true)
		require.NoError(t, err)
		require.Len(t, decoded.Options.Rules, 1)
		domains := decoded.Options.Rules[0].DefaultOptions.Domain
		require.Len(t, domains, entries, "the fixture does not carry the requested entry count")
		require.Equal(t, w8Domain(0, entries-1), domains[entries-1])
		t.Logf("FIXTURE entries=%d payload_bytes=%d", entries, len(payload))
	}
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type w8Server struct {
	server        *httptest.Server
	payload       atomic.Value // []byte
	status        atomic.Int32 // 200, 304, 500
	hits          atomic.Int32
	etag          atomic.Value // string
	shortBody     atomic.Bool  // write fewer bytes than Content-Length, then close
	lastEtagValue atomic.Value // string: the If-None-Match the client actually sent
	lastRequestAt atomic.Value // time.Time: when the last request reached the handler
}

func w8StartServer(t *testing.T) *w8Server {
	t.Helper()
	served := &w8Server{}
	served.status.Store(http.StatusOK)
	served.etag.Store("")
	served.lastEtagValue.Store("")
	served.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		served.hits.Add(1)
		served.lastEtagValue.Store(request.Header.Get("If-None-Match"))
		served.lastRequestAt.Store(time.Now())
		etag, _ := served.etag.Load().(string)
		if etag != "" {
			writer.Header().Set("Etag", etag)
		}
		if served.status.Load() == http.StatusNotModified {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		if served.status.Load() == http.StatusInternalServerError {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		content, _ := served.payload.Load().([]byte)
		writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if served.shortBody.Load() {
			// A real short read: the declared length is larger than what arrives, and the
			// connection is closed. The client must treat this as a failed download, not
			// as a complete document.
			_, _ = writer.Write(content[:len(content)/2])
			if hijacker, ok := writer.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		_, _ = writer.Write(content)
	}))
	t.Cleanup(served.server.Close)
	return served
}

func (s *w8Server) serve(content []byte) {
	s.status.Store(http.StatusOK)
	s.payload.Store(append([]byte(nil), content...))
}

func (s *w8Server) serveStatus(status int) { s.status.Store(int32(status)) }

func (s *w8Server) lastRequestEtag() string {
	value, _ := s.lastEtagValue.Load().(string)
	return value
}

func (s *w8Server) lastRequestTime() time.Time {
	value, _ := s.lastRequestAt.Load().(time.Time)
	return value
}

func (s *w8Server) serveNotModified(etag string) {
	s.status.Store(http.StatusNotModified)
	s.etag.Store(etag)
}

// ---------------------------------------------------------------------------
// Transport and HTTP client manager
// ---------------------------------------------------------------------------

type w8Transport struct {
	base   *http.Transport
	target *url.URL
	hits   atomic.Int32
	fail   atomic.Bool
}

func w8NewTransport(t *testing.T, server *w8Server) *w8Transport {
	t.Helper()
	target, err := url.Parse(server.server.URL)
	require.NoError(t, err)
	return &w8Transport{base: &http.Transport{Proxy: nil}, target: target}
}

func (t *w8Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.hits.Add(1)
	if t.fail.Load() {
		return nil, E.New("w8 measurement transport refused the request: this arm must not contact the server")
	}
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme = t.target.Scheme
	rewritten.URL.Host = t.target.Host
	rewritten.Host = t.target.Host
	return t.base.RoundTrip(rewritten)
}

func (t *w8Transport) CloseIdleConnections() { t.base.CloseIdleConnections() }
func (t *w8Transport) Reset()               { t.base.CloseIdleConnections() }

type w8HTTPClientManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m *w8HTTPClientManager) DefaultTransport() adapter.HTTPTransport { return m.transport }
func (m *w8HTTPClientManager) ResetNetwork()                           {}

// ---------------------------------------------------------------------------
// Legacy single-slot cache (adapter.CacheFile), with fault injection
// ---------------------------------------------------------------------------

type w8CacheFault int

const (
	w8CacheHealthy w8CacheFault = iota
	// w8CacheSaveShort stores only half of the bytes and reports success. A cache that
	// claims to have stored a document it truncated is the legacy single-slot analogue
	// of a short write; the next start must not publish the truncated document.
	w8CacheSaveShort
	// w8CacheSaveError fails the write outright.
	w8CacheSaveError
	// w8CacheLoadGarbage returns a stored document whose bytes do not decode.
	w8CacheLoadGarbage
)

type w8CacheFile struct {
	adapter.CacheFile

	access  sync.Mutex
	saved   map[string]*adapter.SavedBinary
	fault   w8CacheFault
	loads   int
	saves   int
	lastErr error
}

func w8NewCacheFile() *w8CacheFile {
	return &w8CacheFile{saved: make(map[string]*adapter.SavedBinary)}
}

func (c *w8CacheFile) setFault(fault w8CacheFault) {
	c.access.Lock()
	c.fault = fault
	c.access.Unlock()
}

func (c *w8CacheFile) LoadRuleSet(tag string) *adapter.SavedBinary {
	c.access.Lock()
	defer c.access.Unlock()
	c.loads++
	saved := c.saved[tag]
	if saved == nil {
		return nil
	}
	copied := &adapter.SavedBinary{
		Content:     append([]byte(nil), saved.Content...),
		LastUpdated: saved.LastUpdated,
		LastEtag:    saved.LastEtag,
		URLHash:     append([]byte(nil), saved.URLHash...),
	}
	if c.fault == w8CacheLoadGarbage {
		copied.Content = bytes.Repeat([]byte{0x7F}, len(copied.Content))
	}
	return copied
}

func (c *w8CacheFile) SaveRuleSet(tag string, saved *adapter.SavedBinary) error {
	c.access.Lock()
	defer c.access.Unlock()
	c.saves++
	if saved == nil {
		delete(c.saved, tag)
		return nil
	}
	if c.fault == w8CacheSaveError {
		c.lastErr = E.New("w8 injected cache write failure")
		return c.lastErr
	}
	content := append([]byte(nil), saved.Content...)
	if c.fault == w8CacheSaveShort {
		content = content[:len(content)/2]
	}
	c.saved[tag] = &adapter.SavedBinary{
		Content:     content,
		LastUpdated: saved.LastUpdated,
		LastEtag:    saved.LastEtag,
		URLHash:     append([]byte(nil), saved.URLHash...),
	}
	return nil
}

func (c *w8CacheFile) counters() (loads int, saves int) {
	c.access.Lock()
	defer c.access.Unlock()
	return c.loads, c.saves
}

func (c *w8CacheFile) stored(tag string) int {
	c.access.Lock()
	defer c.access.Unlock()
	if saved := c.saved[tag]; saved != nil {
		return len(saved.Content)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Process context
// ---------------------------------------------------------------------------

// w8RemoteContext builds the process context the product runs with: a service registry,
// a file base (the artifact store lives in <base>/rule-set-artifacts) and a real
// transport reachable through adapter.HTTPClientManager.
func w8RemoteContext(t *testing.T, baseDir string) context.Context {
	t.Helper()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	if baseDir != "" {
		require.NoError(t, os.MkdirAll(baseDir, 0o755))
		tempDir := baseDir + "-tmp"
		require.NoError(t, os.MkdirAll(tempDir, 0o755))
		ctx = filemanager.WithDefault(ctx, baseDir, tempDir, -1, -1)
	}
	return ctx
}

func w8InstallTransport(ctx context.Context, transport *w8Transport) {
	service.MustRegister[adapter.HTTPClientManager](ctx, &w8HTTPClientManager{transport: transport})
}

func w8InstallCacheFile(ctx context.Context, cacheFile *w8CacheFile) {
	service.MustRegister[adapter.CacheFile](ctx, cacheFile)
}

// newRuleSetUpdater wraps the product updater for one rule set.
func newRuleSetUpdater(ctx context.Context, ruleSet adapter.RuleSet) *rule.RuleSetUpdater {
	return rule.NewRuleSetUpdater(ctx, []adapter.RuleSet{ruleSet})
}

func w8NewRemoteRuleSet(t *testing.T, ctx context.Context, format string, updateInterval time.Duration) *rule.RemoteRuleSet {
	t.Helper()
	ruleSet, err := rule.NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), "w8-rule-set", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: format,
		RemoteOptions: option.RemoteRuleSet{
			URL:            w8RuleSetURL,
			UpdateInterval: badoption.Duration(updateInterval),
		},
	})
	require.NoError(t, err)
	return ruleSet
}

// w8RuleSetMatches asks the loaded generation through its PRODUCT match entry point
// (adapter.RuleSet.Match), not through an internal rule slice.
func w8RuleSetMatches(ruleSet adapter.RuleSet, domain string) bool {
	return ruleSet.Match(&adapter.InboundContext{Domain: domain})
}

// ---------------------------------------------------------------------------
// Disk census
// ---------------------------------------------------------------------------

type w8DiskCensus struct {
	Files       int   `json:"files"`
	Dirs        int   `json:"dirs"`
	Bytes       int64 `json:"bytes"`
	VersionDirs int   `json:"version_dirs"`
	Artifacts   int   `json:"artifacts"`
	Manifests   int   `json:"manifests"`
	Temps       int   `json:"temps"`
}

// w8Census walks the artifact store directory. It counts the version directories the
// store actually holds, not the versions the process happens to remember.
func w8Census(t *testing.T, storeDir string) (w8DiskCensus, []string) {
	t.Helper()
	var census w8DiskCensus
	var relativeFiles []string
	_, statErr := os.Stat(storeDir)
	if os.IsNotExist(statErr) {
		return census, relativeFiles
	}
	require.NoError(t, statErr)
	walkErr := filepath.WalkDir(storeDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relative, relErr := filepath.Rel(storeDir, path)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			census.Dirs++
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		census.Files++
		census.Bytes += info.Size()
		relativeFiles = append(relativeFiles, filepath.ToSlash(relative))
		switch entry.Name() {
		case "artifact.bin":
			census.Artifacts++
		case "manifest.json":
			census.Manifests++
		}
		if len(relative) > 0 && entry.Name() == "artifact.bin" {
			census.VersionDirs++
		}
		if len(entry.Name()) > 5 && entry.Name()[:5] == ".tmp-" {
			census.Temps++
		}
		return nil
	})
	require.NoError(t, walkErr)
	return census, relativeFiles
}

// w8StoreVersionDigest finds the first published version in a store directory and returns
// its key, version id and artifact digest/size. The store layout is <key>/<versionID>/
// {artifact.bin,manifest.json}; the harness uses the layout rather than assuming an id.
func w8StoreVersionDigest(t *testing.T, storeDir string) (key string, versionID string, digest string, size int64) {
	t.Helper()
	keys, err := os.ReadDir(storeDir)
	require.NoError(t, err, "artifact store %s does not exist", storeDir)
	for _, keyEntry := range keys {
		if !keyEntry.IsDir() {
			continue
		}
		versions, versionErr := os.ReadDir(filepath.Join(storeDir, keyEntry.Name()))
		require.NoError(t, versionErr)
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			artifactPath := filepath.Join(storeDir, keyEntry.Name(), versionEntry.Name(), "artifact.bin")
			content, readErr := os.ReadFile(artifactPath)
			if readErr != nil {
				continue
			}
			return keyEntry.Name(), versionEntry.Name(), w8SHA256(content), int64(len(content))
		}
	}
	t.Fatalf("no published version under %s", storeDir)
	return "", "", "", 0
}

func w8SHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum[:])
}

// w8ReadJSONFile is used to inspect a stored manifest without importing its private type.
func w8ReadJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(content, &decoded))
	return decoded
}

func w8NowStamp() string { return time.Now().Format("150405.000") }
