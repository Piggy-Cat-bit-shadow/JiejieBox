package daemon

import (
	"context"
	"os"
	"reflect"
	"sync"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// This file pins the DEVICE AXIS's lifetime across a service reload, which is the one lifecycle
// boundary the Apple client deliberately does not cross.
//
// # Why a reload is the interesting boundary and not a restart
//
// `ExtensionProvider.reloadService()` replaces the running configuration inside the same command
// server and the same process. It says so in its own comment, and it is why the screen-state observer
// is started in `startTunnel` and torn down in `stopTunnel` only: the observer is deliberately made to
// outlive a reload, because re-registering the two Darwin notifications per reload would hand the core
// a second, independent device axis.
//
// The observer's contribution to that axis is a set of FACTS. What holds the facts is the core's pause
// manager, and `CommandServer.StartOrReloadService` builds a whole new Box. The two halves therefore
// have to agree about the lifetime of the level, and this file is where that agreement is checked.

// libboxContext is the shape `experimental/libbox.baseContext` builds: every registry a Box requires,
// no pause manager, and a working directory.
//
// The registries are EMPTY rather than populated from `include`, because `include` imports this
// package and the import would be a cycle. One registration is needed rather than merely present: a
// configuration with no `dns.servers` falls back to the `local` transport, so the DNS registry has to
// know that type or `box.New` refuses the profile. Everything else can stay empty - the minimal
// profile below declares no inbound, outbound, endpoint, service or certificate provider, and nothing
// on the device axis resolves one.
//
// The filemanager default is not decoration: `libbox.baseContext` sets one, and without it the
// cache-file service waits for a directory it will never be given and fails the whole Box with
// "initialize cache-file: timeout". The directory is per-test so two tests cannot share state.
//
// The absence of a pause manager is the subject of this file, not an omission: `box.Context`
// registers none, so the `pause.WithDefaultManager(ctx)` inside `box.New` creates a fresh one - and
// `NewStartedService`, which is what these tests are about, registers one for the service instead.
func libboxContext(t *testing.T) context.Context {
	t.Helper()
	// Not `t.TempDir()`: the service opens a `cache.db` in this directory and on Windows an open file
	// cannot be unlinked, so the automatic RemoveAll cleanup fails the test after it has passed. The
	// directory is still per-test and still removed, just without turning a held handle into a failure.
	workingPath, err := os.MkdirTemp("", "sing-box-device-axis-")
	if err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workingPath) })
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inbound.NewRegistry(),
		outbound.NewRegistry(),
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	return filemanager.WithDefault(ctx, workingPath, workingPath, -1, -1)
}

// minimalConfig is the smallest profile a Box will build from. Everything this file asks about - the
// pause manager and the device axis - exists before any inbound or outbound, so the profile carries
// none of them and the test cannot be confused by a subsystem it did not mean to start.
const minimalConfig = `{"log":{"level":"error"}}`

// TestTheDevicePauseSurvivesAServiceReload is the device axis's cross-reload invariant.
//
// # The defect it pins
//
// `pause.WithDefaultManager` returns an EXISTING manager unchanged and otherwise registers a NEW one
// into the context it was handed. `box.New` calls it on the per-Box context and its comment claimed the
// libbox/Apple path supplied one already - but nothing did. Before this fix a repository-wide search
// for `WithDefaultManager` and for a registration of `pause.Manager` found exactly one production site,
// `box.go:380`, the `WithDefaultManager(ctx)` call itself; `MustRegister[pause.Manager]` and
// `ContextWith[pause.Manager]` did not exist anywhere in the tree.
//
// `libbox.baseContext` calls `box.Context(...)`, which registers the inbound, outbound, endpoint, DNS,
// service and certificate registries and no pause manager. `daemon.newInstance` builds every Box from
// `service.ExtendContext(s.ctx)`, and `ExtendContext` is a shallow registry CLONE, so a manager
// registered into a child registry would not reach `s.ctx` for the next Box either.
//
// So every Box gets its own manager, created unpaused, and `StartOrReloadService` replaces the Box.
// The sequence on a locked phone is:
//
//	lock the device     -> RecordLockState(true) -> DevicePause() -> devicePaused == true
//	reload the profile  -> a new Box and a new manager        -> devicePaused == false
//	the observer stays  -> lastObserved[lock] == 1, so resync() reads the same value, treats it as a
//	                       repeat (ScreenStateObserver.swift:243-245) and publishes nothing
//
// and nothing re-asserts the fact the platform already reported. `route/reference.go` re-mirrors "not
// paused" from the new manager, `protocol/group/urltest.go` stops refusing to run, provider refresh is
// released, and the on-demand tunnels' eligibility pass stops suspending what it should. The level is
// lost silently: no log line, no error, and no self-correction until the next delivered lock/unlock
// pair.
//
// The test drives the real service and the real manager, so it fails on the shipped composition rather
// than on a stand-in.
func TestTheDevicePauseSurvivesAServiceReload(t *testing.T) {
	started := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))

	before := started.Instance()
	require.NotNil(t, before, "StartOrReloadService returned without a running instance")
	beforeManager := before.PauseManager()
	require.NotNil(t, beforeManager, "the running Box has no pause manager, so the device axis does not exist")

	require.False(t, beforeManager.IsDevicePaused(), "a fresh Box must not start paused")
	beforeManager.DevicePause()
	require.True(t, beforeManager.IsDevicePaused(), "DevicePause did not enter the level")

	// The reload: same service, same command server, a replacement Box - which is exactly what
	// `ExtensionProvider.reloadService()` performs.
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))

	after := started.Instance()
	require.NotNil(t, after, "the reload produced no running instance")
	afterManager := after.PauseManager()
	require.NotNil(t, afterManager)

	// The identity check is the diagnosis, separated from the invariant below so a failure says which
	// of the two went wrong.
	require.Same(t, beforeManager, afterManager,
		"a service reload replaced the pause manager, so the device axis is per-Box while every platform "+
			"fact about it - sleep, wake, lock, display - is per-PROCESS and arrives through a command "+
			"server that outlives the Box. The manager must be created once for the service's life "+
			"(register one into the context the service is built from) so the level outlives the Box that "+
			"happens to be running")

	require.True(t, afterManager.IsDevicePaused(),
		"the device pause did not survive a service reload. The Apple client's screen-state observer "+
			"deliberately outlives a reload and its `lastObserved` makes the still-locked snapshot a "+
			"repeat, so nothing re-publishes the fact: the level is lost, no log line records it, and the "+
			"device stays unpaused until the next lock/unlock pair")

	require.NoError(t, started.CloseService())
}

