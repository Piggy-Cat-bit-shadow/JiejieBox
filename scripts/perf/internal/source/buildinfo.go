package source

// Binary provenance: the report must name the file that produced the number,
// not the tree that happened to be checked out. `go version -m` and
// debug/buildinfo read the Go build information embedded in the executable:
// module path, Go version, GOOS/GOARCH, CGO_ENABLED, -tags and every dependency
// with its replace target. This worktree cannot carry a VCS revision (its .git
// is a file and buildvcs requires a directory), so a missing vcs line is
// recorded as unavailable rather than guessed.

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

// InspectBinary reads the provenance embedded in binaryPath and hashes the file.
// A missing or non-Go file returns a Binary with ReadError set and no error: the
// caller decides whether that is fatal, and the record still says why it is empty.
func InspectBinary(binaryPath string) schema.Binary {
	binary := schema.Binary{Path: binaryPath}
	if sha, err := fileSHA256(binaryPath); err == nil {
		binary.SHA256 = sha
	} else {
		binary.ReadError = fmt.Sprintf("sha256: %v", err)
		return binary
	}
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		binary.ReadError = err.Error()
		return binary
	}
	decodeBuildInfo(info, &binary)
	return binary
}

// decodeBuildInfo fills the record from an already-read build info. It is
// separate so tests can drive every setting without building a binary.
func decodeBuildInfo(info *debug.BuildInfo, binary *schema.Binary) {
	binary.GoVersion = info.GoVersion
	binary.MainPath = info.Main.Path
	binary.MainVersion = info.Main.Version
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	binary.GOOS = settings["GOOS"]
	binary.GOARCH = settings["GOARCH"]
	if binary.GOOS == "" {
		binary.GOOS = runtime.GOOS
	}
	if binary.GOARCH == "" {
		binary.GOARCH = runtime.GOARCH
	}
	if value, loaded := settings["CGO_ENABLED"]; loaded {
		enabled := value == "1"
		binary.CGOEnabled = &enabled
	}
	if tags, loaded := settings["-tags"]; loaded && tags != "" {
		for _, tag := range strings.Split(tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				binary.BuildTags = append(binary.BuildTags, tag)
			}
		}
		binary.TagsSource = "buildinfo"
	}
	if revision := settings["vcs.revision"]; revision != "" {
		binary.VCSRevision = revision
		binary.VCSTime = settings["vcs.time"]
		if modified := settings["vcs.modified"]; modified != "" {
			value := modified == "true"
			binary.VCSModified = &value
		}
	}
	for _, dep := range info.Deps {
		pin := schema.ModulePin{Module: dep.Path, Version: dep.Version}
		if dep.Replace != nil {
			pin.Replace = dep.Replace.Path
			pin.ReplaceVersion = dep.Replace.Version
		}
		binary.Deps = append(binary.Deps, pin)
	}
}

// TagsSource resolves where the recorded build tags came from. BuildInfo wins;
// the sidecar is the fallback for a binary whose tags were stripped; otherwise
// the field is explicitly unavailable rather than an empty list that reads like
// "no tags were used".
func TagsSource(binary schema.Binary, sidecarTags []string) ([]string, string) {
	if len(binary.BuildTags) > 0 {
		return binary.BuildTags, "buildinfo"
	}
	if len(sidecarTags) > 0 {
		return sidecarTags, "sidecar"
	}
	return nil, "unavailable"
}

// FileSHA256 exposes the same hash used by InspectBinary so the runner can
// bracket a run with a before/after check on the measured executable.
func FileSHA256(path string) (string, error) {
	return fileSHA256(path)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
