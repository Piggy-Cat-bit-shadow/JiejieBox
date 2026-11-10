package daemon

import (
	"bytes"
	"context"
	"net/netip"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/clashmode"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/experimental/locale"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

type Instance struct {
	ctx      context.Context
	cancel   context.CancelFunc
	instance *box.Box
	// options is the document this Box was built from, and revision identifies the profile it was
	// parsed out of. Together they are what a change plan compares against: the same document a
	// start would build, not a second reading of the profile text.
	options  option.Options
	revision string
	// overrideOptions are the run-only overrides this Box was actually built with (the platform's
	// TUN patch and package selection), or nil when it was built without any.
	//
	// They are kept because they are part of the document above: `parseEffectiveOptions` has
	// ALREADY merged them into `options`, so a plan that parses the candidate without them compares
	// two documents that differ by the override - a difference the apply did not make and would not
	// reproduce. The default input for a preview is therefore the overrides the running document
	// was built from, not `nil`.
	overrideOptions       OverrideOptions
	hasOverrides          bool
	connectionManager     adapter.ConnectionManager
	clashMode             *clashmode.Manager
	trafficManager        *trafficcontrol.Manager
	cacheFile             adapter.CacheFile
	pauseManager          pause.Manager
	pauseCallback         *list.Element[pause.Callback]
	urlTestHistoryStorage *urltest.HistoryStorage
	outboundManager       adapter.OutboundManager
	endpointManager       adapter.EndpointManager
	// providerManager is the box's provider set. It is a narrow interface so a manual refresh
	// can be tested without a live box, and so this holds only what the refresh needs.
	providerManager providerSource
	// artifactRefs is what this Box published, by artifact key. Persisted with the configuration
	// so a recovery can pin the exact versions it ran.
	artifactRefs *artifact.ReferenceSet
	logFactory   log.Factory
}

func (s *StartedService) CheckConfig(ctx context.Context, configContent string) error {
	selectedLocale := locale.FromContext(ctx)
	ctx, _ = locale.ContextWithLocale(s.ctx, selectedLocale.Locale)
	ctx = service.ExtendContext(ctx)
	options, err := parseConfig(ctx, configContent)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: options,
	})
	if err == nil {
		instance.Close()
	}
	return err
}

func (s *StartedService) FormatConfig(ctx context.Context, configContent string) (string, error) {
	selectedLocale := locale.FromContext(ctx)
	ctx, _ = locale.ContextWithLocale(s.ctx, selectedLocale.Locale)
	options, err := parseConfig(ctx, configContent)
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(options)
	if err != nil {
		return "", err
	}
	return buffer.String(), nil
}

type OverrideOptions struct {
	AutoRedirect   bool
	IncludePackage []string
	ExcludePackage []string
	// LoopbackAddress is what the platform asks the effective TUN to answer for locally.
	//
	// It exists because the address is not in the user's configuration and must never be written
	// into it: the Apple client's "Local Signing Loopback" switch travels down the start options,
	// and this is where it becomes configuration for this run only. Every other platform leaves
	// this nil, which makes the merge below a no-op - same code path, no new behaviour.
	LoopbackAddress []netip.Addr
}

// applyTUNOverrides applies the platform's overrides to the ONE TUN inbound this service drives.
//
// # Why the first TUN, and only the first
//
// These overrides describe how the *packet tunnel this process runs inside* should behave, and
// there is exactly one of those. A configuration with no TUN is legal (a plain proxy) and gets
// nothing; a configuration with several is a shape the Apple client does not produce, and the
// established behaviour of these overrides is to patch the first. Silently editing a second TUN
// would be worse than leaving it alone: it would change an inbound the platform never asked
// about.
//
// # What the loopback merge is careful about
//
// The user may already list loopback addresses in their own configuration. Those are preserved
// verbatim, in order, and are never removed here - this function only appends what the platform
// asked for and the configuration does not already contain. Comparisons use the same
// normalisation the core uses when it matches a packet destination (IPv4-mapped IPv6 unmapped,
// zone stripped), so "::ffff:10.7.0.1" and "10.7.0.1" are one address and a duplicate entry
// cannot exist.
func applyTUNOverrides(options *option.Options, overrideOptions *OverrideOptions) {
	for _, inbound := range options.Inbounds {
		tunInboundOptions, isTUN := inbound.Options.(*option.TunInboundOptions)
		if !isTUN {
			continue
		}
		tunInboundOptions.AutoRedirect = overrideOptions.AutoRedirect && tunInboundOptions.AutoRoute
		tunInboundOptions.IncludePackage = append(tunInboundOptions.IncludePackage, overrideOptions.IncludePackage...)
		tunInboundOptions.ExcludePackage = append(tunInboundOptions.ExcludePackage, overrideOptions.ExcludePackage...)
		tunInboundOptions.LoopbackAddress = mergeLoopbackAddresses(tunInboundOptions.LoopbackAddress, overrideOptions.LoopbackAddress)
		break
	}
}

