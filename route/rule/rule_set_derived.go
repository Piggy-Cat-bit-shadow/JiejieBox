package rule

// N3 - reuse of the COMPILED rule-set artifact.
//
// # What already exists, and what is missing
//
// On iOS `mmapRuleSet` already avoids keeping the matcher arrays on the Go heap: it writes the
// parsed rule-set to a transient temp file, maps it, and unlinks the name. What it does NOT avoid
// is rebuilding that file on every load: `reloadFile` always pays `srs.Read` (zlib + succinct
// matcher construction) or a JSON decode, and `mmapRuleSet` always pays the write of the whole
// SRSM file, even when the source bytes are exactly the ones that were converted a minute ago (a
// file-watcher reload, a second tag pointing at the same document, a Box restart).
//
// This file adds that missing relation: a DERIVED artifact, published into the same versioned
// store the remote rule set already uses, that records "this exact source digest converted to this
// exact SRSM layout". A hit maps the persisted file directly (see srs.ReadMmapPersistent) and
// skips both the parse and the write. A miss builds exactly as before and publishes the result
// best-effort.
//
// # The cache key
//
// The store key is only the SOURCE IDENTITY (absolute local path + declared format, hashed); it
// scopes which versions a collector may prune together, and it deliberately contains neither the
// tag nor the mtime nor a node count. The identity of a *conversion* is the tuple recorded in the
// artifact manifest:
//
//   - exact source digest (sha256 of the bytes actually parsed) and size -> source-sha256/source-size
//   - declared source format                                   -> source-format
//   - source rule-set version (the SRS version byte)           -> source-version
//   - the identity-generation rule (see derivedIdentityGeneration) -> identity-generation
//   - SRSM layout generation                                   -> manifest schema + file header
//   - layout/ABI fingerprint of the compiling build            -> manifest compiler + file header
//   - platform ABI                                             -> manifest ABI/Platform (store-enforced)
//
// The digest names the bytes that were handed to the parser, never an earlier pass over a file
// descriptor that an in-place edit can change between the two reads. `identity-generation` makes
// that rule verifiable at hit time: an artifact written before the rule existed does not carry the
// line and is therefore never served, whatever its source-sha256 says.
//
// A load is a hit only when the newest compatible version's source dependencies match exactly. The
// layout generation and fingerprint are ALSO embedded in the SRSM header, so a file written by a
// build with another layout is rejected by the reader itself rather than trusted because a
// manifest field happened to match - see the note in common/srs/mmap.go.
//
// # Failure contract
//
// Nothing here may fail a load. A missing, corrupt, incompatible, unreadable or unwritable derived
// artifact falls back to the original parse path, and an invalid source still returns the original
// parse error. The cache never substitutes "no rules matched" for a failed load.
//
// # Lifetime
//
// The physical version is immutable (the artifact store's commit is temp-then-rename). The mutable
// state - tag, RuleSet, generation - lives in the caller, not in the artifact. The mapping holds
// the store lease until the last matcher that uses it is collected (srs.ReadMmapPersistent takes
// `Version.Release` as its release hook), so Collect cannot remove a version a live matcher still
// reads, and a failure to release only delays collection, it cannot corrupt a running match.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service/filemanager"
)

const (
	// derivedStoreDirectory is the directory the remote rule set already uses for its downloaded
	// artifacts. Sharing it (rather than adding a second cache root) means one lease table, one
	// collector, and one place where "a version still in use" is decided.
	derivedStoreDirectory = "rule-set-artifacts"
	// derivedSchemaPrefix names the derived relationship, distinguishable from the source artifacts
	// the remote path publishes under "rule-set/<format>".
	derivedSchemaPrefix = "rule-set-derived/srsm/"
	// derivedIdentityGeneration names the generation of the rule "a derived artifact's identity is
	// computed from the bytes that were actually parsed". It is recorded in every manifest as an
	// extra dependency and REQUIRED, verbatim, by derivedManifestMatches. That requirement is the
	// migration: an artifact written before the rule existed has no identity-generation line, so it
	// can never be served even when its source-sha256 happens to match. The store replaces the
	// manifest when identical artifact bytes are republished with a different provenance, so an
	// unchanged source is not stuck forever behind the old account. Bump the value only if the
	// identity rule itself changes meaning again.
	derivedIdentityGeneration = 2
)

