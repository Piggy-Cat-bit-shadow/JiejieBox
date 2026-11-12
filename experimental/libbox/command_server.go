package libbox

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

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
	// sleepEpisode numbers macOS sleep episodes so the wake and the watchdog reports for one sleep
	// share an identity. It is monotonic for the life of the process and survives a reload; the
	// network manager dedups by it instead of by a wall-clock window, which could swallow a second,
	// independent sleep. See NetworkManager.SystemResumed.
	sleepEpisode atomic.Uint64
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

// systemResumer is the network manager capability that re-observes the environment and drains the
// pools after a host suspend.
//
// It is a capability rather than a method on adapter.NetworkManager for the same reason the other
// optional contracts in this tree are: a test double or a platform manager with no sleep signal has
// no opinion about it, and asking for it directly degrades to "nothing to do" instead of forcing
// every implementation to grow a method.
type systemResumer interface {
	// SetSleepEpisode tells the manager which platform sleep the next resume closes. The
	// NetworkExtension wake and the watchdog both carry the same number for one sleep.
	SetSleepEpisode(episode uint64)
	// SystemResumed closes the episode. Episode 0 means the caller has no platform number (the
	// watchdog); the manager falls back to its own numbering when the platform announced nothing.
	SystemResumed(episode uint64)
}