// mergeLoopbackAddresses returns the configuration's own addresses followed by the ones the
// platform added, normalised and de-duplicated, or the configuration unchanged when the platform
// added nothing.
//
// The early return on an empty addition is deliberate: with the feature off, the slice the user's
// configuration produced must come out byte-identical, not merely equivalent.
func mergeLoopbackAddresses(existing badoption.Listable[netip.Addr], addition []netip.Addr) badoption.Listable[netip.Addr] {
	if len(addition) == 0 {
		return existing
	}
	merged := make(badoption.Listable[netip.Addr], 0, len(existing)+len(addition))
	seen := make(map[netip.Addr]struct{}, len(existing)+len(addition))
	appendAddress := func(address netip.Addr) {
		address = address.Unmap().WithZone("")
		if !address.IsValid() {
			return
		}
		if _, loaded := seen[address]; loaded {
			return
		}
		seen[address] = struct{}{}
		merged = append(merged, address)
	}
	for _, address := range existing {
		appendAddress(address)
	}
	for _, address := range addition {
		appendAddress(address)
	}
	return merged
}

// parseEffectiveOptions turns a profile into the document the Box is actually built from: the
// profile as parsed, plus the run-only overrides (the platform's TUN patch and the OOM killer
// service). ConfigRecoveryDirectory aside, it is the ONE place a profile becomes options, so the
// change plan compares exactly the documents a start would build rather than a second reading of
// the same text.
func (s *StartedService) parseEffectiveOptions(ctx context.Context, profileContent string, overrideOptions *OverrideOptions) (option.Options, error) {
	selectedLocale := locale.FromContext(ctx)
	ctx, _ = locale.ContextWithLocale(s.ctx, selectedLocale.Locale)
	ctx = service.ExtendContext(ctx)
	service.MustRegister[deprecated.Manager](ctx, new(deprecatedManager))
	options, err := parseConfig(ctx, profileContent)
	if err != nil {
		return option.Options{}, err
	}
	if overrideOptions != nil {
		applyTUNOverrides(&options, overrideOptions)
	}
	if s.oomKillerEnabled {
		if !common.Any(options.Services, func(it option.Service) bool {
			return it.Type == C.TypeOOMKiller
		}) {
			oomOptions := &option.OOMKillerServiceOptions{
				KillerDisabled:      s.oomKillerDisabled,
				MemoryLimitOverride: s.oomMemoryLimit,
			}
			options.Services = append(options.Services, option.Service{
				Type:    C.TypeOOMKiller,
				Options: oomOptions,
			})
		}
	}
	return options, nil
}

