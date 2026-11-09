package rule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/sing/service/pause"

	"go4.org/netipx"
)

var _ adapter.RuleSet = (*RemoteRuleSet)(nil)

// remoteRuleSetHTTPTimeout bounds one refresh. The updater runs every remote rule-set
// sequentially, so without a per-client deadline a server that completes the handshake and then
// never sends response headers blocks every later rule-set for the life of the session.
const remoteRuleSetHTTPTimeout = 5 * time.Minute

type RemoteRuleSet struct {
	ctx            context.Context
	cancel         context.CancelFunc
	logger         logger.ContextLogger
	outbound       adapter.OutboundManager
	tag            string
	url            string
	urlHash        [32]byte
	initialPath    string
	options        option.RuleSet
	updateInterval time.Duration
	httpClient     *http.Client
	access         sync.RWMutex
	rules          []adapter.HeadlessRule
	metadata       adapter.RuleSetMetadata
	lastUpdated    time.Time
	lastEtag       string
	metaAccess     sync.Mutex
	cacheFile      adapter.CacheFile
	pauseManager   pause.Manager
	callbacks      list.List[adapter.RuleSetUpdateCallback]
	refs           atomic.Int32
	closed         atomic.Bool
	// artifactStore is the versioned store the downloaded bytes are published into, or nil when
	// it could not be opened (the legacy cache keeps working in that case). artifactKey names the
	// store entry for this rule set: its tag and source URL, so two tags pointing at one document
	// share a version and two sources never do.
	artifactStore *artifact.Store
	artifactKey   string
	// artifactLease is the source version the live rules were built from. It is held until the
	// next publish replaces it or the set closes, so a collector can never remove the bytes a
	// live rule set still needs.
	artifactLease *artifact.Version
	// generation orders overlapping refreshes: a fetch that started before another one must not
	// publish after the newer one committed. It is compared immediately before the live rules are
	// swapped in.
	generation atomic.Uint64
}

func NewRemoteRuleSet(ctx context.Context, logger logger.ContextLogger, tag string, options option.RuleSet) (*RemoteRuleSet, error) {
	ctx, cancel := context.WithCancel(ctx)
	var updateInterval time.Duration
	if options.RemoteOptions.UpdateInterval > 0 {
		updateInterval = time.Duration(options.RemoteOptions.UpdateInterval)
	} else {
		updateInterval = 24 * time.Hour
	}
	var initialPath string
	if options.RemoteOptions.InitialPath != "" {
		initialPath = filemanager.BasePath(ctx, strings.ReplaceAll(options.RemoteOptions.InitialPath, C.RuleSetTagPlaceholder, tag))
		initialPath, _ = filepath.Abs(initialPath)
	}
	url := strings.ReplaceAll(options.RemoteOptions.URL, C.RuleSetTagPlaceholder, tag)
	return &RemoteRuleSet{
		ctx:            ctx,
		cancel:         cancel,
		outbound:       service.FromContext[adapter.OutboundManager](ctx),
		logger:         logger,
		tag:            tag,
		url:            url,
		urlHash:        sha256.Sum256([]byte(url)),
		initialPath:    initialPath,
		options:        options,
		updateInterval: updateInterval,
		pauseManager:   service.FromContext[pause.Manager](ctx),
	}, nil
}

func (s *RemoteRuleSet) Name() string {
	return s.tag
}

// currentRules returns the published rule slice, or nil once it has been released.
//
// Every read of `rules` goes through here or through a lock held for a longer section, because the
// slice is written from three places that are NOT the caller: `loadBytes` publishes a new one on each
// update, `updateOnce` and `Cleanup` release it when nothing references the set any more, and `Close`
// releases it on shutdown. `Match` runs on the dial path while a reload can be in flight, so reading
// the field directly is exactly the race `go test -race` reported between `loadBytes` and `Match`.
//
// The snapshot is taken under the read lock and used after it is dropped. That is sound because a
// published slice is never mutated in place - an update assigns a brand new slice - so the header this
// returns stays valid and keeps pointing at immutable contents. Holding the lock for the whole match
// would work too, but it would block a reload for the duration of a packet's rule evaluation for no
// benefit.
func (s *RemoteRuleSet) currentRules() []adapter.HeadlessRule {
	s.access.RLock()
	defer s.access.RUnlock()
	return s.rules
}

