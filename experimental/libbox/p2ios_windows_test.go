//go:build p2ios

package libbox

import (
	"context"
	"os"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// This file and its siblings carry the `p2ios` build tag on purpose, and that tag is the whole point.
//
// # Why a build tag, when nothing else in this tree uses one for a test
//
// The device axis this package drives is behind `if !(C.IsAndroid || C.IsIos) || C.IsTvOS { return }`,
// and `constant/goos` sets BOTH of those to 0 on windows, linux and darwin alike - `IsIos` is 1 only for
// `GOOS=ios`. So on every host the test suite runs on, `CommandServer.Pause`, `Wake`, `WakeNow`,
// `RecordScreenState`, `RecordLockState` and `restoreDeviceLevel` are all dead code: four entry points and
// a reload hook that no test can reach, covering the level that decides whether a locked phone runs
// background work.
//
// That gap cannot be closed by a normal test, because the constant is compile-time. It is closed by
// running the suite with a two-file overlay that flips the constant and enables this tag:
//
//	# a copy of constant/goos/zgoos_windows.go with `const IsIos = 1`
//	go test -tags "…,p2ios" -overlay overlay.json ./experimental/libbox/
//
// and the overlay's Replace maps both `p2ios_windows.go` (below) and `zgoos_windows.go`. Without the tag
// these files do not compile, so a normal `go test ./...` is unaffected; with it, the Apple device axis is
// exercised for real. The command is recorded here rather than in a script because it is three lines and
// the overlay itself is host-specific.
//
// Deliberately NOT a stub or a fake: the tests below drive the REAL `CommandServer` against the REAL
// `StartedService` and the REAL `pause.Manager`. Only the compile-time platform constant is virtualized.

// libboxContextForDeviceAxis is the context `experimental/libbox.baseContext` builds: every registry a
// Box requires, a working directory, and no pause manager - so `daemon.NewStartedService` is the thing
// that registers one, which is what these tests are about.
func libboxContextForDeviceAxis(t *testing.T) context.Context {
	t.Helper()
	// Not `t.TempDir()`: the service opens a `cache.db` in this directory and on Windows an open file
	// cannot be unlinked, so the automatic cleanup would fail the test after it passed.
	workingPath, err := os.MkdirTemp("", "libbox-device-axis-")
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

// deviceAxisFixture is one running service with a real command server pointed at it.
type deviceAxisFixture struct {
	started *daemon.StartedService
	server  *CommandServer
}

// newDeviceAxisFixture starts a service and wraps it in a command server.
//
// `NewCommandServer` is the real constructor: it is what installs the power-report manager that every
// record method touches before it looks at the instance, so a hand-built struct would test less.
func newDeviceAxisFixture(t *testing.T) *deviceAxisFixture {
	t.Helper()
	started := daemon.NewStartedService(daemon.ServiceOptions{Context: libboxContextForDeviceAxis(t)})
	if err := started.StartOrReloadService(context.Background(), deviceAxisConfig, nil); err != nil {
		t.Fatalf("start service: %v", err)
	}
	server, err := NewCommandServer(deviceAxisHandler{}, deviceAxisPlatform{})
	if err != nil {
		t.Fatalf("create command server: %v", err)
	}
	server.StartedService = started
	t.Cleanup(func() { _ = started.CloseService() })
	return &deviceAxisFixture{started: started, server: server}
}

// reload performs the operation the Apple client's `reloadService` performs.
func (f *deviceAxisFixture) reload(t *testing.T) {
	t.Helper()
	if err := f.started.StartOrReloadService(context.Background(), deviceAxisConfig, nil); err != nil {
		t.Fatalf("reload service: %v", err)
	}
}

// TestCommandServerCloseClosesTheBox pins the ownership boundary: CommandServer.Close is the
// process-local shutdown, not just a subscriber cleanup. An embedder that closes the transport
// without issuing the separate CloseService RPC must not leave the Box (TUN, DNS, goroutines)
// running behind a closed command server.
func TestCommandServerCloseClosesTheBox(t *testing.T) {
	fixture := newDeviceAxisFixture(t)
	require.NotNil(t, fixture.started.Instance())
	fixture.server.Close()
	require.Nil(t, fixture.started.Instance(), "CommandServer.Close must close the running Box")
}

// deviceAxisPlatform is the platform surface `NewCommandServer` consumes.
//
// Embedding the interface means every method this test does not reach is a nil-interface call - a visible
// panic - rather than a silent zero value, which is the honest failure for a fixture. The handful the
// constructor calls eagerly are supplied, and each answers "no platform capability" so nothing is claimed
// that this test does not implement.
type deviceAxisPlatform struct {
	PlatformInterface
}

// The assertion is the point: a new eager call in the constructor turns into a compile error here rather
// than a nil dereference in a test run.
var _ PlatformInterface = deviceAxisPlatform{}

func (deviceAxisPlatform) LocalDNSTransport() LocalDNSTransport { return nil }

func (deviceAxisPlatform) UseProcFS() bool { return false }

func (deviceAxisPlatform) UsePlatformAutoDetectInterfaceControl() bool { return false }

// deviceAxisHandler is the `CommandServerHandler` half - the callbacks the command server makes INTO the
// platform. None of them is reached by a record method, and embedding the interface makes any that were
// reached a visible panic rather than a silent no-op.
type deviceAxisHandler struct {
	CommandServerHandler
}

// deviceAxisConfig is the smallest profile a Box builds from. Nothing on the device axis needs an
// inbound, an outbound or a service, so the profile carries none and the test cannot be confused by a
// subsystem it did not mean to start.
const deviceAxisConfig = `{"log":{"level":"error"}}`
