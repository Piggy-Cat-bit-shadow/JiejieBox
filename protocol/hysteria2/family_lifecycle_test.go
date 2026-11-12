package hysteria2

// Q05: the two lifecycle counterexamples the audit reproduced against the first family-race cut.
//
// Both are defects of the CLEANUP, not of the race itself, and both were reproduced 3/3 on both
// protocols:
//
//  1. a loser that returned AFTER a network change cleared the NEW generation's client, because the
//     cleanup nulled clients[family-1] by index without checking which instance it was closing;
//  2. the first failed family was never retired, because retireLosers was told to wait for
//     len(candidates)-1 results while one of them had already been read by the race loop - it waited
//     for an already-consumed result and left the failed family attached.
//
// These tests drive the production carrier's own seam (familyCarrier.race and CloseWithError). The
// sdk.Clients are zero values: CloseWithError on a client with no session is a no-op with no network
// side effect, so the lifecycle is exercised without a socket.

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	sdk "github.com/sagernet/sing-quic/hysteria2"

	"github.com/stretchr/testify/require"
)

// TestLateFamilyCleanupMustNotCloseANewGeneration is counterexample 1: the old race's loser must not
// touch the clients a newer generation installed.
func TestLateFamilyCleanupMustNotCloseANewGeneration(t *testing.T) {
	t.Parallel()
	oldA, oldB := &sdk.Client{}, &sdk.Client{}
	carrier := newFamilyCarrier(nil)
	carrier.families = 2
	carrier.clients = [2]*sdk.Client{oldA, oldB}
	loserStarted := make(chan struct{})
	releaseLoser := make(chan struct{})
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	value, err := carrier.race(context.Background(), func(_ context.Context, client *sdk.Client) (any, error) {
		if client == oldA {
			close(loserStarted)
			<-releaseLoser
			return nil, context.Canceled
		}
		<-loserStarted
		return winner, nil
	})
	require.NoError(t, err)
	require.Same(t, winner, value)
	// The network changed: the old clients are dropped and the next demand installs a new generation.
	require.NoError(t, carrier.CloseWithError(net.ErrClosed))
	freshA, freshB := &sdk.Client{}, &sdk.Client{}
	carrier.access.Lock()
	carrier.clients = [2]*sdk.Client{freshA, freshB}
	carrier.winner = 1
	carrier.established = true
	carrier.access.Unlock()
	// The old race's loser returns only now, after the new generation is installed.
	close(releaseLoser)
	require.Never(t, func() bool {
		carrier.access.Lock()
		defer carrier.access.Unlock()
		return carrier.clients[0] != freshA || carrier.clients[1] != freshB
	}, 300*time.Millisecond, time.Millisecond,
		"the previous generation's loser cleanup removed the fresh generation's family client")
}

// TestFirstFailedFamilyMustAlsoBeRetired is counterexample 2: a family that failed before the winner
// arrived is still a loser and must be retired, and the cleanup must not wait for a result the race
// loop already consumed.
func TestFirstFailedFamilyMustAlsoBeRetired(t *testing.T) {
	t.Parallel()
	first, second := &sdk.Client{}, &sdk.Client{}
	carrier := newFamilyCarrier(nil)
	carrier.families = 2
	carrier.clients = [2]*sdk.Client{first, second}
	failed := make(chan struct{})
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	value, err := carrier.race(context.Background(), func(_ context.Context, client *sdk.Client) (any, error) {
		if client == first {
			close(failed)
			return nil, errors.New("first family failed immediately")
		}
		<-failed
		return winner, nil
	})
	require.NoError(t, err)
	require.Same(t, winner, value)
	require.Eventually(t, func() bool {
		carrier.access.Lock()
		defer carrier.access.Unlock()
		return carrier.clients[0] == nil
	}, time.Second, time.Millisecond,
		"the first failed family remains attached; the cleanup waited for an already-consumed result")
	carrier.access.Lock()
	winnerClient := carrier.clients[1]
	carrier.access.Unlock()
	require.Same(t, second, winnerClient, "the winning family must stay attached")
}

// TestALateWinnerAfterTheNetworkChangeIsDiscarded covers the other half of the same window: a race
// that finished after CloseWithError must not publish a session that was built for the path that just
// ended, and the connection it produced must be released.
func TestALateWinnerAfterTheNetworkChangeIsDiscarded(t *testing.T) {
	t.Parallel()
	first, second := &sdk.Client{}, &sdk.Client{}
	carrier := newFamilyCarrier(nil)
	carrier.families = 2
	carrier.clients = [2]*sdk.Client{first, second}
	winnerChosen := make(chan struct{})
	networkChanged := make(chan struct{})
	winner, peer := net.Pipe()
	defer peer.Close()
	// A peer whose remote end was closed reports the close as an error; the deadline keeps a
	// regression (a leaked session) from hanging the test instead of failing it.
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	go func() {
		<-winnerChosen
		_ = carrier.CloseWithError(net.ErrClosed)
		close(networkChanged)
	}()
	_, err := carrier.race(context.Background(), func(_ context.Context, client *sdk.Client) (any, error) {
		if client == first {
			return nil, errors.New("first family failed")
		}
		close(winnerChosen)
		<-networkChanged
		return winner, nil
	})
	require.ErrorIs(t, err, errStaleFamilyRace)
	// The discarded session must be closed, which the peer sees as EOF.
	buffer := make([]byte, 1)
	_, readErr := peer.Read(buffer)
	require.Error(t, readErr, "the discarded session must be closed, not leaked")
}
