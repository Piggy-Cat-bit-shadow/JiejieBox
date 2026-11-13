package tuic

// Q05 follow-up: two demands arriving while the first session is being established must share ONE
// first-creation race. They cannot run their own races against the same client pair: each would
// declare a different family the winner and close the other race's winner.

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/sagernet/sing-quic/tuic"
	"github.com/stretchr/testify/require"
)

// TestBothFamiliesFailingRetireTheirClients pins the no-winner branch: a failed first-creation must
// not leave either client attached for the next demand to inherit.
func TestBothFamiliesFailingRetireTheirClients(t *testing.T) {
	carrier := newFamilyCarrier(nil)
	carrier.families = 2
	carrier.clients = [2]*sdk.Client{{}, {}}

	_, err := carrier.race(context.Background(), func(context.Context, *sdk.Client) (any, error) {
		return nil, errors.New("no route")
	})
	require.Error(t, err)

	carrier.access.Lock()
	defer carrier.access.Unlock()
	require.Nil(t, carrier.clients[0], "the first failed family must be retired")
	require.Nil(t, carrier.clients[1], "the second failed family must be retired")
}

func TestConcurrentFirstCreationRacesShareOneCreation(t *testing.T) {
	carrier := newFamilyCarrier(nil)
	carrier.families = 2
	carrier.clients = [2]*sdk.Client{{}, {}}

	var arrivals atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	signalArrival := func() {
		if arrivals.Add(1) >= 2 {
			releaseOnce.Do(func() { close(release) })
		}
	}

	attempt := func(ctx context.Context, _ *sdk.Client) (any, error) {
		// The first race's two family attempts and the second race's winner attempt all pass
		// through here. The barrier keeps the two races overlapping pre-fix: both snapshot the
		// same clients and spawn their attempts before either can publish a winner.
		signalArrival()
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		conn, peer := net.Pipe()
		t.Cleanup(func() {
			_ = conn.Close()
			_ = peer.Close()
		})
		return conn, nil
	}

	type outcome struct {
		value any
		err   error
	}
	outcomes := make(chan outcome, 2)
	for index := 0; index < 2; index++ {
		go func() {
			value, err := carrier.race(context.Background(), attempt)
			outcomes <- outcome{value: value, err: err}
		}()
	}
	for index := 0; index < 2; index++ {
		select {
		case result := <-outcomes:
			require.NoError(t, result.err, "both demands must receive a stream")
			require.NotNil(t, result.value)
		case <-time.After(5 * time.Second):
			t.Fatal("a first-creation race never settled")
		}
	}

	// The first race creates two family attempts; the second demand joins the published winner
	// with one stream attempt. A fourth attempt would mean the second race repeated the creation.
	require.EqualValues(t, 3, arrivals.Load(),
		"the second demand must dial the winner, not start a second first-creation race")

	require.Eventually(t, func() bool {
		carrier.access.Lock()
		defer carrier.access.Unlock()
		if !carrier.established || carrier.winner == 0 {
			return false
		}
		winnerClient := carrier.clients[carrier.winner-1]
		return winnerClient != nil
	}, 2*time.Second, time.Millisecond,
		"the published winner's client must stay attached; both slots nil means each race retired the other's winner")
}
