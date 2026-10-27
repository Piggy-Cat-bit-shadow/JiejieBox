package route

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHalfCloseSnapshotNeverSplitsARelease is the regression for the macOS gate failure on
// 2026-10-27: `released == peer reset + keepalive timeout` was asserted by every snapshot, but
// `recordReleased` published the two counters as two independent atomic adds. A reader that landed
// between them saw released=1 with an empty partition - exactly the CI assertion that reported
// "released must be partitioned into peer reset and keepalive timeout" (expected 1, actual 0).
//
// The test drives a release and a snapshot concurrently, many times, from several readers. Before
// the counters were read and written under one lock this fails within a few thousand iterations;
// after it, the partitions hold in every snapshot by construction.
func TestHalfCloseSnapshotNeverSplitsARelease(t *testing.T) {
	var manager ConnectionManager

	stop := make(chan struct{})
	failure := make(chan string, 4)
	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := manager.HalfCloseDiagnostics()
				if snapshot.Released != snapshot.PeerReset+snapshot.KeepaliveTimeout {
					select {
					case failure <- fmt.Sprintf(
						"snapshot split a release: released=%d peer-reset=%d keepalive-timeout=%d",
						snapshot.Released, snapshot.PeerReset, snapshot.KeepaliveTimeout):
					default:
					}
					return
				}
			}
		}()
	}

	const releases = 50000
	for i := 0; i < releases; i++ {
		manager.halfCloseDiag.recordObserved()
		manager.halfCloseDiag.recordReleased(errors.New("peer reset"))
	}
	close(stop)
	readers.Wait()

	select {
	case message := <-failure:
		t.Fatal(message)
	default:
	}

	snapshot := manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(releases), snapshot.Observed)
	require.Equal(t, uint64(releases), snapshot.Released)
	require.Equal(t, uint64(releases), snapshot.PeerReset)
	require.Zero(t, snapshot.KeepaliveTimeout)
	requireConserved(t, snapshot)
}

// TestHalfCloseSnapshotPartitionCoversTheTimeoutHalf keeps the other verdict in the same contract:
// a timeout is still counted as a release, and never as a peer reset.
func TestHalfCloseSnapshotPartitionCoversTheTimeoutHalf(t *testing.T) {
	var manager ConnectionManager
	manager.halfCloseDiag.recordObserved()
	manager.halfCloseDiag.recordReleased(timeoutCause{})

	snapshot := manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(1), snapshot.Released)
	require.Zero(t, snapshot.PeerReset)
	require.Equal(t, uint64(1), snapshot.KeepaliveTimeout)
	requireConserved(t, snapshot)
}

// timeoutCause is a terminal error whose Timeout() answer is true, which is the only property the
// diagnostics use to split a release.
type timeoutCause struct{}

func (timeoutCause) Error() string { return "keepalive timeout" }
func (timeoutCause) Timeout() bool { return true }