func (s *StartedService) newInstance(ctx context.Context, options option.Options, revision string, reloading bool, pinnedReferences map[string]string, retainedReferences map[string]string, overrideOptions *OverrideOptions) (*Instance, error) {
	selectedLocale := locale.FromContext(ctx)
	ctx, _ = locale.ContextWithLocale(s.ctx, selectedLocale.Locale)
	ctx = service.ExtendContext(ctx)
	service.MustRegister[deprecated.Manager](ctx, new(deprecatedManager))
	ctx, cancel := context.WithCancel(ctx)
	artifactReferences := artifact.NewReferenceSet(pinnedReferences, retainedReferences)
	service.MustRegister[*artifact.ReferenceSet](ctx, artifactReferences)
	urlTestHistoryStorage := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, urlTestHistoryStorage)
	// ONE measurement budget for this Box.
	//
	// A separate coordinator per Box is what keeps a temporary configuration-check Box from
	// competing with the running one for slots.
	ctx = urltest.ContextWithCoordinator(ctx, urltest.NewCoordinator(C.URLTestConcurrencyLimit))
	i := &Instance{
		ctx:                   ctx,
		cancel:                cancel,
		options:               options,
		revision:              revision,
		urlTestHistoryStorage: urlTestHistoryStorage,
		artifactRefs:          artifactReferences,
	}
	if overrideOptions != nil {
		i.overrideOptions = *overrideOptions
		i.hasOverrides = true
	}
	boxInstance, err := box.New(box.Options{
		Context:           ctx,
		Options:           options,
		PlatformLogWriter: s,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	i.instance = boxInstance
	i.connectionManager = service.FromContext[adapter.ConnectionManager](ctx)
	i.clashMode = service.PtrFromContext[clashmode.Manager](ctx)
	i.trafficManager = service.PtrFromContext[trafficcontrol.Manager](ctx)
	i.pauseManager = service.FromContext[pause.Manager](ctx)
	i.registerPowerReport(ctx, reloading)
	i.cacheFile = service.FromContext[adapter.CacheFile](ctx)
	i.outboundManager = service.FromContext[adapter.OutboundManager](ctx)
	i.endpointManager = service.FromContext[adapter.EndpointManager](ctx)
	i.providerManager = service.FromContext[adapter.ProviderManager](ctx)
	i.logFactory = boxInstance.LogFactory()
	log.SetStdLogger(boxInstance.LogFactory().Logger())
	return i, nil
}

func attachInstance(ctx context.Context) *Instance {
	return &Instance{
		ctx:                   ctx,
		connectionManager:     service.FromContext[adapter.ConnectionManager](ctx),
		clashMode:             service.PtrFromContext[clashmode.Manager](ctx),
		trafficManager:        service.PtrFromContext[trafficcontrol.Manager](ctx),
		pauseManager:          service.FromContext[pause.Manager](ctx),
		cacheFile:             service.FromContext[adapter.CacheFile](ctx),
		urlTestHistoryStorage: service.PtrFromContext[urltest.HistoryStorage](ctx),
		outboundManager:       service.FromContext[adapter.OutboundManager](ctx),
		endpointManager:       service.FromContext[adapter.EndpointManager](ctx),
		providerManager:       service.FromContext[adapter.ProviderManager](ctx),
		logFactory:            service.FromContext[log.Factory](ctx),
	}
}

func (i *Instance) Start() error {
	return i.instance.Start()
}

func (i *Instance) Close() error {
	i.cancel()
	if i.pauseCallback != nil {
		i.pauseManager.UnregisterCallback(i.pauseCallback)
		i.pauseCallback = nil
	}
	i.urlTestHistoryStorage.Close()
	return i.instance.Close()
}

func (i *Instance) registerPowerReport(ctx context.Context, reloading bool) {
	powerManager := service.FromContext[*powerreport.Manager](ctx)
	if powerManager == nil {
		return
	}
	recorder := powerManager.Recorder()
	if recorder != nil {
		recorder.RecordServiceStart(i.instance.CreatedAt(), reloading)
	}
	if i.pauseManager == nil {
		return
	}
	i.pauseCallback = i.pauseManager.RegisterCallback(func(event int) {
		recorder := powerManager.Recorder()
		if recorder != nil {
			recorder.RecordPauseEvent(event)
		}
	})
}

func (i *Instance) Box() *box.Box {
	return i.instance
}

func (i *Instance) PauseManager() pause.Manager {
	return i.pauseManager
}

func (i *Instance) TrafficManager() *trafficcontrol.Manager {
	return i.trafficManager
}

func parseConfig(ctx context.Context, configContent string) (option.Options, error) {
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(configContent))
	if err != nil {
		return option.Options{}, E.Cause(err, "decode config")
	}
	return options, nil
}
