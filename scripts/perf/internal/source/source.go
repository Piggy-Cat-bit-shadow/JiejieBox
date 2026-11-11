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
			// A cache is a speed contract, not a truth claim: enrich it from the
			// binary and report when its commit is no longer the checked-out one.
			if head := git(repoDir, "rev-parse", "HEAD"); head != "" && fromEnv.Commit != "" && fromEnv.Commit != head {
				fromEnv.Warnings = append(fromEnv.Warnings,
					"source_cache_stale: cached commit "+fromEnv.Commit+" but HEAD is "+head)
			}
			return enrichSource(fromEnv, repoDir, binaryPath)
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
	return enrichSource(s, repoDir, binaryPath)
}

// enrichSource adds what the measured executable says about itself and compares
// it with the tree. Warnings are additive and never silently overwrite the
// tree-derived fields; Binary keeps the binary's own view.
func enrichSource(s schema.Source, repoDir, binaryPath string) schema.Source {
	if binaryPath == "" {
		return s
	}
	binary := InspectBinary(binaryPath)
	s.Binary = &binary
	s.BinarySHA256 = binary.SHA256
	if binary.ReadError != "" {
		s.Warnings = append(s.Warnings, "binary_unreadable: "+binary.ReadError)
		return s
	}
	sidecar := buildTags(binaryPath)
	tags, tagsSource := TagsSource(binary, sidecar)
	if len(binary.BuildTags) > 0 && len(sidecar) > 0 && !sameStrings(binary.BuildTags, sidecar) {
		s.Warnings = append(s.Warnings, "tag_sidecar_mismatch: buildinfo="+joinTags(binary.BuildTags)+" sidecar="+joinTags(sidecar))
	}
	if len(tags) > 0 {
		s.BuildTags = tags
	}
	binary.TagsSource = tagsSource
	if binary.GoVersion != "" {
		s.GoVersion = binary.GoVersion
	}
	if binary.CGOEnabled != nil {
		s.CGOEnabled = binary.CGOEnabled
	}
	if binary.VCSRevision == "" {
		s.Warnings = append(s.Warnings, "vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)")
	}
	if binary.MainPath != "" && binary.MainPath != "github.com/sagernet/sing-box" {
		s.Warnings = append(s.Warnings, "binary_main_module_mismatch: "+binary.MainPath)
	}
	for _, dep := range binary.Deps {
		treeVersion, tracked := s.Modules[dep.Module]
		if !tracked {
			continue
		}
		builtVersion := dep.Version
		if dep.ReplaceVersion != "" {
			builtVersion = dep.ReplaceVersion
		}
		if treeVersion != builtVersion {
			s.Warnings = append(s.Warnings,
				"module_pin_drift: "+dep.Module+" tree="+treeVersion+" binary="+builtVersion)
		}
	}
	return s
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}

func joinTags(tags []string) string {
	out := ""
	for index, tag := range tags {
		if index > 0 {
			out += ","
		}
		out += tag
	}
	return out
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
