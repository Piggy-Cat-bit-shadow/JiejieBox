package artifact

// F04 — versioned rule artifacts with leases.
//
// A rule artifact is downloaded, decompressed, validated and only then published. A half
// written file must never be visible under a valid name, a reader that is still using the old
// version must not have it deleted underneath it, and a crash in the middle must leave the
// last good version usable instead of a directory of plausible-looking garbage.
//
// # The contract
//
//   - COMMIT is temp-then-rename: the body is streamed to a temp file while it is hashed and
//     counted, the manifest is written beside it, and only a complete, matching pair is
//     renamed into place. A short read, a digest mismatch or a crash leaves a `.tmp` entry
//     that Collect removes; nothing under a version name is ever incomplete.
//   - The MANIFEST records what produced the artifact (schema, compiler, policy, ABI,
//     platform, dependencies) plus size and digest. Load rejects a manifest whose ABI/platform
//     do not match this process, and a digest that does not match the bytes.
//   - LEASES keep a version alive. Load returns a lease; Collect never removes a leased
//     version, so an mmap or a streaming reader that is still using the old artifact cannot
//     be pulled out from under it. The newest valid version is kept even when unleased, so a
//     crash recovery always has something to load.
//
// Platform note: on Windows an open file cannot be removed, so a version that is still open
// makes Collect's removal fail. That is not an error here: the lease is the authority, and a
// failed removal is retried by the next Collect rather than turned into a failed build.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	manifestName = "manifest.json"
	artifactName = "artifact.bin"
	tempPrefix   = ".tmp-"
)

// ErrIncomplete is returned when a version directory exists but is not a complete pair.
var ErrIncomplete = E.New("artifact: version is incomplete")

// ErrDigestMismatch is returned when the artifact bytes do not match the manifest.
var ErrDigestMismatch = E.New("artifact: digest mismatch")

// ErrIncompatible is returned when the manifest was produced for another ABI or platform.
var ErrIncompatible = E.New("artifact: manifest is not compatible with this build")

// Manifest says what produced an artifact and what it is safe to load into.
type Manifest struct {
	Schema       string   `json:"schema"`
	Compiler     string   `json:"compiler"`
	Policy       string   `json:"policy"`
	ABI          string   `json:"abi"`
	Platform     string   `json:"platform"`
	Dependencies []string `json:"dependencies,omitempty"`
	Size         int64    `json:"size"`
	Digest       string   `json:"digest"`
	Created      int64    `json:"created"`
}

// Version is one leased, valid artifact.
type Version struct {
	manifest Manifest
	dir      string
	store    *Store
	released bool
}

// Manifest returns the version's manifest.
func (v *Version) Manifest() Manifest { return v.manifest }

// Path is the artifact file.
func (v *Version) Path() string { return filepath.Join(v.dir, artifactName) }

// Open opens the artifact for reading while the lease is held.
func (v *Version) Open() (*os.File, error) { return os.Open(v.Path()) }

// Release drops the lease. It is idempotent so a deferred release plus an explicit one is safe.
func (v *Version) Release() {
	if v == nil || v.store == nil || v.released {
		return
	}
	v.released = true
	v.store.release(v.dir)
}

// Store is a directory of versioned artifacts.
type Store struct {
	access sync.Mutex
	dir    string
	lease  map[string]int
}

// Open prepares a store rooted at dir.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, E.New("artifact: empty directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, E.Cause(err, "create artifact store")
	}
	return &Store{dir: dir, lease: make(map[string]int)}, nil
}

// Commit streams body into a new version of key and publishes it atomically.
func (s *Store) Commit(key string, manifest Manifest, body io.Reader) (*Version, error) {
	if s == nil {
		return nil, E.New("artifact: nil store")
	}
	keyDir := filepath.Join(s.dir, key)
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		return nil, E.Cause(err, "create artifact key directory")
	}
	tempDir, err := os.MkdirTemp(keyDir, tempPrefix)
	if err != nil {
		return nil, E.Cause(err, "create artifact temp directory")
	}
	// A temp directory is never a version: on any error it is removed, and Collect also
	// removes leftovers from an earlier crash.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	artifactPath := filepath.Join(tempDir, artifactName)
	file, err := os.Create(artifactPath)
	if err != nil {
		return nil, E.Cause(err, "create artifact file")
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), body)
	closeErr := file.Close()
	if copyErr != nil {
		return nil, E.Cause(copyErr, "stream artifact")
	}
	if closeErr != nil {
		return nil, E.Cause(closeErr, "close artifact")
	}
	if manifest.Size != 0 && written != manifest.Size {
		return nil, E.New("artifact: body is ", written, " bytes, the manifest declares ", manifest.Size)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if manifest.Digest != "" && manifest.Digest != digest {
		return nil, E.Cause(ErrDigestMismatch, "artifact ", key)
	}
	manifest.Size = written
	manifest.Digest = digest
	if manifest.Created == 0 {
		manifest.Created = time.Now().UnixNano()
	}
	if err := writeManifest(filepath.Join(tempDir, manifestName), manifest); err != nil {
		return nil, err
	}

	// The version directory name is the digest prefix: two builds of the same bytes collapse
	// to one version instead of accumulating, and the name can never describe bytes it does
	// not contain.
	versionDir := filepath.Join(keyDir, "v-"+digest[:16])
	if err := publishDirectory(tempDir, versionDir); err != nil {
		return nil, err
	}
	cleanup = false
	// The returned handle is leased, exactly like a Load result: a commit that immediately
	// reads its own artifact (to index it, or to validate the published bytes) must not have
	// the next Collect remove it.
	s.acquire(versionDir)
	return &Version{manifest: manifest, dir: versionDir, store: s}, nil
}

