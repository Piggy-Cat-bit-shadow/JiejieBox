// Package source records what produced a number: the commit, the toolchain, the
// build tags and the dependency pins that are specific to this fork.
package source

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

// interestingModules are the pins that change protocol behaviour or performance
// in this fork. A full go.mod dump would be noise; these are the ones a
// cross-run comparison must match.
var interestingModules = []string{
	"github.com/sagernet/sing ",
	"github.com/sagernet/sing-quic",
	"github.com/sagernet/quic-go",
	"github.com/sagernet/sing-tun",
	"github.com/sagernet/sing-mux",
	"github.com/sagernet/cronet-go ",
	"github.com/enfein/mieru",
	"github.com/sagernet/sing-anytls",
	"github.com/sagernet/wireguard-go",
	"github.com/sagernet/sing-trusttunnel",
	"github.com/sagernet/sing-vmess",
	"github.com/sagernet/sing-shadowsocks",
}

// Collect inspects repoDir and binaryPath. Every command is read-only.
//
// When PERF_SOURCE_JSON is set, that record is used verbatim instead of running
// git. On a WSL2 drvfs mount a single `git status` of this repository costs
// about nine seconds and a `git log -1` about six; a matrix of twelve cells
// would spend two minutes re-reading the same commit.
func Collect(repoDir, binaryPath string) schema.Source {
	if cached := os.Getenv("PERF_SOURCE_JSON"); cached != "" {
		var fromEnv schema.Source
		if err := json.Unmarshal([]byte(cached), &fromEnv); err == nil {
			if binaryPath != "" {
				if tags := buildTags(binaryPath); len(tags) > 0 {
					fromEnv.BuildTags = tags
				}
			}
			return fromEnv
		}
	}
	s := schema.Source{Repo: repoDir}
	if repoDir != "" {
		s.Branch = git(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
		s.Commit = git(repoDir, "rev-parse", "HEAD")
		if logLine := git(repoDir, "log", "-1", "--format=%cI%x00%s"); logLine != "" {
			if when, subject, ok := strings.Cut(logLine, "\x00"); ok {
				s.CommitTime = when
				s.CommitSubject = subject
			}
		}
		if status := git(repoDir, "status", "--porcelain"); status != "" {
			dirty := true
			s.Dirty = &dirty
			for _, line := range strings.Split(status, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if len(s.DirtyFiles) < 40 {
					s.DirtyFiles = append(s.DirtyFiles, line)
				}
			}
		} else {
			clean := false
			s.Dirty = &clean
		}
	}
	s.GoVersion = goVersion()
	s.BuildTags = buildTags(binaryPath)
	cgo := os.Getenv("CGO_ENABLED")
	if cgo == "" {
		// The build environment default; recorded as unknown rather than guessed.
		s.CGOEnabled = nil
	} else {
		enabled := cgo == "1"
		s.CGOEnabled = &enabled
	}
	s.Modules = modulePins(filepath.Join(repoDir, "go.mod"))
	return s
}

func goVersion() string {
	if out, err := exec.Command("go", "version").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return runtime.Version()
}

// buildTags reads the sidecar the build wrote next to the binary, so the record
// names the tags the binary was actually built with rather than the tags the
// scenario hoped for.
func buildTags(binaryPath string) []string {
	if binaryPath == "" {
		return nil
	}
	raw, err := os.ReadFile(binaryPath + ".tags")
	if err != nil {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(strings.TrimSpace(string(raw)), ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func git(repoDir string, args ...string) string {
	// safe.directory: the WSL2 drvfs mount reports the Windows owner, so git
	// refuses to read the repository without this. The operations here are
	// read-only.
	cmd := exec.Command("git", append(gitArgs(repoDir), args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitArgs addresses a linked worktree whose .git file carries a Windows path.
// Git for Windows writes "C:/...", which is meaningless to the WSL2 git that
// the laboratory runs under; translating it here keeps one checkout usable from
// both sides without rewriting the file.
func gitArgs(repoDir string) []string {
	args := []string{"-c", "safe.directory=*"}
	if gitDir := linkedGitDir(repoDir); gitDir != "" {
		return append(args, "--git-dir", gitDir, "--work-tree", repoDir)
	}
	return append(args, "-C", repoDir)
}

func linkedGitDir(repoDir string) string {
	raw, err := os.ReadFile(filepath.Join(repoDir, ".git"))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(text, "gitdir:") {
		return ""
	}
	path := strings.TrimSpace(strings.TrimPrefix(text, "gitdir:"))
	path = strings.ReplaceAll(path, "\\", "/")
	if len(path) > 2 && path[1] == ':' {
		drive := strings.ToLower(string(path[0]))
		return "/mnt/" + drive + "/" + strings.TrimPrefix(path[2:], "/")
	}
	return path
}

func modulePins(goModPath string) map[string]string {
	raw, err := os.ReadFile(goModPath)
	if err != nil {
		return nil
	}
	requires := make(map[string]string)
	replacements := make(map[string]string)
	inRequireBlock := false
	inReplaceBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "module") || strings.HasPrefix(line, "go ") {
			continue
		}
		switch {
		case line == "require (":
			inRequireBlock = true
			continue
		case line == "replace (":
			inReplaceBlock = true
			continue
		case line == ")":
			inRequireBlock = false
			inReplaceBlock = false
			continue
		case inRequireBlock:
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				requires[fields[0]] = fields[1]
			}
			continue
		case inReplaceBlock:
			recordReplacement(replacements, line)
			continue
		case strings.HasPrefix(line, "replace "):
			recordReplacement(replacements, strings.TrimPrefix(line, "replace "))
			continue
		case strings.HasPrefix(line, "require "):
			fields := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(fields) >= 2 {
				requires[fields[0]] = fields[1]
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "github.com/") {
			requires[fields[0]] = fields[1]
		}
	}
	pins := make(map[string]string)
	for _, needle := range interestingModules {
		key := strings.TrimSpace(needle)
		if version, loaded := requires[key]; loaded {
			pins[key] = version
		}
		if version, loaded := replacements[key]; loaded {
			// A replace directive is what the build actually used; recording the
			// require version here would name a module that never ran.
			pins[key] = version
		}
	}
	return pins
}

// recordReplacement parses one replace directive body: `old => new version` or
// `old version => new version`. A local directory replacement has no version, and
// the path itself is the only truthful pin.
func recordReplacement(replacements map[string]string, line string) {
	parts := strings.SplitN(line, "=>", 2)
	if len(parts) != 2 {
		return
	}
	left := strings.Fields(parts[0])
	right := strings.Fields(parts[1])
	if len(left) == 0 || len(right) == 0 {
		return
	}
	target := right[0]
	if len(right) >= 2 && strings.HasPrefix(right[len(right)-1], "v") {
		target = right[len(right)-1]
	}
	replacements[left[0]] = target
}
