package libbox

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/service/oomkiller"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type CommandServer struct {
	*daemon.StartedService
	ctx               context.Context
	managedService    *daemon.ManagedService
	handler           CommandServerHandler
	platformInterface PlatformInterface
	platformWrapper   *platformInterfaceWrapper
	powerManager      *powerreport.Manager
	oomRecorder       *oomkiller.Recorder
	grpcServer        *grpc.Server
	listener          net.Listener
	// The device level, remembered across a service reload so a fact delivered while there is no Box
	// is not lost. See rememberDeviceLevel for the window this closes and why only the level, and not
	// the edges, is kept.
	axisAccess        sync.Mutex
	deviceAxisPaused  bool
	deviceAxisTouched bool
}

type CommandServerHandler interface {
	ServiceStop() error
	ServiceReload() error
	GetSystemProxyStatus() (*SystemProxyStatus, error)
	SetSystemProxyEnabled(enabled bool) error
	TriggerNativeCrash() error
	WriteDebugMessage(message string)
	ConnectSSHAgent() (int32, error)
}

func NewCommandServer(handler CommandServerHandler, platformInterface PlatformInterface) (*CommandServer, error) {
	ctx := baseContext(platformInterface)
	powerManager := powerreport.NewManager()
	service.MustRegister[*powerreport.Manager](ctx, powerManager)
	platformWrapper := &platformInterfaceWrapper{
		iif:          platformInterface,
		useProcFS:    platformInterface.UseProcFS(),
		powerManager: powerManager,
	}
	service.MustRegister[adapter.PlatformInterface](ctx, platformWrapper)
	server := &CommandServer{
		ctx:               ctx,
		handler:           handler,
		platformInterface: platformInterface,
		platformWrapper:   platformWrapper,
		powerManager:      powerManager,
	}
	server.StartedService = daemon.NewStartedService(daemon.ServiceOptions{
		Context: ctx,
		// Platform:         platformWrapper,
		Handler:           (*platformHandler)(server),
		Debug:             sDebug,
		LogMaxLines:       sLogMaxLines,
		OOMKillerEnabled:  sOOMKillerEnabled,
		OOMKillerDisabled: sOOMKillerDisabled,
		OOMMemoryLimit:    uint64(sOOMMemoryLimit),
		// WorkingDirectory: sWorkingPath,
		// TempDirectory:    sTempPath,
		// UserID:           sUserID,
		// GroupID:          sGroupID,
		// SystemProxyEnabled: false,
	})
	oomRecorder := oomkiller.NewRecorder(OOMRecorderOptions(server.StartedService))
	service.MustRegister[*oomkiller.Recorder](ctx, oomRecorder)
	oomRecorder.Start()
	server.oomRecorder = oomRecorder
	server.managedService = daemon.NewManagedService(daemon.ManagedServiceOptions{
		Handler:     (*platformHandler)(server),
		Debug:       sDebug,
		OOMRecorder: oomRecorder,
	})
	if sPowerReportEnabled {
		err := powerManager.Start(PowerReportOptions(server.StartedService))
		if err != nil {
			log.StdLogger().Error(E.Cause(err, "start power report recorder"))
		}
	}
	return server, nil
}

func unaryAuthInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if sCommandServerSecret == "" {
		return handler(ctx, req)
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("x-command-secret")
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authentication secret")
	}
	if values[0] != sCommandServerSecret {
		return nil, status.Error(codes.Unauthenticated, "invalid authentication secret")
	}
	return handler(ctx, req)
}

func streamAuthInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if sCommandServerSecret == "" {
		return handler(srv, ss)
	}
	md, ok := metadata.FromIncomingContext(ss.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("x-command-secret")
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "missing authentication secret")
	}
	if values[0] != sCommandServerSecret {
		return status.Error(codes.Unauthenticated, "invalid authentication secret")
	}
	return handler(srv, ss)
}

