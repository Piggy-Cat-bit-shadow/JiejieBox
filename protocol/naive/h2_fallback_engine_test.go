//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The engine-level H2 fallback client: lazy creation, reuse, failure handling,
// close, and the two places it is used instead of the per-request H2 choice.

type fakeH2Client struct {
	access       sync.Mutex
	dialEarly    int
	dialWithQUIC []bool
	closed       int
	closedConns  int
	dialErr      error
}

func (c *fakeH2Client) DialEarly(context.Context, M.Socksaddr) (cronet.NaiveConn, error) {
	c.access.Lock()
	c.dialEarly++
	c.access.Unlock()
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return &fakeNaiveConn{}, nil
}

func (c *fakeH2Client) DialEarlyWithQUIC(_ context.Context, _ M.Socksaddr, forceQUIC bool) (cronet.NaiveConn, error) {
	c.access.Lock()
	c.dialWithQUIC = append(c.dialWithQUIC, forceQUIC)
	c.access.Unlock()
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return &fakeNaiveConn{}, nil
}

func (c *fakeH2Client) Close() error {
	c.access.Lock()
	c.closed++
	c.access.Unlock()
	return nil
}

func (c *fakeH2Client) CloseAllConnections() {
	c.access.Lock()
	c.closedConns++
	c.access.Unlock()
}

func (c *fakeH2Client) earlyDials() int {
	c.access.Lock()
	defer c.access.Unlock()
	return c.dialEarly
}

func (c *fakeH2Client) quicChoices() []bool {
	c.access.Lock()
	defer c.access.Unlock()
	return append([]bool(nil), c.dialWithQUIC...)
}

func TestH2FallbackClientCreatesOneEngineUnderConcurrency(t *testing.T) {
	var creates atomic.Int32
	engine := &fakeH2Client{}
	fallback := newH2FallbackClient(func() (naiveH2Client, error) {
		creates.Add(1)
		return engine, nil
	})

	var (
		wg      sync.WaitGroup
		access  sync.Mutex
		results []naiveH2Client
		errs    []error
	)
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := fallback.get()
			access.Lock()
			results = append(results, client)
			errs = append(errs, err)
			access.Unlock()
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, creates.Load(), "concurrent first use must build one engine")
	require.Len(t, results, 32)
	for index := range errs {
		require.NoError(t, errs[index])
		require.Same(t, engine, results[index])
	}
}

func TestH2FallbackClientCreationFailureIsRetried(t *testing.T) {
	var attempts atomic.Int32
	engine := &fakeH2Client{}
	fallback := newH2FallbackClient(func() (naiveH2Client, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("native engine unavailable")
		}
		return engine, nil
	})

	_, err := fallback.get()
	require.Error(t, err)
	client, err := fallback.get()
	require.NoError(t, err)
	require.Same(t, engine, client)
	require.EqualValues(t, 2, attempts.Load(), "a creation failure must not be cached")
}

func TestH2FallbackClientClose(t *testing.T) {
	engine := &fakeH2Client{}
	fallback := newH2FallbackClient(func() (naiveH2Client, error) { return engine, nil })
	_, err := fallback.get()
	require.NoError(t, err)
	require.NoError(t, fallback.Close())
	require.Equal(t, 1, engine.closed)
	require.NoError(t, fallback.Close(), "Close is idempotent")
	require.Equal(t, 1, engine.closed)
	_, err = fallback.get()
	require.Error(t, err, "a closed fallback client must not create a new engine")
	require.Zero(t, engine.closedConns)
}

func TestDialNaiveH2PrefersTheEngineLevelClient(t *testing.T) {
	primary := newScriptedClient()
	engine := &fakeH2Client{}
	fallback := newH2FallbackClient(func() (naiveH2Client, error) { return engine, nil })

	conn, err := dialNaiveH2(context.Background(), primary, fallback, logger.NOP(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 1, engine.earlyDials(), "the engine-level client owns the H2 attempt")
	require.Empty(t, primary.recorded(), "the primary client must not be used")
}

func TestDialNaiveH2FallsBackToThePerRequestChoice(t *testing.T) {
	primary := newScriptedClient()
	fallback := newH2FallbackClient(func() (naiveH2Client, error) {
		return nil, errors.New("no second engine")
	})

	conn, err := dialNaiveH2(context.Background(), primary, fallback, logger.NOP(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []bool{false}, primary.recorded(),
		"without a second engine the previous per-request H2 choice is used")
}

func TestFallbackPathUsesTheEngineLevelH2Client(t *testing.T) {
	primary := newScriptedClient()
	primary.dial[true] = cronet.NetErrorQUICHandshakeFailed
	engine := &fakeH2Client{}
	fallback := newH2FallbackClient(func() (naiveH2Client, error) { return engine, nil })

	conn, err := dialNaiveWithH3Fallback(context.Background(), primary, &h3Fallback{}, fallback, logger.NOP(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []bool{true}, primary.recorded(), "only the H3 attempt belongs to the primary client")
	require.Equal(t, 1, engine.earlyDials(), "the retry is the engine-level H2 client's")
}

func TestFallbackPathWithoutEngineKeepsThePerRequestChoice(t *testing.T) {
	primary := newScriptedClient()
	primary.dial[true] = cronet.NetErrorQUICHandshakeFailed

	conn, err := dialNaiveWithH3Fallback(context.Background(), primary, &h3Fallback{}, nil, logger.NOP(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []bool{true, false}, primary.recorded())
}
