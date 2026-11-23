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
//   - REUSE OF AN EXISTING VERSION NAME is verified, not assumed. A name is derived from the
//     digest of the bytes, so a directory that exists is not evidence that it still HOLDS those
//     bytes: a half-written file, a bad manifest, a missing file or a rewrite by another tool can
//     leave a name that describes one artifact around another. Committing the verified bytes again
//     therefore re-checks the directory it is about to reuse, and repairs it when it is not the
//     artifact its name claims. A damaged version that a reader still holds is never removed or
//     rewritten underneath that reader; the verified bytes are published beside it instead, and the
//     damaged one is collected when its lease drops.
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

// ErrVersionBusy is returned when a version directory holds bytes that are not the artifact its
// manifest names, and the directory cannot be replaced without disturbing whatever still has it
// open. The commit is refused instead of silently reusing the damaged version; nothing is
// rewritten underneath a reader that a lease does not cover.
var ErrVersionBusy = E.New("artifact: damaged version is still in use")

// repairSequence names the quarantine directories and repaired siblings of one process. It is not
// a version name and never becomes loadable; a unique suffix only keeps two repairs of one name
// from colliding.
var repairSequence atomic.Uint64

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
	// Published marks Created as the caller's AUTHORITATIVE ordering value even when the identical
	// bytes are already published. Ordinarily Created is a publication timestamp: republishing
	// unchanged bytes keeps the first account (see sameProvenance), because the timestamp is not
	// provenance. A producer whose order is a logical clock (the derived rule-set cache, which must
	// outrank a future-dated or clock-skewed version) is not claiming when the conversion happened;
	// it is claiming where this version must sort, so its new stamp has to replace the old one.
	// It is not serialized: it is an instruction to this publication, not part of what the artifact is.
	Published bool `json:"-"`
}

