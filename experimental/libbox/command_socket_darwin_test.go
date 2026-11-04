//go:build darwin

package libbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// These tests run the kernel's own check. The unit tests prove the resolver's arithmetic; only
// bind(2) can prove the path is acceptable to Darwin, and only on a Darwin host.
//
// They use /tmp, not t.TempDir(): Go's test temp directory is itself deep enough on macOS that a
// container-shaped fixture built under it cannot fit in 103 bytes, which would test the fixture
// rather than the resolver. The depth being modelled is the device's, not the host's.
func darwinFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "jjb")
	if err != nil {
		t.Skipf("no writable /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func TestDarwinBindsTheResolvedPathInADeepContainer(t *testing.T) {
	root := darwinFixtureRoot(t)
	// The shape reported by the device: <root>/Data/PluginKitPlugin/<UUID>/{tmp, Library/...}.
	plugin := filepath.Join(root, "Data", "PluginKitPlugin", "5B6182FE-3CD7-4330-A11D-6336B908C07A")
	tempDir := filepath.Join(plugin, "tmp")
	basePath := filepath.Join(plugin, "Library", "Application Support", "JiejieBox-Shared")
	for _, dir := range []string{tempDir, basePath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if len(basePath)+len("/command.sock") <= maxUnixSocketPath {
		t.Skipf("the fixture base path is already short enough (%d bytes); it does not model the device", len(basePath))
	}

	resolved, err := resolveCommandSocketPath(basePath, tempDir)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: resolved, Net: "unix"})
	if err != nil {
		t.Fatalf("bind of the resolved path (%d bytes) failed: %v", len(resolved), err)
	}
	defer listener.Close()
	defer os.Remove(resolved)

	conn, err := net.Dial("unix", resolved)
	if err != nil {
		t.Fatalf("dial of the resolved path failed: %v", err)
	}
	_ = conn.Close()
}

func TestDarwinRejectsAPathOverTheLimit(t *testing.T) {
	// The control: a 104-byte name must fail at bind, and the resolver must never produce one.
	root := darwinFixtureRoot(t)
	deep := filepath.Join(root, "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pad := maxUnixSocketPath + 1 - len(deep) - 1
	if pad <= 0 {
		t.Skipf("the fixture root is %d bytes; too deep to build a %d-byte path", len(deep), maxUnixSocketPath+1)
	}
	name := make([]byte, pad)
	for index := range name {
		name[index] = 'a'
	}
	over := filepath.Join(deep, string(name))
	if len(over) != maxUnixSocketPath+1 {
		t.Fatalf("fixture is %d bytes, wanted %d", len(over), maxUnixSocketPath+1)
	}
	if _, err := net.ListenUnix("unix", &net.UnixAddr{Name: over, Net: "unix"}); err == nil {
		_ = os.Remove(over)
		t.Fatalf("the kernel accepted a %d-byte path", len(over))
	}

	// And the resolver, asked for a socket in a directory that leaves no room, chooses a shorter
	// ancestor rather than emitting the over-long path.
	longDir := fmt.Sprintf("%s/%s", deep, string(name[:40]))
	if err := os.MkdirAll(longDir, 0o755); err != nil {
		t.Fatalf("mkdir longDir: %v", err)
	}
	resolved, err := resolveCommandSocketPath(longDir, longDir)
	if err != nil {
		t.Fatalf("resolver refused a layout it can satisfy: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolver produced %d bytes", len(resolved))
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: resolved, Net: "unix"})
	if err != nil {
		t.Fatalf("the resolver answer does not bind (%d bytes): %v", len(resolved), err)
	}
	listener.Close()
	_ = os.Remove(resolved)
}