func (s *CommandServer) Start() error {
	var (
		listener net.Listener
		err      error
	)
	if sCommandServerListenPort == 0 {
		sockPath := filepath.Join(sBasePath, "command.sock")
		os.Remove(sockPath)
		for range 30 {
			listener, err = net.ListenUnix("unix", &net.UnixAddr{
				Name: sockPath,
				Net:  "unix",
			})
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EROFS) {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			return E.Cause(err, "listen command server")
		}
		if sUserID != os.Getuid() {
			err = os.Chown(sockPath, sUserID, sGroupID)
			if err != nil {
				listener.Close()
				os.Remove(sockPath)
				return E.Cause(err, "chown")
			}
		}
	} else {
		listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sCommandServerListenPort))))
		if err != nil {
			return E.Cause(err, "listen command server")
		}
	}
	s.listener = listener
	serverOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryAuthInterceptor, daemon.UnaryLocaleInterceptor),
		grpc.ChainStreamInterceptor(streamAuthInterceptor, daemon.StreamLocaleInterceptor),
	}
	s.grpcServer = grpc.NewServer(serverOptions...)
	daemon.RegisterStartedServiceServer(s.grpcServer, s.StartedService)
	daemon.RegisterManagedServiceServer(s.grpcServer, s.managedService)
	healthServer := health.NewServer()
	healthServer.SetServingStatus(daemon.StartedService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(daemon.ManagedService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(s.grpcServer, healthServer)
	go s.grpcServer.Serve(listener)
	return nil
}

func (s *CommandServer) Close() {
	if s.grpcServer != nil {
		s.grpcServer.Stop()
	}
	common.Close(s.listener)
	s.StartedService.Close()
	s.oomRecorder.Close()
	s.powerManager.Close()
}

type OverrideOptions struct {
	AutoRedirect   bool
	IncludePackage StringIterator
	ExcludePackage StringIterator
}

func (s *CommandServer) StartOrReloadService(configContent string, options *OverrideOptions) error {
	saveConfigSnapshot(configContent)
	if s.powerManager.Recorder() != nil {
		copyConfigSnapshot(filepath.Join(sWorkingPath, powerreport.DraftDirectoryName))
	}
	err := s.StartedService.StartOrReloadService(s.ctx, configContent, &daemon.OverrideOptions{
		AutoRedirect:   options.AutoRedirect,
		IncludePackage: iteratorToArray(options.IncludePackage),
		ExcludePackage: iteratorToArray(options.ExcludePackage),
	})
	if err != nil {
		return E.Cause(err, "start or reload service")
	}
	// The window is closed again: whatever the platform reported while there was no Box is applied to
	// the Box that now exists. See rememberDeviceLevel.
	s.restoreDeviceLevel()
	return nil
}

func (s *CommandServer) CloseService() error {
	return s.StartedService.CloseService()
}

func (s *CommandServer) WriteMessage(level int32, message string) {
	s.StartedService.WriteMessage(log.Level(level), message)
}

func (s *CommandServer) SetError(message string) {
	s.StartedService.SetError(E.New(message))
}

func (s *CommandServer) NeedWIFIState() bool {
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return false
	}
	return instance.Box().Network().NeedWIFIState()
}

func (s *CommandServer) NeedFindProcess() bool {
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return false
	}
	return instance.Box().Router().NeedFindProcess()
}

// deviceAxisPaused mirrors the device LEVEL across a service reload.
//
// # The window this closes
//
// `StartedService.StartOrReloadService` sets `s.instance = nil` and leaves it nil for the whole of
// `newInstance` - the old Box is closed, a new one is built and started. Every entry point here
// resolves `Instance()` and returns early when it is nil, so a fact that arrives in that window is
// **dropped**: not queued, not remembered, not reported to anyone.
//
// That used to be masked. Each Box created its own pause manager, so a reload also reset the level to
// unpaused, and a dropped lock was accidentally consistent with the state the reload left behind.
// Since `daemon.NewStartedService` now registers one manager for the service's life - so that the
// level survives a reload at all - the two errors no longer cancel: a phone that was locked stays
// recorded as locked (correct), and an UNLOCK delivered during the reload window is dropped, so the
// level latches paused while the device is in use. The client cannot repair that either: its
// screen-state observer treats the lock source's unchanged `0` as a repeat and publishes nothing
// (`Library/Network/ScreenStateObserver.swift:243`), so nothing re-asserts the fact.
//
// # What is remembered, and why only the level
//
// Only the LEVEL boolean, not the sequence of facts. The edges are measurements - the governor
// classifies a sleep by its duration - and replaying an edge for a sleep that has already been
// measured would open a second measurement. The level is a plain fact about the device that must
// outlive the Box, and it is exactly what is lost.
//
// `deviceAxisTouched` is separate from `deviceAxisPaused` because "the device is not paused" and
// "nothing has ever said anything about this device" are different: a fresh process must not
// manufacture a wake from a zero value.
//
// Both fields are read and written under `axisAccess`, and they are only consulted on the
// iOS/Android device axis - the platforms whose level a reload would otherwise silently reset.
func (s *CommandServer) rememberDeviceLevel(paused bool) {
	s.axisAccess.Lock()
	s.deviceAxisPaused = paused
	s.deviceAxisTouched = true
	s.axisAccess.Unlock()
}

