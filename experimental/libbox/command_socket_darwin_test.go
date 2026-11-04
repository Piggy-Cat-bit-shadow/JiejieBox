//go:build darwin

package libbox

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run the kernel's own check on the shape the device reported: a Packet Tunnel with no
// App Group, its base path under Application Support, the platform temp path it passes being that
// same tree's Library/Caches, and the only short directory being its TMPDIR.
//
// The fixture root is padded so the base path exceeds the limit; otherwise the resolver would
// (correctly) keep the documented name and the test would never reach the fallback.
func darwinContainerFixture(t *testing.T) (basePath, platformTemp, systemTemp string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", strings.Repeat("j", 40))
	if err != nil {
		t.Skipf("no writable /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	basePath = filepath.Join(root, "Library", "Application Support", "JiejieBox-Shared")
	platformTemp = filepath.Join(basePath, "Library", "Caches")
	systemTemp = filepath.Join(root, "tmp")
	for _, dir := range []string{basePath, platformTemp, systemTemp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if len(basePath)+len("/"+commandSocketName) <= maxUnixSocketPath {
		t.Skipf("fixture base path is %d bytes; too short to model the device", len(basePath))
	}
	return basePath, platformTemp, systemTemp
}

func TestDarwinBindsTheResolvedPathInADeepContainer(t *testing.T) {
	basePath, platformTemp, systemTemp := darwinContainerFixture(t)

	resolved, err := resolveCommandSocketPathIn(basePath, platformTemp, systemTemp)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolved %d bytes: %s", len(resolved), resolved)
	}
	if dir := filepath.Dir(resolved); dir != filepath.Clean(systemTemp) {
		t.Fatalf("resolved into %q, want the container temporary directory %q", dir, systemTemp)
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

// The same container, with the platform temp argument absent: the answer must still be bindable,
// because the system temporary directory is a candidate unconditionally.
func TestDarwinBindsWithoutThePlatformTempArgument(t *testing.T) {
	basePath, _, systemTemp := darwinContainerFixture(t)
	resolved, err := resolveCommandSocketPathIn(basePath, "", systemTemp)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: resolved, Net: "unix"})
	if err != nil {
		t.Fatalf("bind failed (%d bytes): %v", len(resolved), err)
	}
	listener.Close()
	_ = os.Remove(resolved)
}

// The control: a 104-byte name is refused by the kernel, which is what "allows 103" means.
func TestDarwinRejectsAPathOverTheLimit(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "jjb")
	if err != nil {
		t.Skipf("no writable /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	deep := filepath.Join(root, "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pad := maxUnixSocketPath + 1 - len(deep) - 1
	if pad <= 0 {
		t.Skipf("fixture root is %d bytes; too deep", len(deep))
	}
	name := strings.Repeat("a", pad)
	over := filepath.Join(deep, name)
	if len(over) != maxUnixSocketPath+1 {
		t.Fatalf("fixture is %d bytes, wanted %d", len(over), maxUnixSocketPath+1)
	}
	if _, err := net.ListenUnix("unix", &net.UnixAddr{Name: over, Net: "unix"}); err == nil {
		_ = os.Remove(over)
		t.Fatalf("the kernel accepted a %d-byte path", len(over))
	}
	// And the resolver never emits such a path for that directory.
	resolved, err := resolveCommandSocketPathIn(deep, deep, deep)
	if err != nil {
		t.Fatalf("resolver refused a layout it can satisfy: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolver produced %d bytes", len(resolved))
	}
}