func (s *RemoteRuleSet) String() string {
	return strings.Join(F.MapToString(s.currentRules()), " ")
}

func (s *RemoteRuleSet) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	s.cacheFile = service.FromContext[adapter.CacheFile](s.ctx)
	s.openArtifactStore()
	transport, err := s.resolveTransport()
	if err != nil {
		return E.Cause(err, "create rule-set http client")
	}
	startContext.Register(transport)
	s.httpClient = &http.Client{Transport: transport, Timeout: remoteRuleSetHTTPTimeout}
	restored := false
	if s.artifactStore != nil {
		restored = s.restoreStoredRuleSet()
	}
	if !restored && s.cacheFile != nil {
		restored = s.restoreCachedRuleSet()
	}
	var loadedFromInitialPath bool
	if !restored && s.lastUpdateTime().IsZero() && s.initialPath != "" {
		var content []byte
		content, err = filemanager.ReadFile(s.ctx, s.initialPath)
		if err == nil {
			err = s.loadBytes(content)
		}
		if err != nil {
			s.logger.Warn(E.Cause(err, "load initial rule-set from ", s.initialPath))
		} else {
			loadedFromInitialPath = true
		}
	}
	if s.lastUpdateTime().IsZero() && !loadedFromInitialPath {
		err = s.fetch(ctx, true)
		if err != nil {
			return E.Cause(err, "initial rule-set: ", s.tag)
		}
	}
	return nil
}

// openArtifactStore prepares the versioned store the downloaded bytes are kept in.
//
// The store lives under the process's file base directory, next to the other persisted state, and
// every rule set opens the same directory: the store shares leases across handles by canonical
// path, so one rule set's collector cannot remove another's live version.
func (s *RemoteRuleSet) openArtifactStore() {
	store, err := artifact.Open(filemanager.BasePath(s.ctx, "rule-set-artifacts"))
	if err != nil {
		s.logger.Warn(E.Cause(err, "open rule-set artifact store"))
		return
	}
	s.artifactStore = store
	s.artifactKey = artifactKeyFor(s.tag, s.url)
}

// artifactKeyFor names the store entry for one rule set: the tag (sanitised, because it becomes a
// directory name) and a digest of the source URL. The URL is part of the identity so re-pointing a
// tag at another document does not load the previous document's bytes as if they were current.
func artifactKeyFor(tag string, sourceURL string) string {
	name := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_', character == '.':
			return character
		default:
			return '_'
		}
	}, tag)
	if name == "" {
		name = "rule-set"
	}
	digest := sha256.Sum256([]byte(sourceURL))
	return name + "-" + hex.EncodeToString(digest[:8])
}

// newArtifactManifest records what produced the bytes and what they are: the format, the build
// that wrote them, the tag, and the exact source URL (with its digest, so two URLs that sanitise
// to the same key are still distinguishable in the manifest).
func (s *RemoteRuleSet) newArtifactManifest() artifact.Manifest {
	return artifact.NewManifest(
		"rule-set/"+s.options.Format,
		C.Version,
		"tag="+s.tag,
		"url="+s.url,
		"url-sha256="+hex.EncodeToString(s.urlHash[:]),
		"cache-key="+s.tag,
	)
}

// referenceSet returns the per-Box record of exact artifact versions, or nil outside a Box.
func (s *RemoteRuleSet) referenceSet() *artifact.ReferenceSet {
	return service.FromContext[*artifact.ReferenceSet](s.ctx)
}

// recordArtifact notes the exact version this rule set published, for the daemon to persist with
// the configuration that started it.
func (s *RemoteRuleSet) recordArtifact(version *artifact.Version) {
	if version == nil {
		return
	}
	if references := s.referenceSet(); references != nil {
		references.Record(s.artifactKey, version.ID())
	}
}

