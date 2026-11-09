package artifact

// C2 — the exact versions one running configuration used.
//
// A versioned store answers "what is newest", but recovery needs "what did THIS configuration
// use". The reference set is that record: the rule sets (and any later owner) write the exact
// digest they published, the daemon persists the snapshot as part of the configuration
// transaction, and a recovery pins those digests so a source that changed in the meantime cannot
// silently replace the artifacts the old document was running.

import (
	"sync"
)

// ReferenceSet is the per-Box record of exact artifact versions.
//
// `pinned` comes from the store when a recovered configuration is started; `used` is what this
// Box actually published. A lookup prefers the pin: during recovery the running source may already
// be newer, and the point is to load the old bytes, not the new ones.
type ReferenceSet struct {
	access   sync.Mutex
	pinned   map[string]string
	retained map[string]string
	used     map[string]string
}

// NewReferenceSet builds a set: `pinned` is what a recovery requires, `retained` is the previous
// configuration's last-good version, which collection must not remove even before anyone loads it.
func NewReferenceSet(pinned map[string]string, retained map[string]string) *ReferenceSet {
	return &ReferenceSet{
		pinned:   copyReferences(pinned),
		retained: copyReferences(retained),
		used:     make(map[string]string),
	}
}

func copyReferences(references map[string]string) map[string]string {
	copied := make(map[string]string, len(references))
	for key, id := range references {
		if key != "" && id != "" {
			copied[key] = id
		}
	}
	return copied
}

// Pinned returns the exact digest a recovered configuration requires for key.
func (r *ReferenceSet) Pinned(key string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.access.Lock()
	defer r.access.Unlock()
	id, loaded := r.pinned[key]
	return id, loaded
}

// RetainedID returns the last-good digest for key, which collection must keep for a possible
// recovery even when nobody holds a lease on it.
func (r *ReferenceSet) RetainedID(key string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.access.Lock()
	defer r.access.Unlock()
	id, loaded := r.retained[key]
	return id, loaded
}

// RetainAll marks a persisted configuration's versions as the ones collection must keep. It is
// called after the configuration transaction commits, so the running Box's own future refreshes
// cannot collect the version a recovery would return to.
func (r *ReferenceSet) RetainAll(references map[string]string) {
	if r == nil {
		return
	}
	r.access.Lock()
	for key, id := range references {
		if key != "" && id != "" {
			r.retained[key] = id
		}
	}
	r.access.Unlock()
}

// Record notes the exact version this Box published for key.
func (r *ReferenceSet) Record(key string, id string) {
	if r == nil || key == "" || id == "" {
		return
	}
	r.access.Lock()
	r.used[key] = id
	r.access.Unlock()
}

// Snapshot copies the versions this Box used, for persistence with the configuration.
func (r *ReferenceSet) Snapshot() map[string]string {
	if r == nil {
		return nil
	}
	r.access.Lock()
	defer r.access.Unlock()
	if len(r.used) == 0 {
		return nil
	}
	copied := make(map[string]string, len(r.used))
	for key, id := range r.used {
		copied[key] = id
	}
	return copied
}
