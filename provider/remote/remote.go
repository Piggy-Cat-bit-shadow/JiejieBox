package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/buildgate"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

func RegisterProvider(registry *provider.Registry) {
	provider.Register[option.ProviderRemoteOptions](registry, C.ProviderTypeRemote, NewProviderRemote)
}

const (
	// defaultFetchTimeout bounds one subscription download, including reading the body. It is the
	// difference between "this airport is slow" and "this provider silently stopped updating".
	defaultFetchTimeout = 30 * time.Second
	// defaultMaxDocumentSize is a safety limit for an untrusted document, not a product limit: a
	// few thousand nodes fit well below it, while an endless or decompression-bomb body does not.
	defaultMaxDocumentSize = 10 << 20
)

var _ adapter.Provider = (*ProviderRemote)(nil)

type ProviderRemote struct {
	provider.Adapter
	ctx              context.Context
	fetchAccess      sync.Mutex
	workers          sync.WaitGroup
	cancel           context.CancelFunc
	logger           log.ContextLogger
	outbound         adapter.OutboundManager
	provider         adapter.ProviderManager
	cacheFile        adapter.CacheFile
	httpClient       *http.Client
	infoMu           sync.RWMutex
	lastEtag         string
	lastOutOpts      []option.Outbound
	lastUpdated      time.Time
	subscriptionInfo adapter.SubscriptionInfo
	ticker           *time.Ticker
	updating         atomic.Bool

	// Application report state (C03). The revision counters are separate so an attempt can be
	// counted without claiming a document was applied.
	requestedRevision atomic.Uint64
	appliedRevision   atomic.Uint64
	reportAccess      sync.Mutex
	lastReport        *ApplyReport

	httpClientOptions *option.HTTPClientOptions
	downloadDetour    string
	url               string
	// artifactStore keeps the exact node snapshot a configuration used, so a recovery can pin it
	// instead of accepting whatever the subscription serves now.
	artifactStore *artifact.Store
	artifactKey   string
	artifactLease *artifact.Version
	// logURL is the subscription URL with its credentials, query and fragment removed, so a token
	// in a query parameter never reaches the log. The raw s.url is used only for the request.
	logURL         string
	hostIsDomain   bool
	urlHash        [32]byte
	path           string
	initialPath    string
	userAgent      string
	updateInterval time.Duration
	// fetchTimeout bounds one download INCLUDING reading the body. Without it a server that accepts
	// the connection and then stalls would block the refresh worker forever: no later refresh could
	// run (the updating flag stays set) and the provider would silently stop updating.
	fetchTimeout time.Duration
	// maxDocumentSize bounds the policy document read into memory. It is a safety limit for
	// untrusted input, not a product limit; a real node list is far below it.
	maxDocumentSize int64
	exclude         *regexp.Regexp
	include         *regexp.Regexp

	overrideDialer *option.OverrideDialerOptions
}