// restoreDeviceLevel re-applies the remembered level to a Box that has just started.
//
// Called after every `StartOrReloadService`, so the replacement Box is told the fact the platform
// already reported instead of starting optimistic.
//
// # Why the LEVEL and not `Box.DeviceSlept()`
//
// `DeviceSlept` publishes the sleep EDGE as well as the level, and the edge is a MEASUREMENT: the
// governor's `SleepStarted` stamps `pausedSince`, and the reuse policy classifies the sleep by how
// long it lasted (`SuspectAfter` 5s, `RetireAfter` 15s). Replaying the edge for a sleep the previous
// governor had already been measuring would re-stamp the start of that sleep at reload time, so a
// four-hour sleep would be re-measured as zero seconds and classified as "nothing happened" - losing
// exactly the pool retirement a long sleep is supposed to earn. The level is a plain fact that must
// outlive the Box; the edge is a verdict about the past, and the Box it belonged to already published
// it. Only the level moves here, in both directions.
//
// # Why there is a WAKE direction, and why the first version of this was wrong
//
// `pause.Manager.DevicePause` and `DeviceWake` are both level-guarded: the second call in the same
// direction is a silent no-op that emits nothing. That makes this function the only thing that can
// move the level after a reload, and it means the two directions are not symmetric by accident:
//
//   - remembered PAUSED, manager not paused -> enter the level. An unlock delivered during the
//     reload window moved the level, so re-entering it is correct.
//   - remembered NOT paused, manager paused -> **release** the level. This is the case the first
//     version missed. A lock arrives, the reload window opens (`StartedService` sets `s.instance =
//     nil` for the whole of `newInstance`), the user unlocks, the unlock is `rememberDeviceLevel(false)`
//     and then dropped by the nil-instance return below. Without this direction the level stays
//     entered while the phone is in the user's hand: `route/reference.go` mirrors "paused",
//     `protocol/group/urltest.go` refuses to measure, the on-demand tunnels stay suspended - and
//     nothing repairs it, because the client's screen-state observer treats the lock source's
//     unchanged value as a repeat (`Library/Network/ScreenStateObserver.swift`'s repeat rule) and
//     so re-publishes nothing. Measured before this fix: `axisPaused=false` recorded,
//     `managerPaused=true` after the reload.
//
// The governor of the replacement Box is NOT set here: `box.New` seeds it from the manager's level
// when it builds, which is where the level is actually known. This function only moves the level.
//
// # Why the remembered value is only a cache, and is corrected here
//
// The mirror is written where the level moves, but it is not the only writer of the level:
// `PlatformEvents` (the Android path) reaches `instance.PauseManager()` directly, and `WakeNow` records
// before it knows whether there is a Box to apply the fact to. So the mirror can disagree with the
// manager, and acting on it blindly would MEMORY the disagreement forward - a reload could release a
// pause the device is genuinely in, which is the same latch this function exists to close, pointing the
// other way.
//
// The manager is therefore the source of truth and the mirror is a cache: the level is moved only when
// the two disagree, and the mirror is then set from the manager rather than from what was remembered.
// That makes this function self-correcting, which is the property that matters - a stale mirror costs at
// most one reload's worth of wrongness, not a latch.
func (s *CommandServer) restoreDeviceLevel() {
	if !(C.IsAndroid || C.IsIos) || C.IsTvOS {
		return
	}
	s.axisAccess.Lock()
	paused, touched := s.deviceAxisPaused, s.deviceAxisTouched
	s.axisAccess.Unlock()
	if !touched {
		return
	}
	instance := s.StartedService.Instance()
	if instance == nil {
		return
	}
	manager := instance.PauseManager()
	if manager == nil {
		return
	}
	if manager.IsDevicePaused() != paused {
		if paused {
			manager.DevicePause()
		} else {
			manager.DeviceWake()
		}
	}
	// Read back rather than copy: the manager is the authority even when it was another writer that
	// moved it, and this is the one place the two are reconciled.
	s.rememberDeviceLevel(manager.IsDevicePaused())
}

