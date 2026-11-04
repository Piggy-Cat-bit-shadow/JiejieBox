//go:build darwin

package libbox

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// These tests run the kernel's own check. The unit tests above prove the resolver's arithmetic;
// only bind(2) can prove the path is acceptable to Darwin, and only on a Darwin host.
//
// They are the reason this package no longer claims a fix from a test machine's short temp
// directory: the fixture below builds a container as deep as the one on the device and asks the
// kernel what it thinks.

func TestDarwinBindsTheResolvedPathInADeepContainer(t *testing.T) {
	root := t.TempDir()
	// Reproduce the shape: <root>/Data/PluginKitPlugin/<UUID>/tmp, then put the extension's
	// base path under it. The absolute length is whatever t.TempDir() gives us plus this, which
	// is why the resolver - not this test - decides where the socket goes.
	plugin := filepath.Join(root, "Data", "PluginKitPlugin", "5B6182FE-3CD7-4330-A11D-6336B908C07A")
	tempDir := filepath.Join(plugin, "tmp")
	basePath := filepath.Join(plugin, "Library", "Application Support", "JiejieBox-Shared")
	for _, dir := range []string{tempDir, basePath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	resolved, err := resolveCommandSocketPath(basePath, tempDir)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	// The socket may resolve into the base path (short fixture) or a shorter ancestor; what
	// matters is that the kernel accepts it.
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
	// The control for the test above: a 104-byte name must fail at bind, and the resolver must
	// not produce one. This is what "the kernel allows 103" means in bytes.
	root := t.TempDir()
	deep := filepath.Join(root, "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Build a name that makes the full path exactly limit+1 bytes.
	pad := maxUnixSocketPath + 1 - len(deep) - 1
	if pad <= 0 {
		t.Skip("t.TempDir() is already too deep to build the fixture")
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
	// And the resolver, given that directory, must choose a shorter name rather than emit this.
	resolved, err := resolveCommandSocketPath(deep, deep)
	if err != nil {
		t.Fatalf("resolver refused a layout it can satisfy: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolver produced %d bytes", len(resolved))
	}
}
