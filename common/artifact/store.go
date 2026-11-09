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
//   - The lease is taken BEFORE Load reads the manifest and verifies the bytes, and Collect
//     checks and removes under the same lock. That closes the window in which a collector had
//     already decided to remove a version a Load was about to return. A live temp directory is
//     registered while Commit streams into it, so Collect removes only crash leftovers.
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

// NewManifest builds a manifest for this build. The platform and ABI are filled from the running
// process, so a version produced here is only considered loadable by the same platform/ABI; the
// caller supplies what produced the bytes (schema, compiler, policy) and their provenance.
func NewManifest(schema string, compiler string, policy string, dependencies ...string) Manifest {
	return Manifest{
		Schema:       schema,
		Compiler:     compiler,
		Policy:       policy,
		ABI:          currentABI(),
		Platform:     currentPlatform(),
		Dependencies: append([]string(nil), dependencies...),
	}
}

// Manifest returns the version's manifest.
func (v *Version) Manifest() Manifest { return v.manifest }

// ID is the exact version reference: the digest of the artifact bytes. It is stable across
// processes and restarts, unlike the directory name (a prefix of it), and is what a caller stores
// when it has to name the version it used.
func (v *Version) ID() string { return v.manifest.Digest }

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
	dir   string
	lease *leaseTable
	// testHook, when non-nil, is called at named points in Commit and Collect. Tests in this
	// package use it to place a reader or a collector exactly inside the window they are about to
	// check; production leaves it nil.
	testHook func(point string)
}

// leaseTable holds the leases for one store directory, shared by every Store value opened on that
// directory IN THIS PROCESS. It is shared because a lease is a statement about a reader, not about
// the handle it was taken through: two stores on one directory are two handles to the same
// versions, and a collector in one used to remove a version the other was still reading.
//
// # The boundary, stated
//
// Leases are process-local. Two PROCESSES on one directory are not protected from each other: a
// collector in one can remove a version the other holds. The contract for that case is ONE writer
// per directory (single-writer isolation), which is why the production consumer opens its store
// from the process that also collects. Cross-process leases would need a cooperative lock file and
// stale-lease recovery, which is a larger contract than the readers need today - and pretending to
// have it with magic files would be worse than stating the boundary.
type leaseTable struct {
	access sync.Mutex
	counts map[string]int
	// writing holds the temp directories a Commit in this process is still streaming into.
	// Collect removes temp leftovers from a crash, and a live temp directory looks exactly like
	// one; the registration is what tells them apart. Creation and registration happen under
	// access, so a collector that lists the directory cannot see a temp directory that is not
	// registered yet.
	writing map[string]bool
}

var (
	leaseTablesAccess sync.Mutex
	leaseTables       = map[string]*leaseTable{}
)

// leaseTableFor returns the one lease table for a directory path.
func leaseTableFor(directory string) *leaseTable {
	leaseTablesAccess.Lock()
	defer leaseTablesAccess.Unlock()
	table, loaded := leaseTables[directory]
	if !loaded {
		table = &leaseTable{counts: make(map[string]int), writing: make(map[string]bool)}
		leaseTables[directory] = table
	}
	return table
}

// Open prepares a store rooted at dir.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, E.New("artifact: empty directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, E.Cause(err, "create artifact store")
	}
	// The lease table is keyed by the canonical path: two handles opened as "cache" and
	// "C:\cache" are one directory, and their leases must be one table.
	canonical, err := filepath.Abs(dir)
	if err != nil {
		canonical = filepath.Clean(dir)
	}
	return &Store{dir: canonical, lease: leaseTableFor(canonical)}, nil
}

// Commit streams body into a new version of key and publishes it atomically.
func (s *Store) Commit(key string, manifest Manifest, body io.Reader) (*Version, error) {
	staged, err := s.Stage(key, manifest, body, 0)
	if err != nil {
		return nil, err
	}
	return staged.Publish()
}

// Load returns the newest valid version of key, leased to the caller.
//
// The lease is taken BEFORE the version is read, not after it was found loadable: a collector
// that listed the directory before the lease existed can otherwise remove the version between
// "the manifest and digest check out" and "the caller holds it", and a successful Load would
// return a path that is already gone. A version removed in that window fails its check instead,
// and the listing is retried so a version published concurrently is still found.
func (s *Store) Load(key string) (*Version, error) {
	if s == nil {
		return nil, E.New("artifact: nil store")
	}
	for attempt := 0; attempt < 3; attempt++ {
		version, vanished, err := s.loadOnce(key)
		if err == nil {
			return version, nil
		}
		if !errors.Is(err, os.ErrNotExist) || !vanished {
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

// LoadExact returns the version of key whose content digest is id, leased to the caller.
//
// This is the restart-after-pinning API: a recovery record names the digest it used, and a later
// process must get that exact version back rather than whatever happens to be newest now.
func (s *Store) LoadExact(key string, id string) (*Version, error) {
	if s == nil {
		return nil, E.New("artifact: nil store")
	}
	if id == "" {
		return nil, E.New("artifact: empty version id")
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), tempPrefix) {
			continue
		}
		full := filepath.Join(keyDir, entry.Name())
		s.acquire(full)
		manifest, readErr := readManifest(full)
		if readErr != nil || manifest.Digest != id || !compatible(manifest) {
			s.release(full)
			continue
		}
		if verifyErr := verifyArtifact(full, manifest); verifyErr != nil {
			s.release(full)
			continue
		}
		return &Version{manifest: manifest, dir: full, store: s}, nil
	}
	return nil, os.ErrNotExist
}

// loadOnce is one listing pass. `vanished` reports that a version this pass had listed was
// removed before its lease could be taken, which makes another pass worth doing.
func (s *Store) loadOnce(key string) (version *Version, vanished bool, err error) {
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, os.ErrNotExist
		}
		return nil, false, err
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
		// The lease comes first. Everything below runs while this version cannot be collected.
		s.acquire(full)
		manifest, readErr := readManifest(full)
		if readErr != nil {
			if _, statErr := os.Stat(full); errors.Is(statErr, os.ErrNotExist) {
				vanished = true
			}
			s.release(full)
			continue
		}
		if !compatible(manifest) {
			s.release(full)
			continue
		}
		if err := verifyArtifact(full, manifest); err != nil {
			if _, statErr := os.Stat(full); errors.Is(statErr, os.ErrNotExist) {
				vanished = true
			}
			s.release(full)
			continue
		}
		candidates = append(candidates, candidate{dir: full, manifest: manifest})
	}
	if len(candidates) == 0 {
		return nil, vanished, os.ErrNotExist
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].manifest.Created > candidates[j].manifest.Created
	})
	chosen := candidates[0]
	// The rejected candidates were leased only for the duration of their check.
	for _, rejected := range candidates[1:] {
		s.release(rejected.dir)
	}
	return &Version{manifest: chosen.manifest, dir: chosen.dir, store: s}, false, nil
}

