package libbox

// O02 鈥?the ACTIVE diagnostic's concurrency bound.
//
// The per-test budgets bound one run; they say nothing about how many run at once. A client that
// taps "diagnose" again - or retries after its own UI timeout - would otherwise start a second dial
// through the path being diagnosed. The bound is process wide, the refusal is a reported stage, and
// the slot must come back even when the run is refused or interrupted.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/physicalpath"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// blockingDiagnosePath holds a run inside its passive stage until the test releases it, so the
// slots are observably occupied without any network I/O.
type blockingDiagnosePath struct {
	*fakeConnectionTestPath
	entered chan struct{}
	release chan struct{}
}

func (p *blockingDiagnosePath) Status(root string, network string) physicalpath.PathStatus {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return p.fakeConnectionTestPath.status
}

// TestARefusedDiagnosticClosesTheSubscriberStream: the streaming entry hands the runner an emitter
// whose consumer waits for Finished(). A refusal that returned before closing it would leave that
// consumer (and the goroutine draining it) waiting forever for a test that never started.
func TestARefusedDiagnosticClosesTheSubscriberStream(t *testing.T) {
	for index := 0; index < connectionTestMaxConcurrent; index++ {
		require.True(t, acquireConnectionTestSlot())
	}
	t.Cleanup(func() {
		for index := 0; index < connectionTestMaxConcurrent; index++ {
			releaseConnectionTestSlot()
		}
	})

	emitter := newConnectionTestEmitter(defaultEmitterBounds(), time.Now())
	report := runConnectionTestWithEmitter(context.Background(), newConnectionTestPath("node-a", nil), "proxy", "tcp", "https://example.com", emitter)
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)

	select {
	case <-emitter.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("a refused diagnostic left the subscriber stream open")
	}

	require.NotEmpty(t, report.Events)
	require.Contains(t, report.Events[0], "begin", "a refused run is still announced before it ends")
	require.Contains(t, report.Events[len(report.Events)-1], "end")
	for _, event := range report.Events {
		require.NotContains(t, event, "reused",
			"the passive model was never read, so no 'reused' finding may claim it was")
	}
}

func TestActiveDiagnosticsHaveAHardConcurrencyBound(t *testing.T) {
	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			return stubConn{}, nil
		},
	}
	path := &blockingDiagnosePath{
		fakeConnectionTestPath: newConnectionTestPath("node-a", member),
		entered:                make(chan struct{}, connectionTestMaxConcurrent),
		release:                make(chan struct{}),
	}

	var running sync.WaitGroup
	for index := 0; index < connectionTestMaxConcurrent; index++ {
		running.Add(1)
		go func() {
			defer running.Done()
			runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com")
		}()
	}
	for index := 0; index < connectionTestMaxConcurrent; index++ {
		select {
		case <-path.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("a diagnostic did not reach its passive stage")
		}
	}

	refused := runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com")
	require.Equal(t, connectionTestOutcomeFailed, refused.Outcome)
	require.NotEmpty(t, refused.Stages)
	require.Equal(t, connectionTestStagePath, refused.Stages[0].ID)
	require.Contains(t, refused.Stages[0].Detail, "another diagnostic is still running",
		"a refused run must say why, or the caller will simply tap again")

	close(path.release)
	running.Wait()

	// The slots are back: the next run is admitted rather than refused.
	admitted := make(chan connectionTestReport, 1)
	go func() {
		admitted <- runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com")
	}()
	select {
	case report := <-admitted:
		require.NotContains(t, report.Stages[0].Detail, "another diagnostic is still running")
	case <-time.After(5 * time.Second):
		t.Fatal("a run was not admitted after the bound was released")
	}
}