// Pause enters the device pause on the platform whose sleep signal means the device is going to
// sleep.
//
// It publishes the sleep EDGE and then the LEVEL, and it is the Apple and Android `sleep()` override.
// The edge is what matters on Apple, where nothing but an unlock lifts the level: a governor driven
// by levels alone would measure the first sleep of the process and trust every one after it. See
// Box.DeviceSlept and box_lifecycle.go.
func (s *CommandServer) Pause() {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordDeviceSleep()
	}
	if !(C.IsAndroid || C.IsIos) || C.IsTvOS {
		return
	}
	s.rememberDeviceLevel(true)
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	// CloseIdleConnections() used to be called here, the moment the device paused. It is not any
	// more: closing the pool on every screen-off turns each unlock into a burst of DNS, TLS and QUIC
	// handshakes at exactly the moment the user wants a request answered. The pool is released later,
	// from the power governor's DEEP_IDLE transition, which is reached only once the device has stayed
	// paused with no real traffic - so a brief screen-off costs nothing and an afternoon in a pocket
	// does not hold sockets. See common/power and the Box observer that performs it.
	//
	// The sleep edge and the level are published together, and the level is not published separately
	// here: on Apple the level is entered once and lifted by an unlock, so a second Pause() that only
	// touched the level would leave the next sleep unmeasured - which is the reuse defect the edge
	// exists to close.
	instance.Box().DeviceSlept()
}

// Wake ends the device pause on the platform whose wake signal means the device is usable.
//
// # Why this is not the same thing everywhere
//
// On Android the wake command is driven by Doze leaving, which is a statement that the device is out
// of its low-power state and usable again: the pause ends and speculative work is released on the
// stagger.
//
// On iOS the same command arrives for every push and background task, while the phone is still
// locked and nobody is looking at it. Lifting the pause there would be wrong - it would release
// health checks, probes and provider refreshes for a device in a pocket, which is the wake storm the
// power policy exists to prevent. But ignoring it entirely is wrong too, and that is what this used
// to do: a resume is proof that the sleep ended, so reusable state that predates it has not been
// verified since, and the first flow after the unlock must not discover that by blackholing. The
// reuse boundary is that verdict. It retires idle pools, it does not dial, and it does not touch a
// stream that is carrying traffic or a single speculative category.
//
// An earlier version of this comment said "wake is ignored there and the pause ends on the screen
// state instead". The first half is no longer true - the resume publishes the reuse edge - and the
// second half was only ever true of a client that had a screen-state observer, which the revision
// this superproject pinned does not: see docs/fork/apple-screen-state-observer.md.
//
// # Why the remembered level is NOT moved here
//
// Whether this call moves the LEVEL is platform-dependent - it does on Android, it does not on iOS -
// and `rememberDeviceLevel` has to record what the manager now holds, not what this method would like
// it to hold. Recording `false` here would make the remembered level disagree with the manager on
// iOS, and the next reload would then release a pause the device is still in. So this method leaves
// the mirror alone and the two places that do move the level on both platforms - `RecordLockState`
// and `WakeNow` - are the two that write it. `Pause` writes it too, because it moves the level
// wherever the device axis exists.
func (s *CommandServer) Wake() {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordDeviceWake()
	}
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	if C.IsAndroid {
		// A Doze exit is a device wake: the level moves with the edge.
		instance.Box().DeviceWoke()
		return
	}
	instance.Box().DeviceResumed()
}

// WakeNow publishes a device wake that the platform has confirmed by other means.
//
// It is the dedicated host event: an unlock, a user-present transition, or a client that watched for
// one itself. It is the only entry point that lifts the device pause on Apple, and it is deliberately
// narrow - it publishes the reuse verdict and then releases the level, it dials nothing, and the
// release is staggered by the governor.
//
// It resolves the Box because the edge and the level are published through the same bridge; a wake
// with no running Box has no governor to publish to, and reaching the pause manager directly in that
// window would deliver the event to whichever governor is still registered - the resurrection shape
// a reload creates.
func (s *CommandServer) WakeNow() {
	s.rememberDeviceLevel(false)
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().DeviceWoke()
}

// RecordScreenState applies the display fact to the device axis, and records it for the power report.
//
// # Why this is not telemetry only any more
//
// It used to feed the recorder and nothing else, which made the device axis depend on a fact nobody
// published: the Apple client's sleep()/wake() overrides were the only driver, and on a client whose
// resume is a push the level was entered once and never lifted. The display fact is the platform's
// own "nobody is looking at this device" statement, and it is the fact the client's screen-state
// observer reports, so it drives the axis now - see box_lifecycle.go for the mapping and, in
// particular, for why a display turning ON publishes the resume edge but does NOT release the level:
// a push notification lights the lock screen, and treating that as "the device is usable" is the
// wake storm the power policy exists to prevent.
//
// # Apple only, and why
//
// Android already has exactly one writer of this fact - PlatformEvents.SetScreenOn, whose doc warns
// that two drivers of the device axis desync the shared policy - so driving it from here as well
// would be the double-write that warning is about. tvOS is excluded for the same reason Pause is:
// it has no sleep signal and no level to release.
func (s *CommandServer) RecordScreenState(on bool) {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordScreenState(on)
	}
	if !C.IsIos || C.IsTvOS {
		return
	}
	// A display going OFF is a level pause; a display going ON is a resume EDGE and does NOT release
	// the level (see box_lifecycle.go), so only the off direction is remembered.
	if !on {
		s.rememberDeviceLevel(true)
	}
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().ScreenStateChanged(on)
}

