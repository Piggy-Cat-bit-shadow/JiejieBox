package libbox

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// commandSocketName is the file name the command server binds inside its base path.
const commandSocketName = "command.sock"

// maxUnixSocketPath is the usable length of sockaddr_un.sun_path.
//
// Darwin declares `char sun_path[104]`, and the value passed to bind(2) is a C string, so 103
// bytes of path plus the terminating NUL is the most that can be used. A longer path does not
// produce a helpful error: bind returns EINVAL, which reads like a malformed address rather than
// "your directory name is too deep". iOS Packet Tunnel containers are deep enough to exceed it:
//
//	/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/Library/Application Support/JiejieBox-Shared/command.sock
//
// is about 138 bytes. That path is what the client falls back to when a re-signed build cannot
// open its App Group container, and it is exactly the failure the user reported: the tunnel
// started, then died in `listen command server`.
const maxUnixSocketPath = 103

// resolveCommandSocketPath returns the path the command server should bind, and the command
// client should connect to, for a base path.
//
// It is the base path's `command.sock` whenever that fits in sockaddr_un. When it does not, the
// socket is placed under the process temporary directory with a deterministic name derived from
// the full path. Deterministic because the server and the client run the same rule and must agree
// without talking to each other - the socket is how they would talk.
//
// # What this does not solve, and does not pretend to
//
// The fallback directory exists because the App Group is unavailable. In that state the app and
// the Packet Tunnel run in *different* containers, so a socket in the extension's temporary
// directory is reachable only from the extension itself: the tunnel starts and the VPN works,
// but the app's stats and commands cannot connect to it. That is strictly better than a tunnel
// that cannot start at all, and it is reported separately (the app logs
// `FilePath.isUsingProcessLocalDirectory`); a genuinely shared command channel needs a
// provisioning profile that grants the App Group.
func resolveCommandSocketPath(basePath string) string {
	path := filepath.Join(basePath, commandSocketName)
	if len(path) <= maxUnixSocketPath {
		return path
	}
	// Not a truncation: a truncated path could collide with a different directory, and the
	// directory it names would not exist. A hashed name under the temp directory is short,
	// unique to this base path, and inside a directory this process owns.
	shortPath := filepath.Join(os.TempDir(), fmt.Sprintf("jjb-%08x.sock", crc32.ChecksumIEEE([]byte(path))))
	if len(shortPath) <= maxUnixSocketPath {
		return shortPath
	}
	// Even the temporary directory is too deep. Returning the long path would turn this into an
	// EINVAL at bind time with no explanation, so the caller gets a path it will fail on with a
	// message that names the real problem.
	return shortPath
}

// commandSocketPathTooTall reports whether a resolved path still cannot be bound, so the server
// can say why instead of surfacing EINVAL.
func commandSocketPathTooLong(path string) bool {
	return len(path) > maxUnixSocketPath
}
