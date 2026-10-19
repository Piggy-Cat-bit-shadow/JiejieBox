package rule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
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
	cacheFile      adapter.CacheFile
	pauseManager   pause.Manager
	callbacks      list.List[adapter.RuleSetUpdateCallback]
	refs           atomic.Int32
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
	transport, err := s.resolveTransport()
	if err != nil {
		return E.Cause(err, "create rule-set http client")
	}
	startContext.Register(transport)
	s.httpClient = &http.Client{Transport: transport}
	if s.cacheFile != nil {
		savedSet := s.cacheFile.LoadRuleSet(s.tag)
		if savedSet != nil {
			if len(savedSet.URLHash) > 0 && !bytes.Equal(savedSet.URLHash, s.urlHash[:]) {
				s.logger.Info("cached rule-set was downloaded from another URL, will refetch")
			} else {
				err = s.loadBytes(savedSet.Content)
				if err != nil {
					s.logger.Warn(E.Cause(err, "restore cached rule-set, will refetch"))
				} else {
					s.lastUpdated = savedSet.LastUpdated
					s.lastEtag = savedSet.LastEtag
				}
			}
		}
	}
	var loadedFromInitialPath bool
	if s.lastUpdated.IsZero() && s.initialPath != "" {
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
	if s.lastUpdated.IsZero() && !loadedFromInitialPath {
		err = s.fetch(ctx, true)
		if err != nil {
			return E.Cause(err, "initial rule-set: ", s.tag)
		}
	}
	return nil
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
	var (
		ruleSet option.PlainRuleSetCompat
		err     error
	)
	switch s.options.Format {
	case C.RuleSetFormatSource:
		ruleSet, err = json.UnmarshalExtended[option.PlainRuleSetCompat](content)
		if err != nil {
			return err
		}
	case C.RuleSetFormatBinary:
		ruleSet, err = srs.Read(bytes.NewReader(content), false)
		if err != nil {
			return err
		}
	default:
		return E.New("unknown rule-set format: ", s.options.Format)
	}
	return s.publishRules(ruleSet)
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
// `srs.Read` stops at the end of the declared rules; the zlib footer and any bytes after it are
// still unread. The read-all path consumed the body to EOF, so a download truncated after the last
// rule failed the update with an unexpected EOF and left the previous generation live. A streaming
// parse that returns as soon as the rules are complete would publish that same truncated download:
// a silent failure-semantics change. Draining restores the old order and error, and copies the
// remainder through one small buffer instead of retaining it, so the peak win stands.
//
// The drain happens BEFORE publish, and its error is returned, for that reason. (It also lets
// net/http return the connection to the idle pool, but the updater closes idle connections after
// every non-initial refresh anyway; the truncation check is the load-bearing reason.)
func (s *RemoteRuleSet) loadBinaryStream(body io.ReadCloser) error {
	ruleSet, err := srs.Read(body, false)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	return s.publishRules(ruleSet)
}

// publishRules is the one place a parsed generation becomes the live one: build, validate, store,
// then notify. Both loaders (bytes and stream) must go through it so a cache-less update cannot
// publish rules with different validation than a cached one.
func (s *RemoteRuleSet) publishRules(ruleSet option.PlainRuleSetCompat) error {
	plainRuleSet, err := mmapRuleSet(s.ctx, s.logger, s.tag, ruleSet).Upgrade()
	if err != nil {
		return err
	}
	rules := make([]adapter.HeadlessRule, len(plainRuleSet.Rules))
	for i, ruleOptions := range plainRuleSet.Rules {
		rules[i], err = NewHeadlessRule(s.ctx, ruleOptions)
		if err != nil {
			return E.Cause(err, "parse rule_set.rules.[", i, "]")
		}
	}
	metadata := buildRuleSetMetadata(plainRuleSet.Rules)
	err = validateRuleSetMetadataUpdate(s.ctx, s.tag, metadata)
	if err != nil {
		return err
	}
	s.access.Lock()
	s.metadata = metadata
	s.rules = rules
	callbacks := s.callbacks.Array()
	s.access.Unlock()
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
	request, err := http.NewRequest("GET", s.url, nil)
	if err != nil {
		return err
	}
	if s.lastEtag != "" {
		request.Header.Set("If-None-Match", s.lastEtag)
	}
	if !isStart {
		defer s.httpClient.CloseIdleConnections()
	}
	response, err := s.httpClient.Do(request.WithContext(ctx))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		s.lastUpdated = time.Now()
		if s.cacheFile != nil {
			savedRuleSet := s.cacheFile.LoadRuleSet(s.tag)
			if savedRuleSet != nil {
				savedRuleSet.LastUpdated = s.lastUpdated
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
	if s.cacheFile == nil && s.options.Format == C.RuleSetFormatBinary {
		// No cache to write, so the download does not need to exist as a slice: parse the stream.
		err = s.loadBinaryStream(response.Body)
	} else {
		content, err = io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		err = s.loadBytes(content)
	}
	if err != nil {
		return err
	}
	eTagHeader := response.Header.Get("Etag")
	if eTagHeader != "" {
		s.lastEtag = eTagHeader
	}
	s.lastUpdated = time.Now()
	if s.cacheFile != nil {
		err = s.cacheFile.SaveRuleSet(s.tag, &adapter.SavedBinary{
			LastUpdated: s.lastUpdated,
			Content:     content,
			LastEtag:    s.lastEtag,
			URLHash:     s.urlHash[:],
		})
		if err != nil {
			s.logger.Error("save rule-set cache: ", err)
		}
	}
	s.logger.Info("updated rule-set ", s.tag)
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
	s.access.Lock()
	s.rules = nil
	s.access.Unlock()
	s.cancel()
	return nil
}

func (s *RemoteRuleSet) Match(metadata *adapter.InboundContext) bool {
	return matchAnyHeadlessRule(s.currentRules(), metadata)
}

func (s *RemoteRuleSet) mergeableRule() *DefaultHeadlessRule {
	return mergeableRuleIn(s.currentRules())
}