// RecordLockState applies the lock fact to the device axis, and records it for the power report.
//
// Locking is the same statement as a display going off, from a source that does not depend on whether
// a notification lit the screen, so it pauses and starts a measurement. UNLOCKING is the device
// wake: it is the one platform fact that means a person is using the device, and it is therefore the
// one that lifts the pause. Both are coalesced by the governor, so a client that reports the display
// and the lock for the same transition still produces one measurement and one boundary.
//
// Apple only, for the same reason RecordScreenState is.
func (s *CommandServer) RecordLockState(locked bool) {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordLockState(locked)
	}
	if !C.IsIos || C.IsTvOS {
		return
	}
	// Locked pauses the level, unlocked is the one fact that releases it - and it is the only release
	// this client has, so it is the one that must not be lost to a reload window.
	s.rememberDeviceLevel(locked)
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().LockStateChanged(locked)
}

// ReconcileScreenState applies a CURRENT-STATE read of the display to the device axis.
//
// The sibling of RecordScreenState, and the difference is the one P1-C is about. RecordScreenState
// carries a transition the platform witnessed and starts a new reuse measurement; this carries the
// answer to "what is the display doing right now", which the client takes at observer start and on
// every NetworkExtension resume. A re-read may only reconcile the LEVEL: it must never move the
// anchor of a measurement that is already running, because the resume that triggers it has already
// been published as a boundary, and re-anchoring there is what turns a measured five-hour sleep into
// the seconds between the resume and the read. See box_lifecycle.go for the full sequence.
//
// Recorded for the power report on the same terms as RecordScreenState, so a device trace shows the
// re-reads next to the transitions rather than hiding the fact that one happened. Apple only, for the
// same reason RecordScreenState is.
func (s *CommandServer) ReconcileScreenState(on bool) {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordScreenState(on)
	}
	if !C.IsIos || C.IsTvOS {
		return
	}
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().ReconcileScreenState(on)
}

// ReconcileLockState applies a CURRENT-STATE read of the lock to the device axis.
//
// Locked reconciles the pause and leaves the running measurement's anchor alone; unlocked is refused
// by the core, because a re-read cannot establish that a person unlocked the device. Apple only, for
// the same reason RecordScreenState is.
func (s *CommandServer) ReconcileLockState(locked bool) {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordLockState(locked)
	}
	if !C.IsIos || C.IsTvOS {
		return
	}
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().ReconcileLockState(locked)
}

func (s *CommandServer) ResetNetwork() {
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().Network().ResetNetwork(context.Background())
}

func (s *CommandServer) UpdateWIFIState() {
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	instance.Box().Network().UpdateWIFIState(context.Background())
}

type platformHandler CommandServer

func (h *platformHandler) ServiceStop() error {
	return (*CommandServer)(h).handler.ServiceStop()
}

func (h *platformHandler) ServiceReload(ctx context.Context) error {
	return (*CommandServer)(h).handler.ServiceReload()
}

func (h *platformHandler) SystemProxyStatus() (*daemon.SystemProxyStatus, error) {
	status, err := (*CommandServer)(h).handler.GetSystemProxyStatus()
	if err != nil {
		return nil, E.Cause(err, "get system proxy status")
	}
	return &daemon.SystemProxyStatus{
		Enabled:   status.Enabled,
		Available: status.Available,
	}, nil
}

func (h *platformHandler) SetSystemProxyEnabled(enabled bool) error {
	return (*CommandServer)(h).handler.SetSystemProxyEnabled(enabled)
}

func (h *platformHandler) TriggerNativeCrash() error {
	return (*CommandServer)(h).handler.TriggerNativeCrash()
}

func (h *platformHandler) WriteDebugMessage(message string) {
	(*CommandServer)(h).handler.WriteDebugMessage(message)
}

func (h *platformHandler) ConnectSSHAgent() (int32, error) {
	return (*CommandServer)(h).handler.ConnectSSHAgent()
}
