package libbox

// Section 5 — the measurement share at REAL Box scale.
//
// The component tests prove that two equivalent health questions share one probe when a registry is
// installed. This drives two URLTest groups inside a real, running Box (and a second Box) through
// the real gRPC-facing service, and counts the HTTP probes that actually reached a local target:
// same Box, same node, same target → ONE probe; a different Box → its own probe.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slowProbeCounter answers after a delay, so two rounds started together are genuinely in flight at
// the same time, and counts requests.
type slowProbeCounter struct {
	server   *httptest.Server
	requests atomic.Int32
}

func newSlowProbeCounter(t *testing.T) *slowProbeCounter {
	t.Helper()
	counter := &slowProbeCounter{}
	counter.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		counter.requests.Add(1)
		time.Sleep(100 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(counter.server.Close)
	return counter
}

func (c *slowProbeCounter) url() string { return c.server.URL + "/generate_204" }

// urlTestSharingConfig builds one Box with two URLTest groups over the same direct member.
func urlTestSharingConfig(probeURL string) string {
	return fmt.Sprintf(`{"log":{"level":"error"},"outbounds":[{"type":"direct","tag":"node-a"},`+
		`{"type":"urltest","tag":"g1","outbounds":["node-a"],"url":%q,"interval":"10m"},`+
		`{"type":"urltest","tag":"g2","outbounds":["node-a"],"url":%q,"interval":"10m"}],`+
		`"route":{"final":"g1"}}`, probeURL, probeURL)
}

// settleProbeRounds waits until the Box's startup health rounds have finished probing, then resets
// the counter. A group runs a round when it starts, so without this the baseline would already
// contain those requests.
func settleProbeRounds(t *testing.T, counter *slowProbeCounter) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		before := counter.requests.Load()
		time.Sleep(200 * time.Millisecond)
		if counter.requests.Load() == before {
			counter.requests.Store(0)
			return
		}
	}
	t.Fatal("the startup health rounds never settled")
}

// urlTestGroupRound triggers the group's own health round, the same entry the manual test uses.
func urlTestGroupRound(t *testing.T, server *CommandServer, tag string) error {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()
	require.NotNil(t, boxInstance)
	outbound, loaded := boxInstance.Outbound().Outbound(tag)
	require.True(t, loaded, "group %s must exist", tag)
	runner, isRunner := outbound.(interface {
		URLTest(context.Context) (map[string]uint16, error)
	})
	require.True(t, isRunner, "a URLTest group exposes its health round")
	_, err := runner.URLTest(context.Background())
	return err
}

// TestTwoGroupsInOneRealBoxShareOneProbe is the Box-level sharing claim.
func TestTwoGroupsInOneRealBoxShareOneProbe(t *testing.T) {
	counter := newSlowProbeCounter(t)
	server := startDiagnoseTestCommandServer(t, urlTestSharingConfig(counter.url()))
	settleProbeRounds(t, counter)

	// Baseline: one group's round, and how many HTTP requests one probe makes.
	require.NoError(t, urlTestGroupRound(t, server, "g1"))
	baseline := counter.requests.Load()
	require.GreaterOrEqual(t, baseline, int32(1))
	counter.requests.Store(0)

	// Two groups, same node, same target, same network generation, started together.
	start := make(chan struct{})
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for _, tag := range []string{"g1", "g2"} {
		tag := tag
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errors <- urlTestGroupRound(t, server, tag)
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	require.EqualValues(t, baseline, counter.requests.Load(),
		"two groups in one Box asking the same health question must perform ONE probe")
}

// TestASecondBoxDoesNotReuseTheFirstBoxesAnswer is the isolation half: the share is per Box.
func TestASecondBoxDoesNotReuseTheFirstBoxesAnswer(t *testing.T) {
	counter := newSlowProbeCounter(t)
	config := urlTestSharingConfig(counter.url())
	firstBox := startDiagnoseTestCommandServer(t, config)
	secondBox := startDiagnoseTestCommandServer(t, config)
	settleProbeRounds(t, counter)

	require.NoError(t, urlTestGroupRound(t, firstBox, "g1"))
	oneProbe := counter.requests.Load()
	require.GreaterOrEqual(t, oneProbe, int32(1))
	counter.requests.Store(0)

	start := make(chan struct{})
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for _, box := range []*CommandServer{firstBox, secondBox} {
		box := box
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errors <- urlTestGroupRound(t, box, "g1")
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 2*oneProbe, counter.requests.Load(),
		"two separate Boxes must each perform their own probe")
}
