package libbox

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
)

// commandSocketName is the documented socket name, used in the base path.
const commandSocketName = "command.sock"

// maxUnixSocketPath is the usable length of sockaddr_un.sun_path.
//
// Darwin declares `char sun_path[104]`, and the value passed to bind(2) is a C string, so 103
// bytes of path plus the terminating NUL is the most that can be used. A longer path does not
// produce a helpful error: bind returns EINVAL, which reads like a malformed address rather than
// "your directory name is too deep".
const maxUnixSocketPath = 103

// minHashDigits and maxHashDigits bound the generated name.
//
// The name carries no suffix on purpose. Every byte of the name is a byte the directory does not
// have: inside an iOS Packet Tunnel the temporary directory leaves ten bytes for a name, and
// ".sock" would consume five of them for a string nobody reads.
const (
	minHashDigits = 4
	maxHashDigits = 8
)

// commandSocketCandidates lists the directories a command socket may live in, best first.
//
// # What the device taught, three times
//
// The first version joined `command.sock` onto the base path: 138 bytes, EINVAL.
//
// The second moved the socket to the platform's temp path *instead*. The extension passes
// `FilePath.cacheDirectory`, which is `<shared>/Library/Caches` - deeper than the base path, and
// not the temporary directory at all. That produced a 110-byte fallback name.
//
// The third added the temp path and its parent to the candidate list, but used the temp path the
// caller passed: still `<shared>/Library/Caches` and its parent, both too deep. `os.TempDir()` -
// the one directory that is actually short inside the container, because TMPDIR points at
// `<container>/tmp` - was consulted only when the caller passed nothing, which the extension never
// does. The tunnel then failed with "no bindable socket path within 103 bytes".
//
// So the system temporary directory is a candidate ALWAYS, not as a fallback for a missing
// argument, and the container root is derived from it rather than from whatever the caller passed.
func commandSocketCandidates(basePath, tempDir, sysTemp string) []string {
	candidates := make([]string, 0, 6)
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
	// The system temporary directory first among the alternatives: inside an iOS Packet Tunnel it
	// is `<container>/tmp`, the shortest sanctioned writable place this process has.
	add(sysTemp)
	// Its parent is the container root, four bytes shorter.
	if sysTemp != "" {
		add(filepath.Dir(filepath.Clean(sysTemp)))
	}
	// Whatever the platform passed is still worth trying: on macOS it is a real temporary
	// directory, and on a correctly signed iOS build the base path wins before this matters.
	add(tempDir)
	if tempDir != "" {
		add(filepath.Dir(filepath.Clean(tempDir)))
	}
	return candidates
}

// hashedSocketName returns a socket file name that fits in budget bytes.
//
// budget is what the directory leaves after its trailing separator. The hash comes from the
// logical socket path, so two instances in one directory choose different names, and it is
// shortened only as far as the budget requires.
func hashedSocketName(logicalPath string, budget int) (string, bool) {
	digits := budget - 1 // the leading "h"
	if digits < minHashDigits {
		return "", false
	}
	if digits > maxHashDigits {
		digits = maxHashDigits
	}
	full := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(logicalPath)))
	// Fixed-width hex, so the kept suffix is the low bits - the part that differs between
	// container paths that differ at all.
	return "h" + full[len(full)-digits:], true
}

// pathFits reports whether path can be bound as a unix socket.
func pathFits(path string) bool {
	return len(path) <= maxUnixSocketPath
}

// resolveCommandSocketPath returns the socket path the command server binds and the command
// client connects to.
//
// The two sides run this same function with the same inputs, which is what keeps them in
// agreement without a second channel to agree over.
func resolveCommandSocketPath(basePath, tempDir string) (string, error) {
	return resolveCommandSocketPathIn(basePath, tempDir, os.TempDir())
}

// resolveCommandSocketPathIn is the testable form: the system temporary directory is an input, so
// a test can model a device whose TMPDIR is 92 bytes deep instead of asserting about the host's.
func resolveCommandSocketPathIn(basePath, tempDir, sysTemp string) (string, error) {
	candidates := commandSocketCandidates(basePath, tempDir, sysTemp)
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
			// The documented address, unchanged for a correctly signed build.
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
	// Nothing fit. Report the real budget of the shortest candidate - not of the last one - and
	// no container path, which is what ends up pasted into issue reports.
	shortest := candidates[0]
	for _, dir := range candidates[1:] {
		if len(dir) < len(shortest) {
			shortest = dir
		}
	}
	bestBudget := maxUnixSocketPath - len(shortest) - 1
	if bestBudget < 0 {
		bestBudget = 0
	}
	return "", fmt.Errorf(
		"command server: no bindable socket path within %d bytes; %d candidate directories, the shortest leaves %d bytes for a name",
		maxUnixSocketPath, len(candidates), bestBudget,
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
// documented command.sock. Cleanup removes only a name this package could have created.
func isGeneratedSocketName(name string) bool {
	if !strings.HasPrefix(name, "h") {
		return false
	}
	digits := name[1:]
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
