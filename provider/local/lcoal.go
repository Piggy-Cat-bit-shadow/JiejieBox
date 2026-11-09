package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/fswatch"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/artifact"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

func RegisterProviderLocal(registry *provider.Registry) {
	provider.Register[option.ProviderLocalOptions](registry, C.ProviderTypeLocal, NewProviderLocal)
}

func RegisterProviderInline(registry *provider.Registry) {
	provider.Register[option.ProviderInlineOptions](registry, C.ProviderTypeInline, NewProviderInline)
}

var _ adapter.Provider = (*ProviderLocal)(nil)

// ProviderLocal serves a node document from the local filesystem: either a file that is watched for
// changes (local) or the configuration itself (inline).
type ProviderLocal struct {
	provider.Adapter
	ctx         context.Context
	logger      log.ContextLogger
	provider    adapter.ProviderManager
	path        string
	mu          sync.Mutex
	lastOutOpts []option.Outbound
	lastUpdated time.Time
	watcher     *fswatch.Watcher
	closed      bool
	// The exact file bytes the published nodes came from, so a recovery can pin them instead of
	// reading whatever the path contains now.
	artifactStore *artifact.Store
	artifactKey   string
	artifactLease *artifact.Version

	overrideDialer *option.OverrideDialerOptions
}

func NewProviderInline(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderInlineOptions) (adapter.Provider, error) {
	var (
		outbound = service.FromContext[adapter.OutboundManager](ctx)
		logger   = logFactory.NewLogger(F.ToString("provider/inline", "[", tag, "]"))
	)
	provider := &ProviderLocal{
		Adapter: provider.NewAdapter(ctx, router, outbound, logFactory, logger, tag, C.ProviderTypeInline, options.HealthCheck),
		ctx:     ctx,
		logger:  logger,
	}
	provider.RewriteDetourForProvider(options.Outbounds)
	if err := provider.UpdateNodes(options.Outbounds); err != nil {
		return nil, err
	}
	return provider, nil
}

