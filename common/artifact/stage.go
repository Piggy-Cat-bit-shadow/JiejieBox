package artifact

// Stage-then-Publish is the streaming download contract.
//
// `Commit` is convenient for a caller that already holds the bytes; a download should not be
// buffered just to be hashed. Stage streams the body into the store's temp directory (with a size
// limit), hashing and counting it, and leaves it INVISIBLE to readers. Publish is the single
// rename-and-lease step; Discard throws the temp copy away. A caller validates the staged file
// before publishing, so an unparseable or oversized download never becomes a version.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

// StagedVersion is a complete temp artifact that has not been published yet.
type StagedVersion struct {
	store      *Store
	tempDir    string
	versionDir string
	manifest   Manifest
	closed     bool
}

// Stage streams body into a new version directory and returns it unpublished.
//
// `limit` bounds the body; zero means no caller limit. Exceeding it fails the stage, and the temp
// directory is removed before returning.
func (s *Store) Stage(key string, manifest Manifest, body io.Reader, limit int64) (*StagedVersion, error) {
	if s == nil {
		return nil, E.New("artifact: nil store")
	}
	keyDir := filepath.Join(s.dir, key)
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		return nil, E.Cause(err, "create artifact key directory")
	}
	s.lease.access.Lock()
	tempDir, err := os.MkdirTemp(keyDir, tempPrefix)
	if err == nil {
		s.lease.writing[tempDir] = true
	}
	s.lease.access.Unlock()
	if err != nil {
		return nil, E.Cause(err, "create artifact temp directory")
	}
	if s.testHook != nil {
		s.testHook("commit-temp")
	}
	staged := &StagedVersion{store: s, tempDir: tempDir}
	cleanup := true
	defer func() {
		if cleanup {
			staged.Discard()
		}
	}()

	artifactPath := filepath.Join(tempDir, artifactName)
	file, err := os.Create(artifactPath)
	if err != nil {
		return nil, E.Cause(err, "create artifact file")
	}
	hasher := sha256.New()
	var source io.Reader = body
	if limit > 0 {
		// One byte past the limit is enough to know it was exceeded; nothing past it is kept.
		source = io.LimitReader(body, limit+1)
	}
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), source)
	closeErr := file.Close()
	if copyErr != nil {
		return nil, E.Cause(copyErr, "stream artifact")
	}
	if closeErr != nil {
		return nil, E.Cause(closeErr, "close artifact")
	}
	if limit > 0 && written > limit {
		return nil, E.New("artifact: body exceeds the ", limit, "-byte limit")
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
	versionDir := filepath.Join(keyDir, "v-"+digest[:16])
	staged.versionDir = versionDir
	staged.manifest = manifest
	cleanup = false
	return staged, nil
}

// Path is the staged artifact file, for validation before Publish.
func (st *StagedVersion) Path() string {
	if st == nil {
		return ""
	}
	return filepath.Join(st.tempDir, artifactName)
}

// Manifest returns the completed manifest (size and digest filled).
func (st *StagedVersion) Manifest() Manifest {
	if st == nil {
		return Manifest{}
	}
	return st.manifest
}

// Publish renames the staged directory into its version name and takes the commit's lease.
func (st *StagedVersion) Publish() (*Version, error) {
	if st == nil || st.store == nil {
		return nil, E.New("artifact: nil staged version")
	}
	if st.closed {
		return nil, E.New("artifact: staged version already finished")
	}
	store := st.store
	if err := store.publishVersion(st.tempDir, st.versionDir); err != nil {
		st.Discard()
		return nil, err
	}
	st.closed = true
	store.lease.access.Lock()
	delete(store.lease.writing, st.tempDir)
	store.lease.access.Unlock()
	return &Version{manifest: st.manifest, dir: st.versionDir, store: store}, nil
}

// Discard removes the temp copy. It is safe to call after Publish and more than once.
func (st *StagedVersion) Discard() {
	if st == nil || st.store == nil || st.closed {
		return
	}
	st.closed = true
	_ = os.RemoveAll(st.tempDir)
	st.store.lease.access.Lock()
	delete(st.store.lease.writing, st.tempDir)
	st.store.lease.access.Unlock()
}

// Discard removes one unleased version by digest, if it exists. A recovery uses it to replace a
// version whose bytes were damaged instead of publishing over an entry it cannot verify.
func (s *Store) Discard(key string, id string) error {
	if s == nil || id == "" {
		return nil
	}
	keyDir := filepath.Join(s.dir, key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) < 2 || entry.Name()[0] != 'v' {
			continue
		}
		full := filepath.Join(keyDir, entry.Name())
		manifest, readErr := readManifest(full)
		if readErr != nil || manifest.Digest != id {
			continue
		}
		s.removeIfUnleased(full)
	}
	return nil
}
