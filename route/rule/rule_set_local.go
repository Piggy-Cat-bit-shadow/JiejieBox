package rule

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sagernet/fswatch"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service/filemanager"

	"go4.org/netipx"
)

var _ adapter.RuleSet = (*LocalRuleSet)(nil)

type LocalRuleSet struct {
	ctx        context.Context
	logger     logger.Logger
	tag        string
	access     sync.RWMutex
	rules      []adapter.HeadlessRule
	metadata   adapter.RuleSetMetadata
	fileFormat string
	watcher    *fswatch.Watcher
	callbacks  list.List[adapter.RuleSetUpdateCallback]
	refs       atomic.Int32
	// derivedCache reuses the compiled form of this file across reloads on platforms that serve
	// rule-sets from a mapping. It is nil where the platform does not, and every use is guarded:
	// the cache can only make a load cheaper, never make it fail.
	derivedCache *derivedRuleSetCache
}

func NewLocalRuleSet(ctx context.Context, logger logger.Logger, tag string, options option.RuleSet) (*LocalRuleSet, error) {
	ruleSet := &LocalRuleSet{
		ctx:        ctx,
		logger:     logger,
		tag:        tag,
		fileFormat: options.Format,
	}
	if options.Type == C.RuleSetTypeInline {
		if len(options.InlineOptions.Rules) == 0 {
			return nil, E.New("empty inline rule-set")
		}
		err := ruleSet.reloadRules(options.InlineOptions.Rules)
		if err != nil {
			return nil, err
		}
	} else {
		filePath := filemanager.BasePath(ctx, strings.ReplaceAll(options.LocalOptions.Path, C.RuleSetTagPlaceholder, tag))
		filePath, _ = filepath.Abs(filePath)
		if mmapRuleSetPlatform {
			ruleSet.derivedCache = newDerivedRuleSetCache(ctx, logger)
		}
		err := ruleSet.reloadFile(filePath)
		if err != nil {
			return nil, err
		}
		watcher, err := fswatch.NewWatcher(fswatch.Options{
			Path: []string{filePath},
			Callback: func(path string) {
				uErr := ruleSet.reloadFile(path)
				if uErr != nil {
					logger.Error(E.Cause(uErr, "reload rule-set ", tag))
				}
			},
		})
		if err != nil {
			return nil, err
		}
		ruleSet.watcher = watcher
	}
	return ruleSet, nil
}

func (s *LocalRuleSet) Name() string {
	return s.tag
}

// currentRules returns the published rule slice, or nil once it has been released.
//
// Same contract as the remote rule-set's helper, and here the writer is the file watcher's goroutine:
// `reloadRules` publishes a new slice under `access` whenever the file changes, while `Cleanup` and
// `Close` release it. Reading the field directly from `Match` is a read/write pair against that
// goroutine, which is the defect the remote type was caught on; the two types share the shape, so
// they are fixed together rather than leaving one correct and one not.
func (s *LocalRuleSet) currentRules() []adapter.HeadlessRule {
	s.access.RLock()
	defer s.access.RUnlock()
	return s.rules
}

func (s *LocalRuleSet) String() string {
	return strings.Join(F.MapToString(s.currentRules()), " ")
}

func (s *LocalRuleSet) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	if s.watcher != nil {
		err := s.watcher.Start()
		if err != nil {
			s.logger.Error(E.Cause(err, "watch rule-set file"))
		}
	}
	return nil
}

func (s *LocalRuleSet) reloadFile(path string) error {
	var ruleSet option.PlainRuleSetCompat
	var err error
	// derived is the exact source identity when this platform can reuse a compiled artifact. It
	// stays nil elsewhere, so computing a digest is not even attempted.
	var derived *derivedSource
	switch s.fileFormat {
	case C.RuleSetFormatSource, "":
		// On a platform with the derived path the digest is taken from the FILE, not from a
		// materialised copy: reading a large source JSON into memory just to hash it would put the
		// whole document back on the warm path's peak, which is the cost this cache exists to
		// avoid. The document is read below only when the cache missed.
		var content []byte
		if s.derivedCache != nil {
			// The digest and the parse must read the SAME open file. Hashing one descriptor and
			// then re-opening the path lets a replacement between the two parse the new bytes and
			// publish them under the old digest, which would serve rules that do not belong to the
			// source. hashOpenFile rewinds; a hit closes the file without reading the body, and a
			// miss reads that same descriptor.
			sourceFile, openErr := filemanager.Open(s.ctx, path)
			if openErr != nil {
				return openErr
			}
			digest, size, hashErr := hashOpenFile(sourceFile)
			if hashErr != nil {
				_ = sourceFile.Close()
				return hashErr
			}
			derived = localDerivedSource(s.tag, path, derivedFormatName(s.fileFormat), digest, size)
			if mapped, hit := s.derivedCache.open(derived); hit {
				if reloadErr := s.reloadMappedRuleSet(mapped); reloadErr == nil {
					_ = sourceFile.Close()
					return nil
				} else {
					// Verified bytes this build still cannot build rules from: fall through to the
					// source, which is the authoritative path. The descriptor is still at offset 0
					// because hashing rewound it and nothing was read since.
					s.logger.Debug(E.Cause(reloadErr, "derived rule-set is unusable, re-parsing the source"))
				}
			}
			var readErr error
			content, readErr = io.ReadAll(sourceFile)
			_ = sourceFile.Close()
			if readErr != nil {
				return readErr
			}
		} else {
			var readErr error
			content, readErr = filemanager.ReadFile(s.ctx, path)
			if readErr != nil {
				return readErr
			}
		}
		ruleSet, err = json.UnmarshalExtended[option.PlainRuleSetCompat](content)
		if err != nil {
			return err
		}
	case C.RuleSetFormatBinary:
		setFile, err := filemanager.Open(s.ctx, path)
		if err != nil {
			return err
		}
		defer setFile.Close()
		if s.derivedCache != nil {
			// One sequential pass over the compressed source; the parser reads it again. It is the
			// price of knowing WHICH bytes a conversion belongs to, and it is skipped on platforms
			// without the derived path.
			digest, size, hashErr := hashOpenFile(setFile)
			if hashErr != nil {
				return hashErr
			}
			derived = localDerivedSource(s.tag, path, derivedFormatName(s.fileFormat), digest, size)
			if mapped, hit := s.derivedCache.open(derived); hit {
				if err = s.reloadMappedRuleSet(mapped); err == nil {
					return nil
				} else {
					s.logger.Debug(E.Cause(err, "derived rule-set is unusable, re-parsing the source"))
				}
			}
		}
		ruleSet, err = srs.Read(setFile, false)
		if err != nil {
			return err
		}
	default:
		return E.New("unknown rule-set format: ", s.fileFormat)
	}
	return s.reloadParsedRuleSet(derived, ruleSet)
}