// restoreStoredRuleSet loads the stored version this Box is pinned to, or the newest one when the
// configuration did not name a version. The lease is what keeps the bytes alive for as long as this
// rule set serves them.
func (s *RemoteRuleSet) restoreStoredRuleSet() bool {
	var version *artifact.Version
	if references := s.referenceSet(); references != nil {
		if pinnedID, pinned := references.Pinned(s.artifactKey); pinned {
			loaded, err := s.artifactStore.LoadExact(s.artifactKey, pinnedID)
			if err == nil {
				version = loaded
			} else {
				s.logger.Warn(E.Cause(err, "pinned rule-set version ", pinnedID, " is not loadable; falling back to the newest"))
			}
		}
	}
	if version == nil {
		loaded, err := s.artifactStore.Load(s.artifactKey)
		if err != nil {
			return false
		}
		version = loaded
	}
	content, readErr := os.ReadFile(version.Path())
	if readErr == nil {
		readErr = s.loadBytes(content)
	}
	if readErr != nil {
		version.Release()
		s.logger.Warn(E.Cause(readErr, "restore stored rule-set, will refetch"))
		return false
	}
	s.adoptArtifact(version)
	s.recordArtifact(version)
	s.setUpdateMetadata(time.Unix(0, version.Manifest().Created), "")
	return true
}

// restoreCachedRuleSet reads the legacy single-slot cache. An installation that predates the
// versioned store keeps its rules, and the bytes are migrated into the store once so the next
// start (and a pinned recovery) can find them there. The legacy cache is not removed: an older
// build that only knows the cache keeps working.
func (s *RemoteRuleSet) restoreCachedRuleSet() bool {
	savedSet := s.cacheFile.LoadRuleSet(s.tag)
	if savedSet == nil {
		return false
	}
	if len(savedSet.URLHash) > 0 && !bytes.Equal(savedSet.URLHash, s.urlHash[:]) {
		s.logger.Info("cached rule-set was downloaded from another URL, will refetch")
		return false
	}
	if err := s.loadBytes(savedSet.Content); err != nil {
		s.logger.Warn(E.Cause(err, "restore cached rule-set, will refetch"))
		return false
	}
	s.setUpdateMetadata(savedSet.LastUpdated, savedSet.LastEtag)
	s.migrateCachedRuleSet(savedSet.Content, savedSet.LastUpdated)
	return true
}

// migrateCachedRuleSet copies one legacy cache entry into the versioned store.
func (s *RemoteRuleSet) migrateCachedRuleSet(content []byte, updated time.Time) {
	if s.artifactStore == nil {
		return
	}
	manifest := s.newArtifactManifest()
	if !updated.IsZero() {
		manifest.Created = updated.UnixNano()
	}
	version, err := s.artifactStore.Commit(s.artifactKey, manifest, bytes.NewReader(content))
	if err != nil {
		s.logger.Warn(E.Cause(err, "migrate cached rule-set"))
		return
	}
	s.adoptArtifact(version)
	s.recordArtifact(version)
}

// lastUpdateTime and updateMetadata read the refresh bookkeeping under the lock that concurrent
// refreshes write it through.
func (s *RemoteRuleSet) lastUpdateTime() time.Time {
	s.metaAccess.Lock()
	defer s.metaAccess.Unlock()
	return s.lastUpdated
}

func (s *RemoteRuleSet) updateMetadata() (time.Time, string) {
	s.metaAccess.Lock()
	defer s.metaAccess.Unlock()
	return s.lastUpdated, s.lastEtag
}

func (s *RemoteRuleSet) setUpdateMetadata(updated time.Time, etag string) {
	s.metaAccess.Lock()
	s.lastUpdated = updated
	s.lastEtag = etag
	s.metaAccess.Unlock()
}

// adoptArtifact makes version the source of the live rules and releases the one it replaces.
func (s *RemoteRuleSet) adoptArtifact(version *artifact.Version) {
	s.access.Lock()
	previous := s.artifactLease
	s.artifactLease = version
	s.access.Unlock()
	if previous != nil && previous != version {
		previous.Release()
	}
}

func (s *RemoteRuleSet) Metadata() adapter.RuleSetMetadata {
	s.access.RLock()
	defer s.access.RUnlock()
	return s.metadata
}

func (s *RemoteRuleSet) ExtractIPSet() []*netipx.IPSet {
	s.access.RLock()
	defer s.access.RUnlock()
	return common.FlatMap(s.rules, extractIPSetFromRule)
}

func (s *RemoteRuleSet) IncRef() {
	s.refs.Add(1)
}

func (s *RemoteRuleSet) DecRef() {
	if s.refs.Add(-1) < 0 {
		panic("rule-set: negative refs")
	}
}

func (s *RemoteRuleSet) Cleanup() {
	if s.refs.Load() == 0 {
		s.access.Lock()
		s.rules = nil
		s.access.Unlock()
	}
}