func NewProviderRemote(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderRemoteOptions) (adapter.Provider, error) {
	if options.URL == "" {
		return nil, E.New("provider URL is required")
	}
	parsedURL, err := url.Parse(options.URL)
	if err != nil {
		return nil, E.Cause(err, "provider URL is not a valid URL")
	}
	switch parsedURL.Scheme {
	case "http", "https":
	default:
		return nil, E.New("provider URL must use http or https, got ", strconv.Quote(parsedURL.Scheme))
	}
	if parsedURL.Host == "" {
		return nil, E.New("provider URL has no host: ", redactedURL(parsedURL))
	}
	if options.Path != "" && options.InitialPath != "" {
		return nil, E.New("provider path and initial_path are mutually exclusive")
	}
	var (
		path        string
		initialPath string
	)
	if options.Path != "" {
		path = filemanager.BasePath(ctx, options.Path)
		path, _ = filepath.Abs(path)
	}
	if options.InitialPath != "" {
		initialPath = filemanager.BasePath(ctx, options.InitialPath)
		initialPath, _ = filepath.Abs(initialPath)
	}
	if path != "" {
		info, err := filemanager.Stat(ctx, path)
		if err == nil && info.IsDir() {
			return nil, E.New("provider path is a directory: ", path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, E.Cause(err, "check provider path")
		}
	}
	if initialPath != "" {
		info, err := filemanager.Stat(ctx, initialPath)
		if err == nil && info.IsDir() {
			return nil, E.New("provider initial_path is a directory: ", initialPath)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, E.Cause(err, "check provider initial_path")
		}
	}
	updateInterval := time.Duration(options.UpdateInterval)
	if updateInterval <= 0 {
		updateInterval = 24 * time.Hour
	}
	if updateInterval < time.Hour {
		updateInterval = time.Hour
	}
	if options.UserAgent != "" && options.HTTPClient != nil && !options.HTTPClient.IsEmpty() {
		return nil, E.New("user_agent conflicts with http_client: configure User-Agent via http_client.headers instead")
	}
	var userAgent string
	if options.UserAgent == "" {
		userAgent = "sing-box " + C.Version
	} else {
		userAgent = options.UserAgent
	}
	ctx, cancel := context.WithCancel(ctx)
	outbound := service.FromContext[adapter.OutboundManager](ctx)
	logger := logFactory.NewLogger(F.ToString("provider/remote", "[", tag, "]"))
	providerURL := options.URL
	return &ProviderRemote{
		Adapter:  provider.NewAdapter(ctx, router, outbound, logFactory, logger, tag, C.ProviderTypeRemote, options.HealthCheck),
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger,
		outbound: outbound,
		provider: service.FromContext[adapter.ProviderManager](ctx),

		httpClientOptions: options.HTTPClient,
		url:               providerURL,
		logURL:            redactedURL(parsedURL),
		hostIsDomain:      parsedURL.Hostname() != "" && net.ParseIP(parsedURL.Hostname()) == nil,
		urlHash:           sha256.Sum256([]byte(providerURL)),
		path:              path,
		initialPath:       initialPath,
		userAgent:         userAgent,
		updateInterval:    updateInterval,
		fetchTimeout:      defaultFetchTimeout,
		maxDocumentSize:   defaultMaxDocumentSize,
		exclude:           (*regexp.Regexp)(options.Exclude),
		include:           (*regexp.Regexp)(options.Include),

		overrideDialer: options.OverrideDialer,

		//nolint:staticcheck
		downloadDetour: options.DownloadDetour,
	}, nil
}

func (s *ProviderRemote) referenceSet() *artifact.ReferenceSet {
	return service.FromContext[*artifact.ReferenceSet](s.ctx)
}

// openArtifactStore prepares the per-Box snapshot store for this provider.
func (s *ProviderRemote) openArtifactStore() {
	base := filemanager.BasePath(s.ctx, "")
	if base == "" {
		return
	}
	store, err := artifact.Open(filepath.Join(base, "provider-artifacts"))
	if err != nil {
		s.logger.Warn(E.Cause(err, "open provider artifact store"))
		return
	}
	s.artifactStore = store
	s.artifactKey = providerArtifactKey(s.Tag(), s.url)
}

func providerArtifactKey(tag string, source string) string {
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
		name = "provider"
	}
	digest := sha256.Sum256([]byte(source))
	return name + "-" + hex.EncodeToString(digest[:8])
}

func (s *ProviderRemote) providerManifest() artifact.Manifest {
	return artifact.NewManifest(
		"provider/remote",
		C.Version,
		"tag="+s.Tag(),
		"url="+s.url,
		"url-sha256="+hex.EncodeToString(s.urlHash[:]),
	)
}

// restorePinned loads the exact node snapshot a recovered configuration names.
//
// A pinned snapshot that cannot be loaded fails the start instead of silently taking the current
// subscription: a recovery report must not claim old nodes while serving new ones.
func (s *ProviderRemote) restorePinned() (bool, error) {
	references := s.referenceSet()
	if references == nil || s.artifactStore == nil {
		return false, nil
	}
	pinnedID, pinned := references.Pinned(s.artifactKey)
	if !pinned {
		return false, nil
	}
	version, err := s.artifactStore.LoadExact(s.artifactKey, pinnedID)
	if err != nil {
		return false, E.Cause(err, "pinned provider snapshot ", pinnedID, " is not loadable")
	}
	content, err := os.ReadFile(version.Path())
	if err != nil {
		version.Release()
		return false, E.Cause(err, "read pinned provider snapshot")
	}
	if err = s.updateProviderFromContent(string(content)); err != nil {
		version.Release()
		return false, err
	}
	s.adoptArtifact(version)
	references.Record(s.artifactKey, version.ID())
	// The restored document is as fresh as the snapshot; without this the update loop would see a
	// zero lastUpdated and immediately refetch the changed subscription.
	s.infoMu.Lock()
	s.lastUpdated = time.Unix(0, version.Manifest().Created)
	s.infoMu.Unlock()
	return true, nil
}