// reloadMappedRuleSet publishes a rule-set whose matchers are backed by a persisted mapping. The
// slice swap inside reloadRules only happens after every rule and the metadata validate, so a
// failure here has published nothing and the caller can safely fall back to the source.
func (s *LocalRuleSet) reloadMappedRuleSet(mapped option.PlainRuleSetCompat) error {
	plainRuleSet, err := mapped.Upgrade()
	if err != nil {
		return E.Cause(err, "upgrade derived rule-set")
	}
	return s.reloadRules(plainRuleSet.Rules)
}

// reloadParsedRuleSet finishes a load that had to read the source: it converts the parsed options to
// the form this platform serves (mapping them, on platforms that do), and, on a cold miss,
// publishes the conversion as a derived artifact for the next load.
func (s *LocalRuleSet) reloadParsedRuleSet(derived *derivedSource, ruleSet option.PlainRuleSetCompat) error {
	mapped := ruleSet
	if s.derivedCache != nil {
		file, ok := writeRuleSetMmapFile(s.ctx, s.logger, s.tag, ruleSet)
		if ok {
			defer file.Close()
			// Publish before mapping: the store streams the very bytes the mapping will use, so a
			// cold load writes the conversion twice (temp + store copy) instead of three times.
			s.derivedCache.publish(derived, ruleSet.Version, file)
			mapped = mapRuleSetMmapFile(s.logger, s.tag, file, ruleSet)
		}
	} else {
		mapped = mmapRuleSet(s.ctx, s.logger, s.tag, ruleSet)
	}
	plainRuleSet, err := mapped.Upgrade()
	if err != nil {
		return err
	}
	return s.reloadRules(plainRuleSet.Rules)
}

func (s *LocalRuleSet) reloadRules(headlessRules []option.HeadlessRule) error {
	rules := make([]adapter.HeadlessRule, len(headlessRules))
	var err error
	for i, ruleOptions := range headlessRules {
		rules[i], err = NewHeadlessRule(s.ctx, ruleOptions)
		if err != nil {
			return E.Cause(err, "parse rule_set.rules.[", i, "]")
		}
	}
	metadata := buildRuleSetMetadata(headlessRules)
	err = validateRuleSetMetadataUpdate(s.ctx, s.tag, metadata)
	if err != nil {
		return err
	}
	s.access.Lock()
	s.rules = rules
	s.metadata = metadata
	callbacks := s.callbacks.Array()
	s.access.Unlock()
	for _, callback := range callbacks {
		callback(s)
	}
	return nil
}

func (s *LocalRuleSet) Metadata() adapter.RuleSetMetadata {
	s.access.RLock()
	defer s.access.RUnlock()
	return s.metadata
}

func (s *LocalRuleSet) ExtractIPSet() []*netipx.IPSet {
	s.access.RLock()
	defer s.access.RUnlock()
	return common.FlatMap(s.rules, extractIPSetFromRule)
}

func (s *LocalRuleSet) IncRef() {
	s.refs.Add(1)
}

func (s *LocalRuleSet) DecRef() {
	if s.refs.Add(-1) < 0 {
		panic("rule-set: negative refs")
	}
}

func (s *LocalRuleSet) Cleanup() {
	if s.refs.Load() == 0 {
		s.access.Lock()
		s.rules = nil
		s.access.Unlock()
	}
}

func (s *LocalRuleSet) RegisterCallback(callback adapter.RuleSetUpdateCallback) *list.Element[adapter.RuleSetUpdateCallback] {
	s.access.Lock()
	defer s.access.Unlock()
	return s.callbacks.PushBack(callback)
}

func (s *LocalRuleSet) UnregisterCallback(element *list.Element[adapter.RuleSetUpdateCallback]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.callbacks.Remove(element)
}

func (s *LocalRuleSet) Close() error {
	s.access.Lock()
	s.rules = nil
	s.access.Unlock()
	return common.Close(common.PtrOrNil(s.watcher))
}

func (s *LocalRuleSet) Match(metadata *adapter.InboundContext) bool {
	return matchAnyHeadlessRule(s.currentRules(), metadata)
}

func (s *LocalRuleSet) mergeableRule() *DefaultHeadlessRule {
	return mergeableRuleIn(s.currentRules())
}