func (s *RemoteRuleSet) RegisterCallback(callback adapter.RuleSetUpdateCallback) *list.Element[adapter.RuleSetUpdateCallback] {
	s.access.Lock()
	defer s.access.Unlock()
	return s.callbacks.PushBack(callback)
}

func (s *RemoteRuleSet) UnregisterCallback(element *list.Element[adapter.RuleSetUpdateCallback]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.callbacks.Remove(element)
}

func (s *RemoteRuleSet) loadBytes(content []byte) error {
	ruleSet, err := s.decodeRuleSet(content)
	if err != nil {
		return err
	}
	return s.publishRules(ruleSet)
}

// decodeRuleSet parses the downloaded bytes according to the declared format. It touches no live
// state: a candidate that fails here has published nothing.
func (s *RemoteRuleSet) decodeRuleSet(content []byte) (option.PlainRuleSetCompat, error) {
	switch s.options.Format {
	case C.RuleSetFormatSource:
		return json.UnmarshalExtended[option.PlainRuleSetCompat](content)
	case C.RuleSetFormatBinary:
		return srs.Read(bytes.NewReader(content), false)
	default:
		return option.PlainRuleSetCompat{}, E.New("unknown rule-set format: ", s.options.Format)
	}
}

// buildRules turns a parsed document into the headless rules and metadata the router serves. This
// is the validation step: nothing is visible until it succeeds.
func (s *RemoteRuleSet) buildRules(ruleSet option.PlainRuleSetCompat) ([]adapter.HeadlessRule, adapter.RuleSetMetadata, error) {
	plainRuleSet, err := mmapRuleSet(s.ctx, s.logger, s.tag, ruleSet).Upgrade()
	if err != nil {
		return nil, adapter.RuleSetMetadata{}, err
	}
	rules := make([]adapter.HeadlessRule, len(plainRuleSet.Rules))
	for i, ruleOptions := range plainRuleSet.Rules {
		rules[i], err = NewHeadlessRule(s.ctx, ruleOptions)
		if err != nil {
			return nil, adapter.RuleSetMetadata{}, E.Cause(err, "parse rule_set.rules.[", i, "]")
		}
	}
	metadata := buildRuleSetMetadata(plainRuleSet.Rules)
	err = validateRuleSetMetadataUpdate(s.ctx, s.tag, metadata)
	if err != nil {
		return nil, adapter.RuleSetMetadata{}, err
	}
	return rules, metadata, nil
}

// loadBinaryStream parses a binary rule-set straight from the response body.
//
// # Why this is not just loadBytes without the slice
//
// A binary rule-set is already a stream (`srs.Read` decompresses it as it parses), and the parsed
// form is compact: the v3+ format stores a succinct matcher, not the domain strings. Reading the
// whole compressed body into a []byte first therefore adds a second, temporary copy of the entire
// download to the load's peak for no parsing benefit. On a memory-limited platform that peak is
// what matters: a 10 MiB rule-set costs 10 MiB of avoidable transient on top of the parsed form.
//
// The cache path cannot use this: `CacheFile.SaveRuleSet` takes the bytes and stores them, so the
// download must be retained in some form. The streaming path is therefore used only when there is
// no cache file to write; a cached profile keeps the previous behaviour exactly.
//
// # Why the body is drained after parsing
//
// `srs.Read` now verifies its own zlib stream (checksum and truncation), but it stops at the end of
// that stream: bytes after it, or a body shorter than the HTTP framing claims once the zlib stream
// is complete, are outside the parser's view. The read-all path read the body to EOF, so such a
// download failed the update. Draining preserves that HTTP-level check, and copies the remainder
// through one small buffer instead of retaining it, so the peak win stands.
//
// The drain happens BEFORE publish, and its error is returned, for that reason. (It also lets
// net/http return the connection to the idle pool, but the updater closes idle connections after
// every non-initial refresh anyway; the framing check is the load-bearing reason.)
func (s *RemoteRuleSet) loadBinaryStream(body io.ReadCloser, generation uint64) error {
	ruleSet, err := srs.Read(body, false)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	rules, metadata, err := s.buildRules(ruleSet)
	if err != nil {
		return err
	}
	if s.generation.Load() != generation {
		return E.New("rule-set update superseded by a newer one")
	}
	return s.publishBuilt(rules, metadata)
}