// Version is one leased, valid artifact.
type Version struct {
	manifest Manifest
	dir      string
	store    *Store
	// released is the one-shot lease release latch. It is atomic because the comment on Release
	// promises idempotence for a deferred plus an explicit call, and two owners may reach those two
	// calls concurrently; a plain bool would race and could decrement the lease twice.
	released atomic.Bool
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

// Release drops the lease. It is idempotent so a deferred release plus an explicit one is safe, and
// the latch is atomic so the two may also be concurrent.
func (v *Version) Release() {
	if v == nil || v.store == nil || !v.released.CompareAndSwap(false, true) {
		return
	}
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

// NewestCreated returns the largest Created value among the versions of key that this build could
// load, or 0 when the key has none.
//
// It reads manifests only and takes no lease, so it is cheap enough to be called on a publication
// path. A producer whose ordering is a logical clock (rather than a claim about wall time) uses it to
// publish strictly after everything already on disk: Load ranks versions by Created, so a version
// published at wall time after a backward clock correction - or one that has to outrank a stale
// version whose timestamp is ahead of the clock - would otherwise never become the newest one and
// would be collected as the non-newest version, leaving the cache permanently cold.
func (s *Store) NewestCreated(key string) int64 {
	if s == nil {
		return 0
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		return 0
	}
	newest := int64(0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), tempPrefix) {
			continue
		}
		manifest, readErr := readManifest(filepath.Join(keyDir, entry.Name()))
		if readErr != nil || !compatible(manifest) {
			continue
		}
		if manifest.Created > newest {
			newest = manifest.Created
		}
	}
	return newest
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
		return newerView(candidates[i].manifest.Created, candidates[i].dir, candidates[j].manifest.Created, candidates[j].dir)
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
	// The ranking must be the SAME one Load uses, or a collector can remove the version a load
	// would have chosen. Two versions published in the same clock tick are ordered by directory
	// name instead of by the sort's arbitrary tie-break.
	sort.Slice(valid, func(i, j int) bool {
		return newerView(valid[i].created, valid[i].dir, valid[j].created, valid[j].dir)
	})
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

// newerView reports whether the left version outranks the right one.
//
// Created is the ordering value (a publication or logical stamp), and Load and Collect must rank
// with the SAME rule: a collector whose ranking differed could remove the version a load would have
// chosen. Two versions can be published inside one clock tick, so the tie is broken by directory
// name - arbitrary as a statement about time, but identical in both passes, which is what has to
// hold.
func newerView(leftCreated int64, leftDir string, rightCreated int64, rightDir string) bool {
	if leftCreated != rightCreated {
		return leftCreated > rightCreated
	}
	return leftDir < rightDir
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

// versionSlot is what a version name already holds.
//
// The three answers that matter are deliberately separate: a name can be EMPTY, it can hold exactly
// the artifact this commit verified, or it can hold something else. The last case is not one thing
// either - it can be bytes no build can load (a crash, a truncated download, a rewrite by a sync
// tool), or a complete artifact of another identity that happens to be under this name - and the
// repair differs. "The directory exists" is never treated as any of these.
type versionSlot struct {
	// exists reports that something is at the path.
	exists bool
	// manifest is the account found there, when it could be read.
	manifest Manifest
	// valid reports that the directory holds a complete artifact whose bytes match its own
	// manifest, which is what Load would call loadable.
	valid bool
	// reusable reports that those bytes ARE this commit's bytes: the directory can be reused as
	// this version without touching the artifact file.
	reusable bool
}

// inspectVersionSlot verifies what is published under a version name against the bytes this commit
// has just staged and hashed.
//
// The verification is the same one Load performs - read the manifest, hash the artifact, compare
// size and digest - because a commit that reuses a directory is making exactly Load's claim about
// it. A name is a weak statement (a prefix of a digest); the bytes are the strong one.
func inspectVersionSlot(dir string, want Manifest) versionSlot {
	info, err := os.Stat(dir)
	if err != nil {
		return versionSlot{}
	}
	slot := versionSlot{exists: true}
	if !info.IsDir() {
		return slot
	}
	manifest, readErr := readManifest(dir)
	if readErr != nil {
		return slot
	}
	slot.manifest = manifest
	if verifyErr := verifyArtifact(dir, manifest); verifyErr != nil {
		return slot
	}
	slot.valid = true
	slot.reusable = manifest.Digest == want.Digest && manifest.Size == want.Size
	return slot
}

// publishVersion renames a complete temp directory into its version name and takes the commit's
// own lease on it, under the lease lock. It returns the manifest that is now on disk and the
// directory the caller's lease is actually on - the two differ when the canonical name was occupied
// and the verified bytes had to be published beside it.
//
// The rename and the lease are one step because the commit returns a handle to the caller: a
// collector that listed the key directory between the rename and the lease would see a new,
// unleased version and remove it, and Commit would return a version that no longer exists.
//
// A version is named by its bytes, and its manifest is the producer's account of WHAT those bytes
// are. When the identical bytes are published again under a different manifest, the bytes stay one
// version and the manifest is replaced: a producer that can prove a new provenance for the same
// bytes (another source identity that compiles to the same layout, a new cache identity
// generation, a corrected policy) must not be permanently unable to load them because an older
// account of the same bytes is still on disk. Keeping the first manifest made a stale account win
// forever, which is exactly how a corrected cache generation would have been shadowed by the
// generation it replaced.
//
// The reuse is conditional on the directory STILL holding those bytes. A name that exists but whose
// bytes, size or manifest no longer describe the artifact this commit verified is a damaged version,
// and returning it leased would report a successful commit of bytes the caller never gets. That used
// to be the failure: one corrupted artifact.bin survived every re-download, so the live rules were
// correct while the next offline restore was not. Repair rules, in order:
//
//	reusable                 reuse it; replace the manifest only when the account changed.
//	damaged, no lease        quarantine the directory aside, publish the verified copy under its name.
//	damaged, leased          publish the verified copy beside it; the reader keeps its own backing.
//	valid, other identity    publish beside it; a complete artifact of another digest is not this
//	                         build's to delete, and the name is not evidence enough to overwrite it.
//
// In no case is a directory removed or rewritten while this process holds a lease on it, and in no
// case is a verified temp copy thrown away in favour of unverified bytes.
func (s *Store) publishVersion(tempDir string, versionDir string, manifest Manifest) (Manifest, string, error) {
	s.lease.access.Lock()
	defer s.lease.access.Unlock()
	// Stage verified the bytes it wrote, but another writer can land between that check and this
	// rename. Verify once more at the last point that can still refuse to name bytes by a digest
	// they no longer have.
	if manifest.Digest != "" {
		if err := verifyArtifact(tempDir, manifest); err != nil {
			return Manifest{}, "", E.Cause(err, "verify staged artifact")
		}
	}
	slot := inspectVersionSlot(versionDir, manifest)
	if slot.reusable {
		// The identical artifact is already published: the temp copy is redundant, and the
		// existing version is the one this commit leases. The returned manifest is the one now on
		// disk, which is the commit's own unless an identical account was already there.
		if err := replaceManifest(versionDir, manifest); err != nil {
			return Manifest{}, "", err
		}
		effective, err := readManifest(versionDir)
		if err != nil {
			return Manifest{}, "", err
		}
		if err := os.RemoveAll(tempDir); err != nil {
			return Manifest{}, "", err
		}
		s.lease.counts[versionDir]++
		return effective, versionDir, nil
	}
	if slot.exists {
		if slot.valid || s.lease.counts[versionDir] > 0 {
			return s.publishBesideLocked(tempDir, versionDir, manifest)
		}
		if err := s.replaceDamagedLocked(tempDir, versionDir); err != nil {
			return Manifest{}, "", err
		}
		s.lease.counts[versionDir]++
		return manifest, versionDir, nil
	}
	if err := os.Rename(tempDir, versionDir); err != nil {
		return Manifest{}, "", E.Cause(err, "publish artifact version")
	}
	s.lease.counts[versionDir]++
	return manifest, versionDir, nil
}

// replaceDamagedLocked publishes a verified temp directory under a version name that currently
// holds bytes nothing can load. The caller holds the lease lock and has established that no lease
// in this process covers the directory.
//
// The damaged directory is moved aside first and removed only after the verified copy is in place.
// Removing it first would leave a crash window in which the name is empty and the only copy of the
// bytes is a `.tmp` entry; renaming it first also leaves a reader that opened the file without a
// lease holding a functioning handle, which is why the failure of that rename is reported as
// ErrVersionBusy instead of being forced through.
func (s *Store) replaceDamagedLocked(tempDir string, versionDir string) error {
	quarantine := s.repairPathLocked(versionDir, true)
	if err := os.Rename(versionDir, quarantine); err != nil {
		return E.Cause(ErrVersionBusy, "quarantine damaged artifact version ", filepath.Base(versionDir), ": ", err)
	}
	if err := os.Rename(tempDir, versionDir); err != nil {
		// Put the damaged directory back: the name is at least occupied by something a repair can
		// find again, and a concurrent Load is not left with an empty listing.
		_ = os.Rename(quarantine, versionDir)
		return E.Cause(err, "publish repaired artifact version")
	}
	// Best effort: anything left behind starts with the temp prefix and is removed by the next
	// Collect, which is the same rule crash leftovers follow.
	_ = os.RemoveAll(quarantine)
	return nil
}

// publishBesideLocked publishes a verified temp directory under a fresh name next to a version name
// this commit may not replace. The caller holds the lease lock.
//
// The occupied name is not touched: it can be a live reader's backing (a reader that loaded the
// version before its bytes were damaged is exactly the mmap case the lease contract protects) or a
// complete artifact of another identity. The commit still succeeds - it is the artifact its caller
// verified - and Load ranks versions by their manifests, so the repaired copy is found without
// depending on the directory name. The damaged directory is left for Collect, which removes it once
// no lease covers it.
func (s *Store) publishBesideLocked(tempDir string, versionDir string, manifest Manifest) (Manifest, string, error) {
	beside := s.repairPathLocked(versionDir, false)
	if err := os.Rename(tempDir, beside); err != nil {
		return Manifest{}, "", E.Cause(err, "publish repaired artifact beside ", filepath.Base(versionDir))
	}
	s.lease.counts[beside]++
	return manifest, beside, nil
}

// repairPathLocked returns a path next to versionDir that no directory occupies, for a quarantine
// (kind is a temp-prefixed name, so Collect removes it like any crash leftover) or for a repaired
// version published beside an occupied name.
//
// It must be called under the lease lock, which is what makes check-then-rename one step: every
// publisher in this process is serialized by that lock, and the store's contract is one writer per
// directory. Repairs are rare, so a process-wide sequence that only has to skip names already on
// disk is enough.
func (s *Store) repairPathLocked(versionDir string, temporary bool) string {
	directory := filepath.Dir(versionDir)
	base := filepath.Base(versionDir)
	var candidate string
	for attempt := 0; attempt < 64; attempt++ {
		sequence := strconv.FormatUint(repairSequence.Add(1), 36)
		if temporary {
			candidate = filepath.Join(directory, tempPrefix+"replaced-"+sequence+"-"+base)
		} else {
			// A repaired copy is a REAL version: it must be listable and loadable, so it cannot
			// carry the temp prefix Collect deletes.
			candidate = filepath.Join(directory, base+"-repair-"+sequence)
		}
		if !pathExists(candidate) {
			return candidate
		}
	}
	return candidate
}

// pathExists reports whether anything (a directory, a file, a link) is at path.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// replaceManifest rewrites a version's manifest in place, atomically and only when the provenance
// actually changed.
//
// The artifact bytes are untouched, so every reader that already verified them stays valid; a
// reader either sees the old manifest or the new one, and both describe the same bytes. Created is
// a publication timestamp rather than provenance, so republishing the same account of the same
// bytes does not rewrite the file on every load.
func replaceManifest(dir string, manifest Manifest) error {
	existing, existingErr := readManifest(dir)
	if existingErr == nil && sameProvenance(existing, manifest) {
		if !manifest.Published || existing.Created == manifest.Created {
			return nil
		}
		// The caller's order is a logical clock (see Manifest.Published), not a claim about when this
		// conversion happened: adopt its stamp even though the account is otherwise unchanged. Without
		// this the newer stamp would be silently dropped, the version would stay older than the one it
		// must outrank, and the cache could never warm.
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return E.Cause(err, "encode artifact manifest")
	}
	temporary, err := os.CreateTemp(dir, ".tmp-manifest-")
	if err != nil {
		return E.Cause(err, "stage artifact manifest")
	}
	temporaryName := temporary.Name()
	// CreateTemp uses 0600; the manifest it replaces was written 0644, and it is metadata that a
	// diagnostics tool may read. A crash between here and the rename leaves one small file that no
	// reader consults (Load lists directories, readManifest reads manifest.json).
	_ = temporary.Chmod(0o644)
	if _, err = temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
		return E.Cause(err, "write artifact manifest")
	}
	if err = temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return E.Cause(err, "close artifact manifest")
	}
	if err = os.Rename(temporaryName, filepath.Join(dir, manifestName)); err != nil {
		_ = os.Remove(temporaryName)
		return E.Cause(err, "replace artifact manifest")
	}
	return nil
}

// sameProvenance compares two manifests as accounts of the same bytes: everything except the
// publication timestamp must agree.
func sameProvenance(left Manifest, right Manifest) bool {
	if left.Schema != right.Schema ||
		left.Compiler != right.Compiler ||
		left.Policy != right.Policy ||
		left.ABI != right.ABI ||
		left.Platform != right.Platform ||
		left.Size != right.Size ||
		left.Digest != right.Digest ||
		len(left.Dependencies) != len(right.Dependencies) {
		return false
	}
	for index := range left.Dependencies {
		if left.Dependencies[index] != right.Dependencies[index] {
			return false
		}
	}
	return true
}