// snapshotPublishedNodes stores the normalized node document that this provider just published.
func (s *ProviderRemote) snapshotPublishedNodes() {
	if s.artifactStore == nil || s.lastOutOpts == nil {
		return
	}
	content, err := json.Marshal(option.Options{Outbounds: s.lastOutOpts})
	if err != nil {
		return
	}
	version, err := s.artifactStore.Commit(s.artifactKey, s.providerManifest(), bytes.NewReader(content))
	if err != nil {
		s.logger.Warn(E.Cause(err, "store provider snapshot"))
		return
	}
	s.adoptArtifact(version)
	if references := s.referenceSet(); references != nil {
		references.Record(s.artifactKey, version.ID())
	}
	var retained []string
	if references := s.referenceSet(); references != nil {
		if retainedID, ok := references.RetainedID(s.artifactKey); ok {
			retained = append(retained, retainedID)
		}
	}
	if collectErr := s.artifactStore.Collect(s.artifactKey, retained...); collectErr != nil {
		s.logger.Warn(E.Cause(collectErr, "collect provider snapshots"))
	}
}

// adoptArtifact installs the lease of the snapshot the published nodes came from.
func (s *ProviderRemote) adoptArtifact(version *artifact.Version) {
	previous := s.artifactLease
	s.artifactLease = version
	if previous != nil && previous != version {
		previous.Release()
	}
}

func (s *ProviderRemote) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	s.cacheFile = service.FromContext[adapter.CacheFile](s.ctx)
	s.openArtifactStore()
	restored, pinErr := s.restorePinned()
	if pinErr != nil {
		return pinErr
	}
	if !restored {
		loadedFromCache, err := s.loadCacheFile()
		if err != nil {
			s.logger.Warn(E.Cause(err, "restore cached outbound provider, will refetch"))
		}
		if loadedFromCache {
			restored = true
		}
		if !loadedFromCache && s.initialPath != "" {
			err = s.loadInitialPath()
			if err != nil {
				s.logger.Warn(E.Cause(err, "load initial outbound provider from ", s.initialPath))
			} else {
				restored = true
			}
		}
		s.snapshotPublishedNodes()
	}
	// A download routed through a group this provider itself feeds can never complete its first
	// fetch: the group has no member until the fetch publishes one. Refuse the configuration with a
	// message naming the loop, instead of leaving a group that is permanently empty. A restored
	// document already populated the group, so the check is only about the bootstrap case.
	//
	// The implicit default transport follows `route.final` (dialer.Options.DefaultOutbound), so it
	// is part of the download path even when the provider configures no http_client at all - the
	// common shape where `route.final` is the subscription's own group.
	if !restored {
		detour := s.downloadDetourTag()
		followsDefaultOutbound := detour == "" && (s.httpClientOptions == nil || s.httpClientOptions.IsEmpty())
		if err := s.Adapter.CheckDownloadPath(detour, followsDefaultOutbound); err != nil {
			return err
		}
		// The HTTP client cannot dial before it resolved its own server name. When that resolution
		// goes through an explicitly configured or route-default DNS server, that server's detour
		// chain is part of the download path too.
		if s.hostIsDomain {
			if resolverTag := s.downloadResolverTag(); resolverTag != "" {
				if err := s.Adapter.CheckDownloadResolver(resolverTag); err != nil {
					return err
				}
			}
		}
	}
	transport, err := s.resolveTransport()
	if err != nil {
		return E.Cause(err, "create provider http client")
	}
	startContext.Register(transport)
	s.httpClient = &http.Client{Transport: transport, Timeout: s.fetchTimeout}
	if err = s.Adapter.Start(); err != nil {
		return err
	}
	s.ticker = time.NewTicker(s.updateInterval)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		s.loopUpdate()
	}()
	// Publish the nodes the cache produced. Groups registered their callbacks at their own Start,
	// which runs BEFORE this provider start (the outbound manager starts first), so this is the
	// notification that gives them their initial provider membership.
	//
	// The returned error is the graph validator refusing a membership. A cached document is
	// untrusted input, and a refusal here is the same defect a fresh download would hit; the Box
	// must not start with a group the validator has already proven recursive.
	if err = s.UpdateGroups(); err != nil {
		return err
	}
	// A provider with no cache performs its first download inside loopUpdate on its own goroutine:
	// a Box start must not block on a subscription URL, because the device may be offline, the
	// airport behind a captive portal, or the URL slow. Until that download completes, a
	// provider-only group has no members and its dials fail with a clear error rather than the
	// whole Box failing to start.
	return nil
}