func NewCommandServer(handler CommandServerHandler, platformInterface PlatformInterface) (*CommandServer, error) {
	ctx := baseContext(platformInterface)
	powerManager := powerreport.NewManager()
	service.MustRegister[*powerreport.Manager](ctx, powerManager)
	platformWrapper := &platformInterfaceWrapper{
		iif:          platformInterface,
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
		sockPath, sockErr := resolveCommandSocketPath(sBasePath, sTempPath)
		if sockErr != nil {
			return E.Cause(sockErr, "listen command server")
		}
		// Only a name this package could have created is cleared. The base path's command.sock is
		// ours by convention; a fallback name carries our hash. Anything else in these
		// directories belongs to somebody else and is left alone: an unconditional remove here
		// would delete a file the command server has no claim on.
		if base := filepath.Base(sockPath); base == commandSocketName || isGeneratedSocketName(base) {
			os.Remove(sockPath)
		}
		listener, err = net.ListenUnix("unix", &net.UnixAddr{
			Name: sockPath,
			Net:  "unix",
		})
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
	// Close is the process-local shutdown, so it owns the Box as well as the subscribers. The
	// separate CloseService RPC is the ordered path a client uses while the transport is alive;
	// an embedder that tears the transport down first must not leave the Box running merely
	// because it did not issue one last RPC. CloseService is idempotent and reuses the same
	// lifecycle lock, so the ordered path and this one cannot race into a double close.
	_ = s.CloseService()
	s.StartedService.Close()
	s.oomRecorder.Close()
	s.powerManager.Close()
}

type OverrideOptions struct {
	AutoRedirect   bool
	IncludePackage StringIterator
	ExcludePackage StringIterator
	// LoopbackAddress carries the addresses the platform wants the effective TUN to answer for
	// locally, e.g. the Apple client's "Local Signing Loopback" address. It is a list rather than
	// a flag so the core stays generic: the platform decides which address, this only transports
	// it. Empty (the default everywhere) leaves the parsed configuration exactly as it was.
	LoopbackAddress StringIterator
}

// parseLoopbackAddresses converts the platform's list into addresses the core can merge.
//
// An entry that does not parse is dropped rather than failing the start: the list is a platform
// hint about one optional feature, and refusing to start the tunnel because a hint was malformed
// would turn a cosmetic problem into "the VPN does not work". The drop is visible to the platform
// in that the address it asked for is simply not in the TUN, which is what it would get anyway if
// it had not asked.
func parseLoopbackAddresses(iterator StringIterator) []netip.Addr {
	if iterator == nil {
		return nil
	}
	var addresses []netip.Addr
	for iterator.HasNext() {
		address, err := netip.ParseAddr(iterator.Next())
		if err != nil {
			continue
		}
		addresses = append(addresses, address)
	}
	return addresses
}

// StartOrReloadService applies a configuration to the running service.
//
// The error-only contract is unchanged for every existing embedder. `ApplyServiceChangeReport` is
// the same attempt with the report attached; keeping both means an older client cannot be affected
// by the newer one, and the newer one cannot drift from the older one's behaviour.
func (s *CommandServer) StartOrReloadService(configContent string, options *OverrideOptions) error {
	_, err := s.applyServiceChange("", configContent, options)
	return err
}

// ApplyServiceChangeReport applies a configuration and returns the report THIS call produced, as a
// versioned JSON document.
//
// # Why this is a separate method instead of "call apply, then read the last report"
//
// Reading `GetLastServiceChange` after an apply is not a receipt: the report it returns belongs to
// whichever attempt was last, and an apply that was interrupted or recovered returns no error at
// all, so a caller has nothing else to distinguish "my document is running" from "something else
// is". This method leaves the daemon's own ownership boundary (`StartedService.ApplyServiceChange`
// holds `lifecycleAccess` across the whole attempt) with the report in hand, and it never returns a
// Go error, because the gomobile bridge throws on a non-nil error and would drop the document:
// the outcome, including a failure, travels INSIDE the document.
//
// See `daemon.DescribeServiceChangeOutcome` for the shape and for why `ok` is not "no error".
func (s *CommandServer) ApplyServiceChangeReport(configContent string, options *OverrideOptions) *StringBox {
	return s.ApplyServiceChangeReportWithRequest("", configContent, options)
}

// ApplyServiceChangeReportWithRequest is ApplyServiceChangeReport with a caller-supplied request
// token.
//
// # Where the token has to live, and why it is here
//
// The daemon protocol has no Reload RPC: a profile is applied through this process-internal path
// (`CommandServer` -> `StartedService`), and the platform sends the patch with the call. A token that
// travelled on the wire would need a request field the protocol does not have, so it travels on this
// call and comes back inside the report as `requestId`, next to the instance identity the report now
// names. That is enough for a client to answer "did MY reload produce the instance I am talking to",
// including for the two outcomes that return no error at all.
func (s *CommandServer) ApplyServiceChangeReportWithRequest(requestID string, configContent string, options *OverrideOptions) *StringBox {
	report, applyErr := s.applyServiceChange(requestID, configContent, options)
	return wrapString(daemon.EncodeServiceChangeOutcome(daemon.DescribeServiceChangeOutcome(report, applyErr)))
}

// applyServiceChange is the one implementation behind the exported entry points.
func (s *CommandServer) applyServiceChange(requestID string, configContent string, options *OverrideOptions) (*daemon.ServiceChangeReport, error) {
	saveConfigSnapshot(configContent)
	if s.powerManager.Recorder() != nil {
		copyConfigSnapshot(filepath.Join(sWorkingPath, powerreport.DraftDirectoryName))
	}
	report, err := s.StartedService.ApplyServiceChangeWithRequest(s.ctx, requestID, configContent, &daemon.OverrideOptions{
		AutoRedirect:    options.AutoRedirect,
		IncludePackage:  iteratorToArray(options.IncludePackage),
		ExcludePackage:  iteratorToArray(options.ExcludePackage),
		LoopbackAddress: parseLoopbackAddresses(options.LoopbackAddress),
	})
	if err != nil {
		// The report is returned with the error: a failed attempt still has a state worth
		// reporting (which document runs, whether the previous one was retained, and why).
		return report, E.Cause(err, "start or reload service")
	}
	// The window is closed again: whatever the platform reported while there was no Box is applied to
	// the Box that now exists. See rememberDeviceLevel.
	s.restoreDeviceLevel()
	return report, nil
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
	if !(C.IsAndroid || C.IsIos || C.IsDarwin) || C.IsTvOS {
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
	// A replacement Box has a fresh network manager. If the device is still in the sleep this reload
	// happened inside, hand it the sleep episode Pause numbered; otherwise a watchdog that fires
	// before the wake would number it independently and a later Wake would look like a second sleep.
	if C.IsDarwin && manager.IsDevicePaused() {
		if episode := s.sleepEpisode.Load(); episode != 0 {
			if box := instance.Box(); box != nil {
				if resumer, isResumer := box.Network().(systemResumer); isResumer {
					resumer.SetSleepEpisode(episode)
				}
			}
		}
	}
}

// Pause enters the device pause on the platform whose sleep signal means the device is going to
// sleep.
//
// It publishes the sleep EDGE and then the LEVEL, and it is the Apple and Android `sleep()` override.
// The edge is what matters on Apple, where nothing but an unlock (iOS) or a system wake (macOS)
// lifts the level: a governor driven by levels alone would measure the first sleep of the process
// and trust every one after it. See Box.DeviceSlept and box_lifecycle.go.
//
// macOS is included because its NetworkExtension `sleep()` is the one sleep signal a Mac reports.
// Before this, Pause returned for anything that was not Android or iOS, so a sleeping Mac never
// entered the device axis and never earned a reuse boundary; the pools the sleep invalidated stayed
// in use. docs/fork/post-wake-reuse.md §6 recorded that as a known gap.
func (s *CommandServer) Pause() {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordDeviceSleep()
	}
	if !(C.IsAndroid || C.IsIos || C.IsDarwin) || C.IsTvOS {
		return
	}
	s.rememberDeviceLevel(true)
	// Number the macOS sleep before the instance is resolved. The number is what lets the wake and
	// the watchdog reports for THIS sleep be deduplicated without a wall-clock window; a second sleep
	// increments it even if it happens a second later. The manager is told when one exists; if the
	// service is mid-reload, Wake carries the same number to the replacement Box.
	var sleepEpisode uint64
	if C.IsDarwin {
		sleepEpisode = s.sleepEpisode.Add(1)
	}
	instance := s.StartedService.Instance()
	if instance == nil || instance.Box() == nil {
		return
	}
	if C.IsDarwin {
		if resumer, isResumer := instance.Box().Network().(systemResumer); isResumer {
			resumer.SetSleepEpisode(sleepEpisode)
		}
	}
	// CloseIdleConnections() used to be called here, the moment the device paused. It is not any
	// more: closing the pool on every screen-off turns each unlock into a burst of DNS, TLS and QUIC
	// handshakes at exactly the moment the user wants a request answered. The pool is released later,
	// from the power governor's DEEP_IDLE transition, which is reached only once the device has stayed
	// paused with no real traffic - so a brief screen-off costs nothing and an afternoon in a pocket
	// does not hold sockets. See common/power and the Box observer that performs it.
	//
	// The sleep edge and the level are published together, and the level is not published separately
	// here: on Apple the level is entered once and lifted by an unlock (iOS) or a system wake
	// (macOS), so a second Pause() that only touched the level would leave the next sleep unmeasured -
	// which is the reuse defect the edge exists to close.
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
// On macOS the same command is the NetworkExtension's system-wake callback, and unlike iOS it is not
// delivered for every push: the process only runs again because the machine did. The pause is lifted
// (the device axis is entered by Pause on macOS too) and the network is reset, because a Mac that
// slept on the same Wi-Fi produces no interface callback and every socket it held may be dead. See
// route.NetworkManager.SystemResumed and docs/fork/post-wake-reuse.md.
//
// On iOS the same command arrives for every push and background task, while the phone is still
// locked and nobody is looking at it. Lifting the pause there would be wrong - it would release
// health checks, probes and provider refreshes for a device in a pocket, which is the wake storm the
// power policy exists to prevent. But ignoring it entirely is wrong too: a resume is proof that the
// sleep ended, so reusable state that predates it has not been verified since, and the first flow
// after the unlock must not discover that by blackholing. The reuse boundary is owed at the END of
// that episode, which on this platform is the unlock - not this resume.
//
// An earlier version of this comment said the resume published the reuse edge itself. It does not:
// `Box.DeviceResumed` is deliberately empty after the P1-C measurement (a resume inside a locked
// episode must not re-anchor the episode's boundary), and the display/lock facts are what publish it.
// A future reader who "restores" the edge on a resume re-opens the 28-minute-sleep defect recorded in
// box_lifecycle.go.
//
// # Why the remembered level is moved on Android and macOS but NOT on iOS
//
// Whether this call moves the LEVEL is platform-dependent - it does on Android and macOS, it does not
// on iOS - and `rememberDeviceLevel` has to record what the manager now holds, not what this method
// would like it to hold. On Android and macOS the level moves with the edge, so the mirror is written
// before the instance is resolved, which covers the reload window where there is no Box to move; a
// mirror left saying "paused" would be re-applied by the next `restoreDeviceLevel` and re-enter the
// level on a device that is awake. On iOS the level does not move here, so writing `false` would make
// the mirror disagree with the manager and the next reload would then release a pause the device is
// still in - there the two places that do move the level are `RecordLockState` and `WakeNow`, and
// they are the two that write it. `Pause` writes it too, because it moves the level wherever the
// device axis exists.
func (s *CommandServer) Wake() {
	recorder := s.powerManager.Recorder()
	if recorder != nil {
		recorder.RecordDeviceWake()
	}
	// Record the level BEFORE the instance is resolved. This is the reload window: on Android and
	// macOS this call moves the level, so a wake that arrives while there is no Box must still move
	// the mirror the replacement Box is restored from. On iOS the level does not move here and the
	// mirror must not claim it did - see the comment above.
	if C.IsAndroid || C.IsDarwin {
		s.rememberDeviceLevel(false)
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
	if C.IsDarwin {
		// The system woke the machine, so this is the device becoming usable: the level moves with
		// the edge, exactly as Android's Doze exit. The reset is the half the process cannot infer
		// from its own sockets: after a sleep on the same network the interface does not move and no
		// callback arrives, so the stale pools have to be drained here.
		instance.Box().DeviceWoke()
		episode := s.sleepEpisode.Load()
		if resumer, isResumer := instance.Box().Network().(systemResumer); isResumer {
			// Re-state the episode for the replacement Box: Pause may have run with no instance
			// while a reload was in progress. Episode 0 means no sleep was announced; the manager
			// treats that as a watchdog-style resume and numbers it itself.
			if episode != 0 {
				resumer.SetSleepEpisode(episode)
			}
			resumer.SystemResumed(episode)
		}
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

// ServiceReloadWithRequest runs the same platform reload the legacy entry runs, and observes the
// service identity around it. The platform owns what a reload means (the extension restarts its
// box); this method only answers which instance and revision the caller ended up with.
func (h *platformHandler) ServiceReloadWithRequest(ctx context.Context, requestID string, expectedInstanceID string) (*daemon.ReloadOutcome, error) {
	server := (*CommandServer)(h)
	previous := server.StartedService.ServiceIdentity()
	stale := expectedInstanceID != "" && expectedInstanceID != previous.InstanceID
	err := server.handler.ServiceReload()
	if err != nil {
		return nil, err
	}
	identity := server.StartedService.ServiceIdentity()
	status := daemon.ReloadStatusStopped
	if identity.InstanceID != "" {
		status = daemon.ReloadStatusRestarted
	}
	return &daemon.ReloadOutcome{
		RequestID:      requestID,
		InstanceID:     identity.InstanceID,
		ActiveRevision: identity.ActiveRevision,
		Stale:          stale,
		Status:         status,
	}, nil
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
