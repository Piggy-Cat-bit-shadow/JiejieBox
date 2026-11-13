package artifact

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVersionReleaseIsConcurrentSafe pins the one-shot latch the Release comment promises. A
// deferred release and an explicit release may be reached by two owners at the same time; the
// atomic latch makes the lease drop exactly once and `go test -race` does not report the old bool.
func TestVersionReleaseIsConcurrentSafe(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	version, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:example.org"))
	require.NoError(t, err)

	const callers = 64
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer waitGroup.Done()
			version.Release()
		}()
	}
	waitGroup.Wait()

	store.lease.access.Lock()
	count := store.lease.counts[version.dir]
	store.lease.access.Unlock()
	require.Zero(t, count, "the lease must be released exactly once")
}