// downloadResolverTag reports the DNS server the dialer would use to resolve the provider's own
// hostname: the explicit http_client resolver when one is configured, otherwise
// route.default_domain_resolver. An empty answer means the system resolver is used, which cannot
// depend on the provider's outbound nodes.
func (s *ProviderRemote) downloadResolverTag() string {
	if s.httpClientOptions != nil && !s.httpClientOptions.IsEmpty() &&
		s.httpClientOptions.DialerOptions.DomainResolver != nil {
		return s.httpClientOptions.DialerOptions.DomainResolver.Server
	}
	if reader, isReader := s.outbound.(interface{ DefaultDomainResolverTag() string }); isReader {
		return reader.DefaultDomainResolverTag()
	}
	return ""
}

// downloadDetourTag reports the outbound the download's dialer is configured to detour through.
//
// The two sources are mutually exclusive at resolveTransport time (a deprecated `download_detour`
// alongside an explicit http_client is refused there), so reading the effective one here cannot
// disagree with the transport that will be built.
func (s *ProviderRemote) downloadDetourTag() string {
	if s.httpClientOptions != nil && !s.httpClientOptions.IsEmpty() {
		return s.httpClientOptions.DialerOptions.Detour
	}
	return s.downloadDetour
}

func (s *ProviderRemote) Update() error {
	_, err := s.UpdateWithReport()
	return err
}

// UpdateWithReport refreshes the provider and returns what the attempt actually did.
//
// Update() keeps its error-only contract for existing callers; the report is additional and
// describes the same attempt.
func (s *ProviderRemote) UpdateWithReport() (*ApplyReport, error) {
	return s.UpdateWithReportContext(s.ctx)
}

// UpdateWithReportContext is the same refresh with the CALLER's context.
//
// # Why the caller's context has to reach the fetch
//
// `UpdateWithReport` fetched with the provider's own lifecycle context, so a caller's deadline was
// decorative: the daemon created a bounded context for the provider, checked it between retries,
// and then started an HTTP request that only the provider's shutdown could cancel. On a slow or
// black-holed subscription endpoint the caller's bound therefore did not bound anything - the
// request ran to the provider's own `fetch_timeout` (30s by default), once per provider, inside a
// call whose caller had asked for seconds.
//
// The fetch itself already applies the context to the request (`req.WithContext`), so passing the
// caller's context through is the whole fix.
//
// # Why a refused attempt returns NO report
//
// `fetch` records a report for every attempt it starts, so `LastApplyReport` after a fetch is
// normally this attempt's. It is not when the attempt never started: a provider its own timer is
// already refreshing, one that is not started, or one the build gate deferred returns before the
// report exists, and `LastApplyReport` then still holds the PREVIOUS attempt - which may well say
// "committed". Handing that to a caller together with this attempt's error is how a publish from
// minutes ago gets reported as the result of a refresh that never happened. The revision counter is
// the evidence that the stored report belongs to this call, so it is checked rather than assumed.
func (s *ProviderRemote) UpdateWithReportContext(ctx context.Context) (*ApplyReport, error) {
	if s.ticker != nil {
		s.ticker.Reset(s.updateInterval)
	}
	if ctx == nil {
		ctx = s.ctx
	}
	previousRequested := s.lastRequestedRevision()
	err := s.fetch(ctx, false)
	report := s.LastApplyReport()
	if err != nil {
		if report == nil || report.RequestedRevision <= previousRequested {
			return nil, err
		}
		// The attempt ran and recorded its own report; both facts belong to the caller, because a
		// document that was published before the cancellation is still published.
		return report, err
	}
	return report, nil
}

func (s *ProviderRemote) UpdatedAt() time.Time {
	s.infoMu.RLock()
	defer s.infoMu.RUnlock()
	return s.lastUpdated
}

func (s *ProviderRemote) SubscriptionInfo() adapter.SubscriptionInfo {
	s.infoMu.RLock()
	defer s.infoMu.RUnlock()
	return s.subscriptionInfo
}

