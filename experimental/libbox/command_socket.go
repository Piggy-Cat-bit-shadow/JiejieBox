package libbox

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
)

// commandSocketName is the file name the command server binds inside its base path.
const commandSocketName = "command.sock"

// maxUnixSocketPath is the usable length of sockaddr_un.sun_path.
//
// Darwin declares `char sun_path[104]`, and the value passed to bind(2) is a C string, so 103
// bytes of path plus the terminating NUL is the most that can be used. A longer path does not
// produce a helpful error: bind returns EINVAL, which reads like a malformed address rather than
// "your directory name is too deep".
const maxUnixSocketPath = 103

// socketNameSuffix is what a fallback name ends with; the leading "h" plus hex characters is the
// instance hash.
const socketNameSuffix = ".sock"

// minHashDigits is the shortest hash worth generating: 4 hex digits is 65536 distinct names, and
// the number of command servers in one container is one per extension instance.
const minHashDigits = 4

// maxHashDigits caps the hash so a path budget cannot produce a pointlessly long name.
const maxHashDigits = 8

// commandSocketCandidates lists the directories a command socket may live in, best first.
//
// # Why the ladder exists
//
// The first fix for this problem assumed the process temporary directory is short. On a
// re-signed iOS build without its App Group that assumption is false: the Packet Tunnel runs in
//
//	/private/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/tmp
//
// which is 92 bytes before a file name is appended, so even an eight-character hashed name does
// not fit and the tunnel died with "socket path is 110 bytes, the kernel allows 103".
//
// A length limit cannot be satisfied by choosing one alternative directory and hoping. The
// directories this process can write to have a known order from shortest to longest, and the NAME
// must be sized to whatever budget the directory leaves. So both are inputs to one decision.
//
// The base path comes first and keeps its original file name: a correctly signed build never
// leaves the first rung, and its socket address does not change.
func commandSocketCandidates(basePath, tempDir string) []string {
	candidates := make([]string, 0, 4)
	add := func(dir string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		for _, existing := range candidates {
			if existing == clean {
				return
			}
		}
		candidates = append(candidates, clean)
	}
	add(basePath)
	if tempDir != "" {
		add(tempDir)
	} else {
		add(os.TempDir())
	}
	// The container root: four bytes shorter than its tmp directory on iOS, and still inside the
	// sandbox. It is what makes the real 92-byte tmp path survivable.
	if len(candidates) > 1 {
		add(filepath.Dir(candidates[1]))
	}
	return candidates
}

// hashedSocketName returns a socket file name that fits in budget bytes.
//
// budget is what the directory leaves after its trailing separator. The hash is derived from the
// logical socket path so two instances in the same directory choose different names, and it is
// shortened only as far as the budget requires - a shorter hash is a worse name, never a better
// one. ok is false when not even the minimum name fits.
func hashedSocketName(logicalPath string, budget int) (name string, ok bool) {
	nameOverhead := len("h") + len(socketNameSuffix)
	digits := budget - nameOverhead
	if digits < minHashDigits {
		return "", false
	}
	if digits > maxHashDigits {
		digits = maxHashDigits
	}
	full := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(logicalPath)))
	// A fixed-width hex string, so the truncated suffix keeps the low bits - the part that
	// differs between container paths that differ at all.
	return "h" + full[len(full)-digits:] + socketNameSuffix, true
}

// pathFits reports whether path can be bound as a unix socket.
func pathFits(path string) bool {
	return len(path) <= maxUnixSocketPath
}

// resolveCommandSocketPath returns the socket path the command server binds and the command
// client connects to.
//
// The two sides run this same function with the same two inputs, which is what keeps them in
// agreement without a second channel to agree over.
func resolveCommandSocketPath(basePath, tempDir string) (string, error) {
	candidates := commandSocketCandidates(basePath, tempDir)
	if len(candidates) == 0 {
		return "", fmt.Errorf("command server: no directory to bind a socket in")
	}
	logicalPath := filepath.Join(basePath, commandSocketName)
	for index, dir := range candidates {
		budget := maxUnixSocketPath - len(dir) - 1
		if budget <= 0 {
			continue
		}
		if index == 0 && budget >= len(commandSocketName) {
			// The normal case, and the only one that keeps the documented address.
			path := filepath.Join(dir, commandSocketName)
			if pathFits(path) {
				return path, nil
			}
			continue
		}
		name, ok := hashedSocketName(logicalPath, budget)
		if !ok {
			continue
		}
		path := filepath.Join(dir, name)
		if pathFits(path) {
			return path, nil
		}
	}
	// Nothing fit. Report sizes, not the private container path: the full path identifies the
	// container and ends up in logs the user pastes into issues.
	shortest := candidates[len(candidates)-1]
	return "", fmt.Errorf(
		"command server: no bindable socket path within %d bytes; %d candidate directories, the shortest leaves %d bytes for a name",
		maxUnixSocketPath, len(candidates), maxUnixSocketPath-len(shortest)-1,
	)
}

// socketPathDiagnostics names a socket path without disclosing its container, for logs that must
// stay safe to paste: only the final element and the directory length are included.
func socketPathDiagnostics(path string) string {
	if path == "" {
		return "none"
	}
	return fmt.Sprintf("%s in a %d-byte directory", filepath.Base(path), len(filepath.Dir(path)))
}

// isGeneratedSocketName reports whether a name is one this package allocates, as opposed to the
// documented command.sock. Used by cleanup to decide whether removing a stale file is safe: only
// a name this package could have created is removed.
func isGeneratedSocketName(name string) bool {
	if !strings.HasPrefix(name, "h") || !strings.HasSuffix(name, socketNameSuffix) {
		return false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, "h"), socketNameSuffix)
	if len(digits) < minHashDigits || len(digits) > maxHashDigits {
		return false
	}
	for _, r := range digits {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