// TestAConfigurationCheckSharesTheSessionsDeviceAxis is the second consequence of one manager per
// service, and it is the one that is easy to miss because it does not go through `newInstance`.
//
// `CheckConfig` builds a throwaway Box straight from `s.ctx` to validate a profile. With the manager
// now registered on that context, the throwaway Box resolves the SESSION's manager rather than one of
// its own, registers its own governor callback on it, and releases that callback when it closes
// (`box.New`'s `releasePowerGovernor` defer). Two things have to hold:
//
//   - validating a profile must not disturb the level of the running service, and
//   - the throwaway Box must not leave a callback behind on a manager that outlives it, which is the
//     shape `box.go`'s own comment warns about - "every device pause and wake for the rest of the
//     session would call into a governor whose Box was never returned".
//
// Both are asserted on the level and on the callback count, so a regression that leaks the callback
// fails here rather than in production as a stall.
func TestAConfigurationCheckSharesTheSessionsDeviceAxis(t *testing.T) {
	started := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))

	manager := started.Instance().PauseManager()
	require.NotNil(t, manager)
	manager.DevicePause()
	require.True(t, manager.IsDevicePaused())

	callbacksBefore := pauseCallbackCount(t, manager)
	require.Positive(t, callbacksBefore,
		"the running service's Box registered no governor callback on the session's manager, so this "+
			"test cannot tell a leak from a correct release")

	// A live configuration check, while the device is paused and the service is running.
	require.NoError(t, started.CheckConfig(context.Background(), minimalConfig),
		"a configuration check failed while a service was running, which is the desktop daemon's "+
			"normal path when the user edits a profile with the tunnel up")

	require.True(t, manager.IsDevicePaused(),
		"validating a profile lifted the device pause of the running service, so a config check is now "+
			"a way to wake a locked device")
	require.Equal(t, callbacksBefore, pauseCallbackCount(t, manager),
		"the configuration check's throwaway Box left its governor callback registered on the SESSION's "+
			"pause manager. That manager now outlives every Box, so the entry would never be removed and "+
			"every device pause and wake for the rest of the session would call into a governor whose Box "+
			"was thrown away - exactly the hazard box.New's releasePowerGovernor defer exists for")

	require.Same(t, manager, started.Instance().PauseManager(),
		"the check's throwaway Box replaced the running service's device axis")

	require.NoError(t, started.CloseService())
}