func (s *ProviderRemote) Close() error {
	s.cancel()
	s.workers.Wait()
	s.fetchAccess.Lock()
	defer s.fetchAccess.Unlock()
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.artifactLease != nil {
		s.artifactLease.Release()
		s.artifactLease = nil
	}
	return common.Close(&s.Adapter)
}

func (s *ProviderRemote) resolveTransport() (adapter.HTTPTransport, error) {
	httpClientManager := service.FromContext[adapter.HTTPClientManager](s.ctx)
	if s.httpClientOptions != nil && !s.httpClientOptions.IsEmpty() {
		if s.downloadDetour != "" {
			return nil, E.New("http_client conflicts with deprecated download_detour field")
		}
		return httpClientManager.ResolveTransport(s.ctx, s.logger, *s.httpClientOptions)
	}
	if s.downloadDetour != "" {
		return httpClientManager.ResolveTransport(s.ctx, s.logger, option.HTTPClientOptions{
			DialerOptions: option.DialerOptions{
				Detour: s.downloadDetour,
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

func (s *ProviderRemote) updateOnce() {
	if err := s.fetch(s.ctx, false); err != nil {
		s.logger.Error("update outbound provider: ", err)
	}
}

func (s *ProviderRemote) fetch(ctx context.Context, isStart bool) error {
	if s.httpClient == nil {
		// StartContext installs the client. A fetch before that (a manual Update from an embedder,
		// or a start that failed before the transport was resolved) has nothing to send through,
		// and must say so instead of dereferencing nil.
		return E.New("provider is not started")
	}
	if s.updating.Swap(true) {
		return E.New("provider is updating")
	}
	defer s.updating.Store(false)
	s.fetchAccess.Lock()
	defer s.fetchAccess.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	// The build reservation is taken AFTER this provider's own fetch lock, so a provider that
	// is already mid-fetch cannot consume a reservation it will not use, and two DIFFERENT
	// providers contend on the shared gate. A refusal is a retryable deferral: the version
	// currently serving traffic is untouched, which is the whole point of charging the build
	// rather than the download.
	buildRelease, buildErr := buildgate.AcquireFromContext(ctx)
	if buildErr != nil {
		s.logger.Debug("outbound provider ", s.Tag(), " build deferred: ", buildErr)
		return buildErr
	}
	buildStarted := time.Now()
	defer func() {
		buildRelease()
		if gate := buildgate.FromContext(ctx); gate != nil {
			stats := gate.Stats()
			s.logger.Debug("outbound provider ", s.Tag(), " build finished in ", time.Since(buildStarted),
				" inflight=", stats.Inflight, " admitted=", stats.AdmittedBuilds, " deferred=", stats.Deferred)
		}
	}()
	// The report is created before anything is requested and recorded on EVERY exit path: a
	// path that did not classify itself is honestly "unknown" rather than silently committed.
	report := &ApplyReport{
		Tag:               s.Tag(),
		RequestedRevision: s.requestedRevision.Add(1),
		Status:            ApplyStatusUnknown,
	}
	defer func() { s.recordApply(report) }()
	s.logger.Debug("updating outbound provider ", s.Tag(), " from URL: ", s.logURL)
	req, err := http.NewRequest(http.MethodGet, s.url, nil)
	if err != nil {
		return E.New("create provider request: ", s.redact(err.Error()))
	}
	if s.lastEtag != "" {
		req.Header.Set("If-None-Match", s.lastEtag)
	}
	req.Header.Set("User-Agent", s.userAgent)
	if !isStart {
		defer s.httpClient.CloseIdleConnections()
	}
	resp, err := s.httpClient.Do(req.WithContext(ctx))
	if err != nil {
		// The transport error embeds the request URL, query string and all. It is redacted here
		// rather than at every log site, so a token cannot leak through a wrapped error.
		return E.New("download provider document: ", s.redact(err.Error()))
	}
	defer resp.Body.Close()
	infoStr := resp.Header.Get("subscription-userinfo")
	info, hasInfo := parseInfo(infoStr)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		s.infoMu.Lock()
		s.subscriptionInfo = info
		s.lastUpdated = time.Now()
		s.infoMu.Unlock()
		if s.cacheFile != nil {
			saveSub := s.cacheFile.LoadSubscription(s.Tag())
			if saveSub != nil {
				if s.path == "" && hasInfo {
					index := bytes.IndexByte(saveSub.Content, '\n')
					if index != -1 {
						saveSub.Content = append([]byte(infoStr+"\n"), saveSub.Content[index+1:]...)
					}
				}
				saveSub.LastUpdated = s.lastUpdated
				saveSub.URLHash = s.urlHash[:]
				if err := s.cacheFile.SaveSubscription(s.Tag(), saveSub); err != nil {
					s.logger.Error("save outbound provider cache file: ", err)
				}
			}
		}
		if s.path != "" && s.lastOutOpts != nil {
			// Rewriting the path cache refreshes its mtime, which a start without a cachefile record
			// uses as LastUpdated. It is only safe when this provider actually has a document: a
			// spurious 304 before any content was loaded would otherwise replace a valid cache file
			// with `{"outbounds":null}`.
			content, _ := json.Marshal(option.Options{
				Outbounds: s.lastOutOpts,
			})
			if err := s.saveCacheFile(hasInfo, info, content); err != nil {
				report.AncillaryError = s.redact(err.Error())
				return E.Cause(err, "save outbound provider cache file")
			}
		}
		report.Status = ApplyStatusUnchanged
		s.logger.Info("update outbound provider ", s.Tag(), ": not modified")
		return nil
	default:
		return E.New("unexpected status: ", resp.Status)
	}
	contentRaw, err := io.ReadAll(io.LimitReader(resp.Body, s.maxDocumentSize+1))
	if err != nil {
		return E.New("read provider document: ", s.redact(err.Error()))
	}
	if int64(len(contentRaw)) > s.maxDocumentSize {
		return E.New("provider document is larger than the ", s.maxDocumentSize, " byte limit")
	}
	report.Downloaded = true
	content, _ := parser.DecodeBase64URLSafe(string(contentRaw))
	if !hasInfo {
		firstLine, others := getFirstLine(content)
		if info, hasInfo = parseInfo(firstLine); hasInfo {
			infoStr = firstLine
			content, _ = parser.DecodeBase64URLSafe(others)
		}
	}
	nodeErr, groupErr := s.applyContent(content)
	after := s.publishedTags()
	report.RequestedTags = s.ResolvedTags(s.lastOutOpts)
	report.AppliedTags, report.PendingTags = diffTags(report.RequestedTags, after)
	report.RetainedTags = retainedTags(report.RequestedTags, after)
	report.GroupRejected = groupErr != nil
	// A retry re-reads the same document and its callbacks refuse it again; only a restart
	// rebuilds the groups from configuration. That is why this is not just another partial.
	report.RestartRequired = groupErr != nil
	report.Diagnostics = dedupeDiagnostics([]string{s.redact(errorText(nodeErr)), s.redact(errorText(groupErr))})
	switch {
	case nodeErr != nil || groupErr != nil:
		if len(report.AppliedTags) > 0 {
			report.Status = ApplyStatusPartial
		} else {
			report.Status = ApplyStatusUnknown
		}
	default:
		report.Status = ApplyStatusCommitted
	}
	if nodeErr != nil {
		return nodeErr
	}
	if groupErr != nil {
		return groupErr
	}
	// The nodes are published; snapshot the exact document a recovery would pin.
	s.snapshotPublishedNodes()
	// The ETag is evidence that the SERVER's document is in the cache. Advancing it before the
	// cache write succeeds would make the next conditional request answer 304 for a document the
	// cache never received, pinning the provider to stale nodes forever. When the cache write
	// fails, the tag stays behind so the next refresh downloads the body again.
	newEtag := resp.Header.Get("Etag")
	cacheSaved := true
	if s.path != "" || s.cacheFile != nil {
		content, _ := json.Marshal(option.Options{
			Outbounds: s.lastOutOpts,
		})
		if s.path != "" {
			if err = s.saveCacheFile(hasInfo, info, content); err != nil {
				report.AncillaryError = s.redact(err.Error())
				return E.Cause(err, "save outbound provider cache file")
			}
		} else if hasInfo {
			content = append([]byte(infoStr+"\n"), content...)
		}
		if s.cacheFile != nil {
			saveSub := &adapter.SavedBinary{
				LastUpdated: time.Now(),
				LastEtag:    newEtag,
				URLHash:     s.urlHash[:],
			}
			if s.path == "" {
				saveSub.Content = content
			}
			if err = s.cacheFile.SaveSubscription(s.Tag(), saveSub); err != nil {
				s.logger.Error("save outbound provider cache file: ", err)
				cacheSaved = false
			}
		}
	}
	s.infoMu.Lock()
	s.subscriptionInfo = info
	s.lastUpdated = time.Now()
	if cacheSaved {
		s.lastEtag = newEtag
	}
	s.infoMu.Unlock()
	if !cacheSaved {
		s.logger.Warn("outbound provider cache was not saved; the next refresh will download the document again")
	}
	s.logger.Info("updated outbound provider ", s.Tag())
	return nil
}

func (s *ProviderRemote) loadCacheFile() (bool, error) {
	var content []byte
	var lastUpdated time.Time
	var lastEtag string
	var saveSub *adapter.SavedBinary
	if s.cacheFile != nil {
		if saveSub = s.cacheFile.LoadSubscription(s.Tag()); saveSub != nil {
			if len(saveSub.URLHash) > 0 && !bytes.Equal(saveSub.URLHash, s.urlHash[:]) {
				s.logger.Info("cached outbound provider was downloaded from another URL, will refetch")
				return false, nil
			}
		}
	}
	if s.path != "" {
		exists, err := pathExists(s.ctx, s.path)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
		file, err := filemanager.Open(s.ctx, s.path)
		if err != nil {
			return false, err
		}
		content, err = io.ReadAll(file)
		if err != nil {
			file.Close()
			return false, err
		}
		fileInfo, err := file.Stat()
		closeErr := file.Close()
		if err != nil {
			return false, err
		}
		if closeErr != nil {
			return false, closeErr
		}
		if saveSub != nil {
			lastUpdated = saveSub.LastUpdated
			lastEtag = saveSub.LastEtag
		} else {
			lastUpdated = fileInfo.ModTime()
		}
	} else if saveSub != nil && len(saveSub.Content) > 0 {
		content = saveSub.Content
		lastUpdated = saveSub.LastUpdated
		lastEtag = saveSub.LastEtag
	} else {
		return false, nil
	}
	if err := s.loadFromContent(content); err != nil {
		return false, err
	}
	s.lastUpdated, s.lastEtag = lastUpdated, lastEtag
	return true, nil
}

func (s *ProviderRemote) loadInitialPath() error {
	contentRaw, err := filemanager.ReadFile(s.ctx, s.initialPath)
	if err != nil {
		return err
	}
	content := s.decodeContent(contentRaw)
	return s.updateProviderFromContent(content)
}

func (s *ProviderRemote) loadFromContent(contentRaw []byte) error {
	content := s.decodeContent(contentRaw)
	// The cached document MUST go through the same reader as a fresh download. Parsing it with the
	// native reader only would silently drop the format detection, the include/exclude filters and
	// the dialer override on every restart: the running provider would then differ from a provider
	// that had just downloaded the same bytes.
	return s.updateProviderFromContent(content)
}

func (s *ProviderRemote) decodeContent(contentRaw []byte) string {
	content, _ := parser.DecodeBase64URLSafe(string(contentRaw))
	firstLine, others := getFirstLine(content)
	if info, ok := parseInfo(firstLine); ok {
		s.infoMu.Lock()
		s.subscriptionInfo = info
		s.infoMu.Unlock()
		content, _ = parser.DecodeBase64URLSafe(others)
	}
	return content
}

func pathExists(ctx context.Context, path string) (bool, error) {
	info, err := filemanager.Stat(ctx, path)
	if err == nil {
		if info.IsDir() {
			return false, E.New("provider path is a directory: ", path)
		}
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (s *ProviderRemote) loopUpdate() {
	s.ticker.Stop()
	select {
	case <-s.ticker.C:
	default:
	}
	if remaining := time.Until(func() time.Time {
		s.infoMu.RLock()
		defer s.infoMu.RUnlock()
		return s.lastUpdated
	}().Add(s.updateInterval)); remaining > 0 {
		s.ticker.Reset(remaining)
	} else {
		s.updateOnce()
		s.ticker.Reset(s.updateInterval)
	}
	for {
		runtime.GC()
		select {
		case <-s.ctx.Done():
			return
		case <-s.ticker.C:
			s.updateOnce()
			s.ticker.Reset(s.updateInterval)
		}
	}
}

func (s *ProviderRemote) saveCacheFile(hasInfo bool, info adapter.SubscriptionInfo, contentRaw []byte) error {
	content := contentRaw
	if hasInfo {
		infoStr := fmt.Sprint(
			"# upload=", info.Upload,
			"; download=", info.Download,
			"; total=", info.Total,
			"; expire=", info.Expire,
			";")
		content = append([]byte(infoStr+"\n"), content...)
	}
	dir := filepath.Dir(s.path)
	err := filemanager.MkdirAll(s.ctx, dir, 0o755)
	if err != nil {
		return err
	}
	// Write-then-rename in the same directory: a crash between the two leaves the previous cache
	// intact instead of a half-written document that the next start would have to reject. The temp
	// name is fixed, so a leftover from an interrupted write is overwritten rather than accumulating.
	tempPath := s.path + ".tmp"
	err = filemanager.WriteFile(s.ctx, tempPath, content, 0o666)
	if err != nil {
		return err
	}
	err = filemanager.Rename(s.ctx, tempPath, s.path)
	if err != nil {
		_ = filemanager.Remove(s.ctx, tempPath)
		return err
	}
	return nil
}

func (s *ProviderRemote) updateProviderFromContent(content string) error {
	nodeErr, groupErr := s.applyContent(content)
	if nodeErr != nil {
		return nodeErr
	}
	return groupErr
}

// applyContent parses the document and applies it, returning the node round's failure and the
// group round's failure SEPARATELY: they mean different things to the report (a node failure
// leaves members pending, a group refusal leaves the published graph differ from the document).
func (s *ProviderRemote) applyContent(content string) (nodeErr error, groupErr error) {
	outboundOpts, err := parser.ParseSubscription(s.ctx, content, s.overrideDialer, s.Tag())
	if err != nil {
		return err, nil
	}
	outboundOpts = common.Filter(outboundOpts, func(it option.Outbound) bool {
		return (s.exclude == nil || !s.exclude.MatchString(it.Tag)) && (s.include == nil || s.include.MatchString(it.Tag))
	})
	updateErr := s.UpdateNodes(outboundOpts)
	// The callbacks may refuse the membership the update would create (a cycle in the published
	// graph). Reporting that is what keeps a refresh honest: the nodes are installed, but the
	// group kept its previous members and selection, so the document was NOT applied as written.
	groupErr = s.UpdateGroups()
	// Record the applied document even when a node failed to construct: the published set already
	// reflects this document (the failed tag keeps its previous object), and a later 304 must not
	// rewrite the cache from the PREVIOUS document.
	s.lastOutOpts = outboundOpts
	return updateErr, groupErr
}

// redactedURL keeps the address of a subscription URL and removes what makes it a credential: the
// userinfo, the query string and the fragment. Logs and errors use this form, never the raw URL.
func redactedURL(parsed *url.URL) string {
	if parsed == nil {
		return "<nil>"
	}
	redacted := *parsed
	redacted.User = nil
	redacted.RawQuery = ""
	redacted.Fragment = ""
	return redacted.String()
}

// redact removes the raw subscription URL from text that is about to be logged. Transport errors
// embed the request URL, so redacting at the error boundary is what keeps a token out of the log
// even when the message is produced by a package that has never seen this configuration.
func (s *ProviderRemote) redact(text string) string {
	if s.url == "" {
		return text
	}
	logURL := s.logURL
	if logURL == "" {
		if parsed, err := url.Parse(s.url); err == nil {
			logURL = redactedURL(parsed)
		} else {
			logURL = "<redacted provider url>"
		}
	}
	text = strings.ReplaceAll(text, s.url, logURL)
	if escaped := url.QueryEscape(s.url); escaped != s.url {
		text = strings.ReplaceAll(text, escaped, logURL)
	}
	return text
}

func getFirstLine(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 1 {
		return lines[0], ""
	}
	others := strings.Join(lines[1:], "\n")
	return lines[0], others
}

func parseInfo(infoStr string) (adapter.SubscriptionInfo, bool) {
	info := adapter.SubscriptionInfo{}
	if infoStr == "" {
		return info, false
	}
	reg := regexp.MustCompile(`(upload|download|total|expire)[\s\t]*=[\s\t]*(-?\d*);?`)
	matches := reg.FindAllStringSubmatch(infoStr, 4)
	if len(matches) == 0 {
		return info, false
	}
	for _, match := range matches {
		key, value := match[1], match[2]
		switch key {
		case "upload":
			info.Upload = parser.StringToType[int64](value)
		case "download":
			info.Download = parser.StringToType[int64](value)
		case "total":
			info.Total = parser.StringToType[int64](value)
		case "expire":
			info.Expire = parser.StringToType[int64](value)
		default:
			return info, false
		}
	}
	return info, true
}