// Load returns the newest valid version of key, leased to the caller.
func (s *Store) Load(key string) (*Version, error) {
	if s == nil {
		return nil, E.New("artifact: nil store")
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	type candidate struct {
		dir      string
		manifest Manifest
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), tempPrefix) {
			continue
		}
		full := filepath.Join(keyDir, entry.Name())
		manifest, readErr := readManifest(full)
		if readErr != nil {
			continue
		}
		if !compatible(manifest) {
			continue
		}
		if err := verifyArtifact(full, manifest); err != nil {
			continue
		}
		candidates = append(candidates, candidate{dir: full, manifest: manifest})
	}
	if len(candidates) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].manifest.Created > candidates[j].manifest.Created
	})
	chosen := candidates[0]
	s.acquire(chosen.dir)
	return &Version{manifest: chosen.manifest, dir: chosen.dir, store: s}, nil
}

// Collect removes temp leftovers and unleased versions of key, keeping the newest valid one.
// It is best-effort: a version that cannot be removed on this platform is retried later.
func (s *Store) Collect(key string) error {
	if s == nil {
		return nil
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	s.access.Lock()
	leased := make(map[string]bool, len(s.lease))
	for dir, count := range s.lease {
		if count > 0 {
			leased[dir] = true
		}
	}
	s.access.Unlock()

	type candidate struct {
		dir     string
		created int64
	}
	var valid []candidate
	for _, entry := range entries {
		full := filepath.Join(keyDir, entry.Name())
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			_ = os.RemoveAll(full)
			continue
		}
		manifest, readErr := readManifest(full)
		if readErr != nil {
			_ = os.RemoveAll(full)
			continue
		}
		valid = append(valid, candidate{dir: full, created: manifest.Created})
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].created > valid[j].created })
	for index, entry := range valid {
		if index == 0 || leased[entry.dir] {
			continue
		}
		_ = os.RemoveAll(entry.dir)
	}
	return nil
}

// acquire takes a lease on a version directory.
func (s *Store) acquire(dir string) {
	s.access.Lock()
	s.lease[dir]++
	s.access.Unlock()
}

// release drops a lease on a version directory.
func (s *Store) release(dir string) {
	s.access.Lock()
	if count := s.lease[dir]; count <= 1 {
		delete(s.lease, dir)
	} else {
		s.lease[dir] = count - 1
	}
	s.access.Unlock()
}

// writeManifest writes the manifest beside the artifact.
func writeManifest(path string, manifest Manifest) error {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return E.Cause(err, "encode artifact manifest")
	}
	return os.WriteFile(path, encoded, 0o644)
}

// readManifest reads and decodes a manifest.
func readManifest(dir string) (Manifest, error) {
	encoded, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return Manifest{}, ErrIncomplete
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return Manifest{}, E.Cause(err, "decode artifact manifest")
	}
	if manifest.Digest == "" || manifest.Size < 0 {
		return Manifest{}, ErrIncomplete
	}
	return manifest, nil
}

// verifyArtifact checks size and digest before a version is offered.
func verifyArtifact(dir string, manifest Manifest) error {
	file, err := os.Open(filepath.Join(dir, artifactName))
	if err != nil {
		return ErrIncomplete
	}
	defer file.Close()
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return ErrIncomplete
	}
	if written != manifest.Size {
		return ErrIncomplete
	}
	if hex.EncodeToString(hasher.Sum(nil)) != manifest.Digest {
		return ErrDigestMismatch
	}
	return nil
}

// compatible reports whether the manifest can be loaded by this build.
func compatible(manifest Manifest) bool {
	if manifest.Platform != "" && manifest.Platform != currentPlatform() {
		return false
	}
	if manifest.ABI != "" && manifest.ABI != currentABI() {
		return false
	}
	return true
}

// publishDirectory renames a complete temp directory into its version name.
func publishDirectory(tempDir string, versionDir string) error {
	if _, err := os.Stat(versionDir); err == nil {
		// The identical artifact is already published: the temp copy is redundant.
		return os.RemoveAll(tempDir)
	}
	if err := os.Rename(tempDir, versionDir); err != nil {
		return E.Cause(err, "publish artifact version")
	}
	return nil
}