// TestTheReplacementBoxSGovernorAgreesWithTheHeldLevel is the half of the reload that the level
// itself does not cover.
//
// # The defect it pins
//
// `pause.Manager`'s level calls are level-GUARDED: `DevicePause` on an already-paused manager is a
// silent no-op that emits no event. That is what makes the level survive a reload, and it is also why
// a reload cannot re-tell the replacement Box anything: `box.New` builds a FRESH `power.Governor`
// (state ACTIVE), and with the manager already paused no `EventDevicePaused` will ever reach it for
// the sleep that is still in progress.
//
// A governor that believes the device is awake is not a cosmetic problem. `common/power`'s QUIESCENT
// row is what denies `ProviderRefresh` and `Statistics`, so a locked phone whose configuration was
// just reloaded goes back to refreshing remote rule sets (`route/rule/rule_set_updater.go` blocks on
// the governor's provider-refresh gate) and writing statistics snapshots - for the rest of the lock,
// with no log line. A later lock fact does not repair it either: a lock is an EDGE, and `SleepStarted`
// on a governor that never saw the sleep does not enter the level.
//
// The fix seeds the governor from the manager's level in `box.New`, so "the governor agrees with the
// level" holds from construction. This test asserts the agreement rather than the mechanism.
func TestTheReplacementBoxSGovernorAgreesWithTheHeldLevel(t *testing.T) {
	started := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))

	manager := started.Instance().PauseManager()
	require.NotNil(t, manager)
	require.False(t, manager.IsDevicePaused())

	// The device is awake, so the first Box's governor is legitimately permissive.
	awake := governorOf(t, started)
	require.True(t, awake.Allow().ProviderRefresh, "an awake device must be allowed to refresh providers")

	manager.DevicePause()
	require.True(t, manager.IsDevicePaused())

	// The reload: a replacement Box, and therefore a replacement governor.
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.Same(t, manager, started.Instance().PauseManager(), "the level owner must not change")

	paused := governorOf(t, started)
	allow := paused.Allow()
	require.True(t, manager.IsDevicePaused(), "the level is held")
	require.False(t, allow.ProviderRefresh,
		"the replacement Box's governor permits provider refresh while the device is paused, so a reload "+
			"puts a locked phone back to refreshing remote rule sets for the rest of the lock")
	require.False(t, allow.Statistics,
		"the replacement Box's governor permits statistics while the device is paused")
	require.True(t, allow.HealthCheck,
		"QUIESCENT allows health checks; denying them would be a different policy than the one the "+
			"first Box applied, and this test is about agreement, not about tightening the policy")

	// And the release direction is symmetric: an unlock must return the replacement Box to ACTIVE.
	manager.DeviceWake()
	require.False(t, manager.IsDevicePaused())
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.True(t, governorOf(t, started).Allow().ProviderRefresh,
		"a reload while the device is awake must not leave the replacement Box's governor paused")

	require.NoError(t, started.CloseService())
}

// governorOf resolves the power governor of the running Box through the same service registry the Box
// registered it into. `Box` exposes no accessor for it, and the registry is how every production
// consumer reaches it (`service.FromContext[*power.Governor]`), so this is the real lookup rather than
// a reading of an internal field.
func governorOf(t *testing.T, started *StartedService) *power.Governor {
	t.Helper()
	instance := started.Instance()
	require.NotNil(t, instance)
	governor := service.FromContext[*power.Governor](instance.ctx)
	require.NotNil(t, governor, "the running Box did not register a power governor in its own context")
	return governor
}

// pauseCallbackCount is the number of callbacks registered on a pause manager.
//
// Read through reflection because the count is the only observable proof that a manager which now
// outlives its Boxes does not accumulate callbacks from the throwaway Boxes a configuration check
// builds. `pause.defaultManager` keeps the list in an unexported field and exposes no accessor, and
// adding one to `sing` for a test would be a dependency change; the reflection is confined to this
// helper and fails loudly if the field is ever renamed rather than silently returning zero.
func pauseCallbackCount(t *testing.T, manager pause.Manager) int {
	t.Helper()
	value := reflect.ValueOf(manager)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("callbacks")
	if !field.IsValid() {
		t.Fatalf("pause manager %T has no `callbacks` field; this helper is stale and its assertion "+
			"would silently pass", manager)
	}
	length := field.FieldByName("len")
	if !length.IsValid() || length.Kind() != reflect.Int {
		t.Fatalf("pause manager %T's `callbacks` list has no readable length; this helper is stale "+
			"and its assertion would silently pass", manager)
	}
	// `.Int()` rather than `.Interface().(int)`: the field is unexported, and `Interface()` refuses
	// to hand one out.
	return int(length.Int())
}

// TestCloseIsIdempotentUnderConcurrency pins the guard that keeps a double shutdown from panicking the
// extension process.
//
// # The defect it pins
//
// `StartedService.Close` sets a `closed` flag under `serviceAccess` and then closes five
// `observable.Subscriber`s outside that lock. The flag fences the SERVICE's lifecycle, not the five
// closes: two goroutines can both read `closed == false`, both fall through, and both close the same
// subscriber. `Subscriber.Close` is a check-then-act on a done channel, so the loser panics with
// "close of closed channel" - reproduced here with eight concurrent calls before the fix, on the
// first run.
//
// The panic is in the embedder's process, so on the Apple client it kills the tunnel. The two callers
// there are `stopTunnel` (via `stopService`) and `unwindFailedStart()`, which run on different tasks
// with nothing serialising them - and `Box` already carries a `closeOnce` for exactly this reason.
//
// Sequential repeats are asserted too, so a fix cannot trade one shape for the other.
func TestCloseIsIdempotentUnderConcurrency(t *testing.T) {
	started := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	require.NoError(t, started.StartOrReloadService(context.Background(), minimalConfig, nil))

	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			started.Close()
		}()
	}
	waitGroup.Wait()

	// And the sequential shape, which was already safe and must stay so.
	for range 50 {
		started.Close()
	}
}