// derivedSource is the exact identity of the bytes a derived artifact belongs to.
type derivedSource struct {
	// key is the artifact store entry. It is identity-scoped (not content-scoped) so that the
	// store's own Collect bounds the disk: at most the newest version plus any version a live
	// mapping still leases.
	key string
	// format is the declared source format; it is part of both the key and the manifest.
	format string
	// digest is the hex sha256 of the exact source bytes, and size their length.
	digest string
	size   int64
	// tag is recorded for diagnostics only. It is NOT part of the identity: the same file loaded
	// under two tags is one conversion.
	tag string
}

// derivedRuleSetCache is one rule set's handle on the shared artifact store. It holds no mappings
// itself and no package-level map: the mapping's lifetime is owned by the rules that use it.
type derivedRuleSetCache struct {
	ctx    context.Context
	store  *artifact.Store
	logger logger.Logger
}

// derivedCacheCounters are bounded diagnostics: four counters, no per-rule history. They exist so a
// test (and a debug log) can prove the warm path is really taken, not merely compiled.
var derivedCacheCounters struct {
	hits      atomic.Int64
	misses    atomic.Int64
	publishes atomic.Int64
	failures  atomic.Int64
}

func derivedCacheDiagnostics() (hits int64, misses int64, publishes int64, failures int64) {
	return derivedCacheCounters.hits.Load(), derivedCacheCounters.misses.Load(),
		derivedCacheCounters.publishes.Load(), derivedCacheCounters.failures.Load()
}

func resetDerivedCacheDiagnostics() {
	derivedCacheCounters.hits.Store(0)
	derivedCacheCounters.misses.Store(0)
	derivedCacheCounters.publishes.Store(0)
	derivedCacheCounters.failures.Store(0)
}

// newDerivedRuleSetCache opens the shared store, or returns nil when this context has no persistent
// base directory (tests and embedded uses) - in which case nothing is cached and the original path
// runs unchanged.
func newDerivedRuleSetCache(ctx context.Context, logger logger.Logger) *derivedRuleSetCache {
	base := filemanager.BasePath(ctx, "")
	if base == "" {
		return nil
	}
	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	if err != nil {
		logger.Debug(E.Cause(err, "open derived rule-set store"))
		return nil
	}
	return &derivedRuleSetCache{ctx: ctx, store: store, logger: logger}
}

// localDerivedSource builds the identity for one local file path and format. The path is part of
// the identity so re-pointing a tag at another file cannot serve the previous file's conversion.
func localDerivedSource(tag string, path string, format string, digest string, size int64) *derivedSource {
	identity := "local\x00" + format + "\x00" + path
	sum := sha256.Sum256([]byte(identity))
	return &derivedSource{
		key:    "derived-local-" + hex.EncodeToString(sum[:8]),
		format: format,
		digest: digest,
		size:   size,
		tag:    tag,
	}
}

// derivedFormatName canonicalises the declared format: an empty format means "source" to the
// loader, so it must not be a second identity for the same file.
func derivedFormatName(format string) string {
	if format == "" {
		return C.RuleSetFormatSource
	}
	return format
}