// Collect removes temp leftovers and unleased versions of key, keeping the newest version this
// build can actually load and any version named in retained (an exact reference a recovery record
// still points at).
//
// "Can actually load" is the predicate Load uses, not a weaker one: the manifest must read, the
// artifact must match the size and digest the manifest declares, and the manifest must be
// compatible with this build. A collector that measured validity with the manifest alone kept a
// corrupted newest version and deleted the last one that loads - the corruption destroyed the
// fallback instead of being removed - and it let another build's newer version evict the one this
// build needs.
func (s *Store) Collect(key string, retained ...string) error {
	if s == nil {
		return nil
	}
	retainedIDs := make(map[string]bool, len(retained))
	for _, id := range retained {
		if id != "" {
			retainedIDs[id] = true
		}
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// The listing is the collector's view of the versions; the hook lets a test place a reader or
	// a writer inside this pass.
	if s.testHook != nil {
		s.testHook("collect-listed")
	}

	type candidate struct {
		dir     string
		created int64
		digest  string
	}
	var valid []candidate
	for _, entry := range entries {
		full := filepath.Join(keyDir, entry.Name())
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			// A temp directory is garbage only when no commit is still writing it. A live one
			// looks exactly like a crash leftover, and removing it failed the commit that was
			// using it; the registration is what tells them apart.
			s.lease.access.Lock()
			writing := s.lease.writing[full]
			s.lease.access.Unlock()
			if !writing {
				_ = os.RemoveAll(full)
			}
			continue
		}
		manifest, readErr := readManifest(full)
		if readErr != nil {
			s.removeIfUnleased(full)
			continue
		}
		if verifyErr := verifyArtifact(full, manifest); verifyErr != nil {
			// The bytes are not the ones the manifest names, so no build can load this version.
			// Removing it is what makes the LAST valid version the one that is kept.
			s.removeIfUnleased(full)
			continue
		}
		if !compatible(manifest) {
			// Another build's version: it may be the one that build loads. It is neither a reason
			// to evict ours nor this build's to delete.
			continue
		}
		// A leased version still takes part in the ranking: "newest" is a property of the
		// versions, not of who happens to be reading them. It is only protected from removal.
		valid = append(valid, candidate{dir: full, created: manifest.Created, digest: manifest.Digest})
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].created > valid[j].created })
	for index, entry := range valid {
		if index == 0 || retainedIDs[entry.digest] {
			continue
		}
		s.removeIfUnleased(entry.dir)
	}
	return nil
}

// removeIfUnleased deletes a version directory only when no reader holds it.
//
// The check and the removal happen under the lease lock together: a reader that acquires the
// lease between them would otherwise be handed a version this collector is deleting. The cost is
// that a Load blocks for the duration of one version's removal, which is a directory with two
// small files - not a copy.
func (s *Store) removeIfUnleased(dir string) {
	s.lease.access.Lock()
	defer s.lease.access.Unlock()
	if s.lease.counts[dir] > 0 {
		return
	}
	_ = os.RemoveAll(dir)
}

// acquire takes a lease on a version directory.
func (s *Store) acquire(dir string) {
	s.lease.access.Lock()
	s.lease.counts[dir]++
	s.lease.access.Unlock()
}

// release drops a lease on a version directory.
func (s *Store) release(dir string) {
	s.lease.access.Lock()
	if count := s.lease.counts[dir]; count <= 1 {
		delete(s.lease.counts, dir)
	} else {
		s.lease.counts[dir] = count - 1
	}
	s.lease.access.Unlock()
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

// publishVersion renames a complete temp directory into its version name and takes the commit's
// own lease on it, under the lease lock.
//
// The rename and the lease are one step because the commit returns a handle to the caller: a
// collector that listed the key directory between the rename and the lease would see a new,
// unleased version and remove it, and Commit would return a version that no longer exists.
func (s *Store) publishVersion(tempDir string, versionDir string) error {
	s.lease.access.Lock()
	defer s.lease.access.Unlock()
	if _, err := os.Stat(versionDir); err == nil {
		// The identical artifact is already published: the temp copy is redundant, and the
		// existing version is the one this commit leases.
		if err := os.RemoveAll(tempDir); err != nil {
			return err
		}
		s.lease.counts[versionDir]++
		return nil
	}
	if err := os.Rename(tempDir, versionDir); err != nil {
		return E.Cause(err, "publish artifact version")
	}
	s.lease.counts[versionDir]++
	return nil
}
