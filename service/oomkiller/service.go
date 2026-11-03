package oomkiller

import (
	"context"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/resourceguard"
	boxConstant "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.OOMKillerServiceOptions](registry, boxConstant.TypeOOMKiller, NewService)
}

type Service struct {
	boxService.Adapter
	ctx           context.Context
	logger        log.ContextLogger
	network       adapter.NetworkManager
	connections   adapter.ConnectionManager
	recorder      *Recorder
	timerConfig   timerConfig
	adaptiveTimer *adaptiveTimer
	pressure      atomic.Uint32
	// guard is the admission policy for expensive creations, or nil when this build has no
	// process budget to protect. See NewService for the only condition that installs one.
	guard *resourceguard.Guard
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.OOMKillerServiceOptions) (adapter.Service, error) {
	memoryLimit, mode := resolvePolicyMode(ctx, options)
	config, err := buildTimerConfig(options, memoryLimit, mode, options.KillerDisabled)
	if err != nil {
		return nil, err
	}
	s := &Service{
		Adapter:     boxService.NewAdapter(boxConstant.TypeOOMKiller, tag),
		ctx:         ctx,
		logger:      logger,
		network:     service.FromContext[adapter.NetworkManager](ctx),
		connections: service.FromContext[adapter.ConnectionManager](ctx),
		recorder:    service.FromContext[*Recorder](ctx),
		timerConfig: config,
	}
	service.MustRegister[*Service](ctx, s)
	// The admission guard is installed ONLY for a Network Extension with a process budget.
	//
	// That condition, and not "a memory limit was configured", is what makes this safe to
	// ship: a desktop or Android process has no 50 MiB ceiling to police, and imposing
	// admission there would trade throughput for a limit the platform does not enforce. The
	// guard is registered in the context so protocol code reads the POLICY OWNER's decision
	// instead of deciding for itself whether it is running inside a Network Extension.
	if mode == policyModeNetworkExtension {
		s.guard = resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), s.guardLevel)
		service.MustRegister[*resourceguard.Guard](ctx, s.guard)
	}
	return s, nil
}

// guardLevel maps the pressure this service already publishes onto the admission levels.
//
// It reads the SAME atomic the TUN stack consumes through MemoryPressure, so admission, the
// slab pools, and the reclaim path cannot hold three different opinions about how much
// pressure the process is under. The mapping is deliberately total: an unknown value is
// treated as normal rather than as pressure, because refusing traffic on an unrecognised
// state would turn a wiring mistake into an outage.
func (s *Service) guardLevel() resourceguard.Level {
	switch tun.MemoryPressure(s.pressure.Load()) {
	case tun.MemoryPressureCritical:
		return resourceguard.LevelCritical
	case tun.MemoryPressureWarning:
		return resourceguard.LevelArmed
	default:
		return resourceguard.LevelNormal
	}
}

// GuardStats reports the admission counters, or the zero value when this build has no guard.
//
// It exists so a pressure report can say not only "the process was at 47 MiB" but "the
// admission policy refused N creations and the peak in flight was M": the first is a symptom,
// the second is what the system did about it.
func (s *Service) GuardStats() resourceguard.GuardStats {
	if s.guard == nil {
		return resourceguard.GuardStats{}
	}
	return s.guard.Stats()
}

func MemoryPressure(ctx context.Context) func() tun.MemoryPressure {
	oomKiller := service.FromContext[*Service](ctx)
	if oomKiller == nil {
		return nil
	}
	return oomKiller.MemoryPressure
}

func (s *Service) MemoryPressure() tun.MemoryPressure {
	return tun.MemoryPressure(s.pressure.Load())
}

func (s *Service) startTimer() error {
	if !s.timerConfig.policyMode.hasTimerMode() {
		return E.New("memory pressure monitoring is not available on this platform without memory_limit")
	}
	s.adaptiveTimer = newAdaptiveTimer(s.logger, s.network, s.connections, service.FromContext[adapter.CacheFile](s.ctx), s.recorder, &s.pressure, s.timerConfig)
	var carriedState *timerState
	if s.recorder != nil {
		carriedState = s.recorder.instanceStarted(s.timerConfig, s.adaptiveTimer.limitThresholds)
	}
	s.adaptiveTimer.start(carriedState)
	return nil
}

func (s *Service) stopTimer() {
	var state timerState
	if s.adaptiveTimer != nil {
		state = s.adaptiveTimer.stop()
	}
	s.pressure.Store(uint32(tun.MemoryPressureNone))
	if s.recorder != nil {
		s.recorder.instanceStopped(state)
	}
}