// hashOpenFile hashes the whole file and rewinds it for the parser. It is one sequential read of
// the source, which the cold path already had to read; it is skipped entirely on platforms where
// the derived path is not active.
//
// The digest it returns is the FIRST-PASS identity: it answers "which artifact may already describe
// this file", so the warm path can stop here. It is NOT the publish identity. A file can be edited
// in place between this pass and the parse read, so the bytes the parser actually receives are
// measured again by derivedIdentity and that second measurement is what a manifest may name.
func hashOpenFile(file *os.File) (digest string, size int64, err error) {
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

// derivedIdentity accumulates the sha256 and the byte count of the source bytes that are actually
// handed to the parser. It is written through io.TeeReader, so the measurement is a by-product of
// the parse read rather than an extra pass over the file: the cold path still performs exactly one
// hashing pass (for the cache lookup) plus one parse read (which now also yields the identity).
//
// The JSON path tees io.ReadAll and hashes exactly the bytes passed to the decoder. The binary path
// tees the reader handed to srs.Read and then drains the rest of the descriptor into the same
// accumulator, so the identity covers the complete byte stream from offset 0: the zlib stream the
// parser interprets plus any trailing bytes it deliberately does not.
type derivedIdentity struct {
	hasher hash.Hash
	size   int64
}

func newDerivedIdentity() *derivedIdentity {
	return &derivedIdentity{hasher: sha256.New()}
}

func (i *derivedIdentity) Write(bytes []byte) (int, error) {
	written, err := i.hasher.Write(bytes)
	i.size += int64(written)
	return written, err
}

func (i *derivedIdentity) digest() string {
	return hex.EncodeToString(i.hasher.Sum(nil))
}

// matches reports whether the measured bytes are the ones the first pass hashed. When they are
// not, an in-place edit landed inside the hashing/parsing window and the first-pass identity must
// not be used for anything that names the parsed bytes.
func (i *derivedIdentity) matches(digest string, size int64) bool {
	return i.size == size && i.digest() == digest
}

// derivedManifest builds the manifest of one conversion. Schema and Compiler carry the layout
// generation/fingerprint so a person (and Collect's compatibility check) can tell what wrote it;
// the hit test itself uses the source dependency lines and the file header.
func (c *derivedRuleSetCache) derivedManifest(source *derivedSource, ruleSetVersion uint8) artifact.Manifest {
	return artifact.NewManifest(
		derivedSchemaPrefix+strconv.Itoa(srs.MmapFormatVersion),
		"srsm/"+srs.MmapFingerprint()+"/sing-box/"+C.Version,
		"source-format/"+source.format,
		"identity-generation="+strconv.Itoa(derivedIdentityGeneration),
		"source-sha256="+source.digest,
		"source-size="+strconv.FormatInt(source.size, 10),
		"source-format="+source.format,
		"source-version="+strconv.Itoa(int(ruleSetVersion)),
		"srsm-format="+strconv.Itoa(srs.MmapFormatVersion),
		"cache-key="+source.key,
		"tag="+source.tag,
	)
}

// derivedManifestMatches tests the exact source identity. Only lines that pin BYTES are accepted:
// a tag or a creation time must never make a stale conversion look current. The identity-generation
// line is required, so an artifact written before the "identity is the parsed bytes" rule existed is
// a miss no matter what its source-sha256 says - refusing that hit is the only way to stop serving
// a conversion whose provenance was measured on a different snapshot.
func derivedManifestMatches(manifest artifact.Manifest, source *derivedSource) bool {
	wanted := []string{
		"identity-generation=" + strconv.Itoa(derivedIdentityGeneration),
		"source-sha256=" + source.digest,
		"source-size=" + strconv.FormatInt(source.size, 10),
		"source-format=" + source.format,
	}
	for _, want := range wanted {
		found := false
		for _, dependency := range manifest.Dependencies {
			if dependency == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// open returns the mapped rule-set when the persisted conversion of this exact source exists.
//
// The returned options are backed by the mapping, exactly like the temp-file path; the caller must
// treat them as read-only. The store lease is transferred to the mapping and is released when the
// last matcher using it is collected.
func (c *derivedRuleSetCache) open(source *derivedSource) (option.PlainRuleSetCompat, bool) {
	if c == nil {
		return option.PlainRuleSetCompat{}, false
	}
	version, err := c.store.Load(source.key)
	if err != nil {
		derivedCacheCounters.misses.Add(1)
		if !os.IsNotExist(err) {
			c.logger.Debug(E.Cause(err, "load derived rule-set"))
		}
		return option.PlainRuleSetCompat{}, false
	}
	manifest := version.Manifest()
	if !derivedManifestMatches(manifest, source) {
		// Another source's conversion. It stays on disk until the owner publishes the new one;
		// serving it would run stale rules, which is the one thing a cache must not do.
		version.Release()
		derivedCacheCounters.misses.Add(1)
		return option.PlainRuleSetCompat{}, false
	}
	file, err := version.Open()
	if err != nil {
		version.Release()
		derivedCacheCounters.misses.Add(1)
		c.logger.Debug(E.Cause(err, "open derived rule-set"))
		return option.PlainRuleSetCompat{}, false
	}
	info, statErr := file.Stat()
	if statErr != nil || info.Size() != manifest.Size {
		// The path and the mapped file must be one physical record. Stat before mapping, and hash
		// the SAME descriptor that is about to be mapped below.
		_ = file.Close()
		version.Release()
		c.discard(source.key, version.ID())
		derivedCacheCounters.misses.Add(1)
		return option.PlainRuleSetCompat{}, false
	}
	digest, _, hashErr := hashOpenFile(file)
	if hashErr != nil || digest != manifest.Digest {
		_ = file.Close()
		version.Release()
		c.discard(source.key, version.ID())
		derivedCacheCounters.misses.Add(1)
		c.logger.Debug(E.Cause(hashErr, "verify derived rule-set bytes"))
		return option.PlainRuleSetCompat{}, false
	}
	mapped, mapErr := srs.ReadMmapPersistent(file, version.Release)
	_ = file.Close()
	if mapErr != nil {
		// Verified bytes this build cannot interpret: an old layout generation or a fingerprint
		// from another dependency layout. Drop it so the next load does not repeat the attempt;
		// the release above already ran only on success, so the lease is still ours here.
		version.Release()
		c.discard(source.key, version.ID())
		derivedCacheCounters.misses.Add(1)
		c.logger.Debug(E.Cause(mapErr, "map derived rule-set"))
		return option.PlainRuleSetCompat{}, false
	}
	derivedCacheCounters.hits.Add(1)
	return mapped, true
}

// discard removes a derived version this build has proven unusable. A version a live mapping still
// leases is left alone by the store; a failure here is not a load failure.
func (c *derivedRuleSetCache) discard(key string, id string) {
	if c == nil || id == "" {
		return
	}
	if err := c.store.Discard(key, id); err != nil {
		c.logger.Debug(E.Cause(err, "discard derived rule-set"))
	}
}

// publish stores one conversion best-effort. It is called only after the parsed rules have been
// built and validated, so a document that cannot become live rules is never cached as one.
//
// `file` is the transient SRSM file the caller is about to map: streaming THAT file into the store
// avoids serialising the conversion a second time. The extra sequential read+write of the derived
// bytes is the cold-path cost of the cache and is measured in the report. A failure here costs the
// caller nothing but a log line.
func (c *derivedRuleSetCache) publish(source *derivedSource, ruleSetVersion uint8, file *os.File) {
	if c == nil || source == nil || file == nil {
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		derivedCacheCounters.failures.Add(1)
		c.logger.Debug(E.Cause(err, "rewind derived rule-set"))
		return
	}
	version, err := c.store.Commit(source.key, c.derivedManifest(source, ruleSetVersion), file)
	if err != nil {
		derivedCacheCounters.failures.Add(1)
		c.logger.Debug(E.Cause(err, "store derived rule-set"))
		return
	}
	// The cold load's live rules are backed by the temp mapping, not by this version; the commit's
	// own lease is not what keeps it alive, so hand it back immediately.
	version.Release()
	derivedCacheCounters.publishes.Add(1)
	// Keep the disk bounded: the newest version stays, an older one goes once no live mapping
	// leases it.
	if err = c.store.Collect(source.key); err != nil {
		c.logger.Debug(E.Cause(err, "collect derived rule-set"))
	}
}