// publishRules builds and publishes one parsed generation. It is used by the paths that are not a
// network refresh (the initial path and the cache restore), where there is no generation to order.
func (s *RemoteRuleSet) publishRules(ruleSet option.PlainRuleSetCompat) error {
	rules, metadata, err := s.buildRules(ruleSet)
	if err != nil {
		return err
	}
	return s.publishBuilt(rules, metadata)
}

// publishBuilt is the one place a validated generation becomes the live one: store, then notify.
// Both loaders (bytes and stream) must go through it so a cache-less update cannot publish rules
// with different validation than a cached one.
func (s *RemoteRuleSet) publishBuilt(rules []adapter.HeadlessRule, metadata adapter.RuleSetMetadata) error {
	s.access.Lock()
	if s.closed.Load() {
		s.access.Unlock()
		return E.New("rule-set is closed")
	}
	s.metadata = metadata
	s.rules = rules
	callbacks := s.callbacks.Array()
	s.access.Unlock()
	if s.closed.Load() {
		return nil
	}
	for _, callback := range callbacks {
		callback(s)
	}
	return nil
}

func (s *RemoteRuleSet) updateOnce() {
	err := s.fetch(s.ctx, false)
	if err != nil {
		s.logger.Error("fetch rule-set ", s.tag, ": ", err)
	} else if s.refs.Load() == 0 {
		s.access.Lock()
		s.rules = nil
		s.access.Unlock()
	}
}

func (s *RemoteRuleSet) fetch(ctx context.Context, isStart bool) error {
	s.logger.Debug("updating rule-set ", s.tag, " from URL: ", s.url)
	// One refresh attempt is one generation. A refresh that started earlier and finishes later
	// must not publish over the one that started after it; the generation is checked immediately
	// before the live rules are swapped.
	generation := s.generation.Add(1)
	started := time.Now()
	request, err := http.NewRequest("GET", s.url, nil)
	if err != nil {
		return err
	}
	_, lastEtag := s.updateMetadata()
	if lastEtag != "" {
		request.Header.Set("If-None-Match", lastEtag)
	}
	if !isStart {
		defer s.httpClient.CloseIdleConnections()
	}
	// F03: a rule-set refresh is a BUILD - it downloads a document, parses it into a matcher
	// index and swaps it in - so it shares the box's build reservation with provider refreshes.
	// The reservation is taken BEFORE the request: a deferred build must not download at all,
	// and the version currently serving traffic stays untouched.
	buildRelease, buildErr := buildgate.AcquireFromContext(ctx)
	if buildErr != nil {
		s.logger.Debug("rule-set ", s.tag, " build deferred: ", buildErr)
		return buildErr
	}
	defer buildRelease()
	response, err := s.httpClient.Do(request.WithContext(ctx))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		updated := time.Now()
		s.setUpdateMetadata(updated, lastEtag)
		if s.cacheFile != nil {
			savedRuleSet := s.cacheFile.LoadRuleSet(s.tag)
			if savedRuleSet != nil {
				savedRuleSet.LastUpdated = updated
				savedRuleSet.URLHash = s.urlHash[:]
				err = s.cacheFile.SaveRuleSet(s.tag, savedRuleSet)
				if err != nil {
					s.logger.Error("save rule-set updated time: ", err)
					return nil
				}
			}
		}
		s.logger.Info("update rule-set ", s.tag, ": not modified")
		return nil
	default:
		return E.New("unexpected status: ", response.Status)
	}
	var content []byte
	if s.artifactStore == nil && s.cacheFile == nil && s.options.Format == C.RuleSetFormatBinary {
		// No store and no cache to write, so the download does not need to exist as a slice: parse
		// the stream. A configured store needs the bytes to commit them, and a cache needs them
		// to save them.
		err = s.loadBinaryStream(response.Body, generation)
	} else {
		content, err = io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		err = s.publishDownload(content, started, generation)
	}
	if err != nil {
		return err
	}
	eTagHeader := response.Header.Get("Etag")
	finalEtag := lastEtag
	if eTagHeader != "" {
		finalEtag = eTagHeader
	}
	updated := time.Now()
	s.setUpdateMetadata(updated, finalEtag)
	if s.cacheFile != nil {
		err = s.cacheFile.SaveRuleSet(s.tag, &adapter.SavedBinary{
			LastUpdated: updated,
			Content:     content,
			LastEtag:    finalEtag,
			URLHash:     s.urlHash[:],
		})
		if err != nil {
			s.logger.Error("save rule-set cache: ", err)
		}
	}
	s.logger.Info("updated rule-set ", s.tag)
	return nil
}