func NewProviderLocal(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderLocalOptions) (adapter.Provider, error) {
	if options.Path == "" {
		return nil, E.New("provider path is required")
	}
	var (
		outbound = service.FromContext[adapter.OutboundManager](ctx)
		logger   = logFactory.NewLogger(F.ToString("provider/local", "[", tag, "]"))
	)
	provider := &ProviderLocal{
		Adapter:  provider.NewAdapter(ctx, router, outbound, logFactory, logger, tag, C.ProviderTypeLocal, options.HealthCheck),
		ctx:      ctx,
		logger:   logger,
		provider: service.FromContext[adapter.ProviderManager](ctx),

		overrideDialer: options.OverrideDialer,
	}
	filePath := filemanager.BasePath(ctx, options.Path)
	provider.path, _ = filepath.Abs(filePath)
	// Watch the RESOLVED path, not the configured string. fsnotify reports event names in the host's
	// native form (a backslash path on Windows) and fswatch matches them against the configured
	// value byte for byte. A configuration written with forward slashes - the ordinary form in JSON -
	// would otherwise never fire: the watch would be syntactically installed and permanently silent.
	if info, statErr := filemanager.Stat(ctx, provider.path); statErr == nil && info.IsDir() {
		return nil, E.New("provider path is a directory: ", provider.path)
	} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, E.Cause(statErr, "check provider path")
	}
	watcher, err := fswatch.NewWatcher(fswatch.Options{
		Path: []string{provider.path},
		Callback: func(path string) {
			uErr := provider.reloadFile(path)
			if uErr != nil {
				logger.Error(E.Cause(uErr, "reload provider ", tag))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	provider.watcher = watcher
	return provider, nil
}

func (s *ProviderLocal) referenceSet() *artifact.ReferenceSet {
	return service.FromContext[*artifact.ReferenceSet](s.ctx)
}

func (s *ProviderLocal) openArtifactStore() {
	// A context without a file base (a unit test, an embedder that keeps no state) has nowhere
	// persistent to put snapshots; do not create one in the process working directory.
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
	s.artifactKey = localProviderKey(s.Tag(), s.path)
}

func localProviderKey(tag string, path string) string {
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
	digest := sha256.Sum256([]byte(filepath.Clean(path)))
	return name + "-" + hex.EncodeToString(digest[:8])
}

func (s *ProviderLocal) localManifest() artifact.Manifest {
	return artifact.NewManifest("provider/local", C.Version, "tag="+s.Tag(), "path="+s.path)
}

func (s *ProviderLocal) adoptArtifact(version *artifact.Version) {
	previous := s.artifactLease
	s.artifactLease = version
	if previous != nil && previous != version {
		previous.Release()
	}
}

// snapshotContent stores the file bytes the published nodes came from.
func (s *ProviderLocal) snapshotContent(content []byte, updated time.Time) {
	if s.artifactStore == nil {
		return
	}
	manifest := s.localManifest()
	if !updated.IsZero() {
		manifest.Created = updated.UnixNano()
	}
	version, err := s.artifactStore.Commit(s.artifactKey, manifest, strings.NewReader(string(content)))
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

// restorePinned loads the exact file bytes a recovered configuration names. A missing pinned
// snapshot is repaired from the file only when the file still has exactly those bytes; otherwise
// the start fails instead of silently serving the changed file.
func (s *ProviderLocal) restorePinned() (bool, error) {
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
		if s.path != "" {
			content, readErr := os.ReadFile(s.path)
			if readErr == nil {
				sum := sha256.Sum256(content)
				if hex.EncodeToString(sum[:]) == pinnedID {
					_ = s.artifactStore.Discard(s.artifactKey, pinnedID)
					manifest := s.localManifest()
					repaired, commitErr := s.artifactStore.Commit(s.artifactKey, manifest, strings.NewReader(string(content)))
					if commitErr == nil {
						version = repaired
					}
				}
			}
		}
		if version == nil {
			return false, E.Cause(err, "pinned provider file version ", pinnedID, " is not loadable")
		}
	}
	content, err := os.ReadFile(version.Path())
	if err != nil {
		version.Release()
		return false, err
	}
	if err = s.applyContent(content, time.Unix(0, version.Manifest().Created)); err != nil {
		version.Release()
		return false, err
	}
	s.adoptArtifact(version)
	references.Record(s.artifactKey, version.ID())
	return true, nil
}

// applyContent parses and publishes one provider document, and records the update time only after
// the nodes and groups accepted it.
func (s *ProviderLocal) applyContent(content []byte, updated time.Time) error {
	outboundOpts, err := parser.ParseSubscription(s.ctx, string(content), s.overrideDialer, s.Tag())
	if err != nil {
		return err
	}
	updateErr := s.UpdateNodes(outboundOpts)
	groupErr := s.UpdateGroups()
	if updateErr != nil {
		return updateErr
	}
	if groupErr != nil {
		return groupErr
	}
	s.lastUpdated = updated
	s.lastOutOpts = outboundOpts
	return nil
}

func (s *ProviderLocal) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	if err := s.Adapter.Start(); err != nil {
		return err
	}
	s.openArtifactStore()
	restored, err := s.restorePinned()
	if err != nil {
		return err
	}
	if !restored && s.path != "" {
		err := s.reloadFile(s.path)
		if err != nil {
			return err
		}
	}
	if s.path != "" && s.watcher != nil {
		if err := s.watcher.Start(); err != nil {
			s.logger.Error(E.Cause(err, "watch provider file"))
		}
	}
	// A membership the graph validator refused is a START failure for a synchronous provider: the
	// same final graph written statically is refused at start, and letting it through here would
	// make the verdict depend on where the node was declared.
	return s.UpdateGroups()
}

func (s *ProviderLocal) UpdatedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUpdated
}

func (s *ProviderLocal) reloadFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return context.Canceled
	}
	file, err := filemanager.Open(s.ctx, path)
	if err != nil {
		return err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		file.Close()
		return err
	}
	fileInfo, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	outboundOpts, err := parser.ParseSubscription(s.ctx, string(content), s.overrideDialer, s.Tag())
	if err != nil {
		return err
	}
	updateErr := s.UpdateNodes(outboundOpts)
	groupErr := s.UpdateGroups()
	if updateErr != nil {
		return updateErr
	}
	if groupErr != nil {
		// The document was parsed and its nodes were installed, but a group refused the membership
		// the update would have created. The group kept its previous members and selection; the
		// caller still needs the refusal, because it is the reason the update did not take effect.
		return groupErr
	}
	s.lastUpdated = fileInfo.ModTime()
	s.lastOutOpts = outboundOpts
	s.snapshotContent(content, fileInfo.ModTime())
	return nil
}

func (s *ProviderLocal) Close() error {
	err := common.Close(common.PtrOrNil(s.watcher))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.artifactLease != nil {
		s.artifactLease.Release()
		s.artifactLease = nil
	}
	return E.Errors(err, s.Adapter.Close())
}