// publishDownload turns one downloaded body into a validated generation, stores the bytes as a
// version, and only then swaps the live rules in.
//
// The order is the contract: a body that does not parse or does not build publishes nothing; a
// version is committed before the rules that were built from it become live, so the lease this
// rule set takes can always be traced to the bytes it is serving; and a refresh superseded while
// it worked commits no live change, even though it may have stored its bytes.
func (s *RemoteRuleSet) publishDownload(content []byte, started time.Time, generation uint64) error {
	ruleSet, err := s.decodeRuleSet(content)
	if err != nil {
		return err
	}
	rules, metadata, err := s.buildRules(ruleSet)
	if err != nil {
		return err
	}
	var version *artifact.Version
	if s.artifactStore != nil {
		manifest := s.newArtifactManifest()
		// Created is the time the REFRESH STARTED, not the time it finished: an older refresh
		// that finishes later must not become the "newest" version a restart loads.
		manifest.Created = started.UnixNano()
		version, err = s.artifactStore.Commit(s.artifactKey, manifest, bytes.NewReader(content))
		if err != nil {
			return E.Cause(err, "store rule-set artifact")
		}
	}
	if s.generation.Load() != generation {
		if version != nil {
			version.Release()
		}
		return E.New("rule-set update superseded by a newer one")
	}
	if err = s.publishBuilt(rules, metadata); err != nil {
		if version != nil {
			version.Release()
		}
		return err
	}
	if version != nil {
		s.adoptArtifact(version)
		s.recordArtifact(version)
		// The live rules hold the newest version's lease, so collection cannot remove what they
		// serve. The previous configuration's last-good version is retained explicitly: it has no
		// reader but a recovery may still name it.
		var retained []string
		if references := s.referenceSet(); references != nil {
			if retainedID, ok := references.RetainedID(s.artifactKey); ok {
				retained = append(retained, retainedID)
			}
		}
		if collectErr := s.artifactStore.Collect(s.artifactKey, retained...); collectErr != nil {
			s.logger.Warn(E.Cause(collectErr, "collect rule-set artifacts"))
		}
	}
	return nil
}

func (s *RemoteRuleSet) resolveTransport() (adapter.HTTPTransport, error) {
	httpClientManager := service.FromContext[adapter.HTTPClientManager](s.ctx)
	if s.options.RemoteOptions.HTTPClient != nil && !s.options.RemoteOptions.HTTPClient.IsEmpty() {
		if s.options.RemoteOptions.DownloadDetour != "" { //nolint:staticcheck
			return nil, E.New("http_client is conflict with deprecated download_detour field")
		}
		return httpClientManager.ResolveTransport(s.ctx, s.logger, *s.options.RemoteOptions.HTTPClient)
	}
	if s.options.RemoteOptions.DownloadDetour != "" { //nolint:staticcheck
		deprecated.Report(s.ctx, deprecated.OptionLegacyRuleSetDownloadDetour)
		return httpClientManager.ResolveTransport(s.ctx, s.logger, option.HTTPClientOptions{
			DialerOptions: option.DialerOptions{
				Detour: s.options.RemoteOptions.DownloadDetour, //nolint:staticcheck
			},
			DisableEmptyDirectCheck: true,
		})
	}
	defaultTransport := httpClientManager.DefaultTransport()
	if defaultTransport == nil {
		return nil, E.New("default http client transport is not initialized")
	}
	return defaultTransport, nil
}

func (s *RemoteRuleSet) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.access.Lock()
	s.rules = nil
	lease := s.artifactLease
	s.artifactLease = nil
	s.access.Unlock()
	if lease != nil {
		lease.Release()
	}
	s.cancel()
	return nil
}

func (s *RemoteRuleSet) Match(metadata *adapter.InboundContext) bool {
	return matchAnyHeadlessRule(s.currentRules(), metadata)
}

func (s *RemoteRuleSet) mergeableRule() *DefaultHeadlessRule {
	return mergeableRuleIn(s.currentRules())
}
