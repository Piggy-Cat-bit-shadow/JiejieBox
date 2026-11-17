package remote

// Regression test for W8 in docs/fork/go-1274-regressions.md: a provider's first download could be
// refused by the dialer across a network reset ("network changed while dialling") and nothing retried,
// so the subscription stayed empty until its next update_interval - an hour in the profile that hit it.
//
// The retry is only for the failure that provably never reached the server, so this test also pins the
// negative side: an ordinary transport error is NOT retried.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	providerAdapter "github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// roundTripFunc lets a test answer requests without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func responseFor(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// downloadProvider builds the minimum provider the download path needs. Everything the retry touches is
// the HTTP client and the logger.
func downloadProvider(t *testing.T, transport roundTripFunc) *ProviderRemote {
	t.Helper()
	ctx := context.Background()
	logFactory := log.NewNOPFactory()
	logger := logFactory.NewLogger("test")
	return &ProviderRemote{
		Adapter: providerAdapter.NewAdapter(
			ctx, nil, nil, logFactory, logger, "w8", C.ProviderTypeRemote, option.ProviderHealthCheckOptions{},
		),
		ctx:        ctx,
		logger:     logger,
		httpClient: &http.Client{Transport: transport},
	}
}

func TestANetworkChangeDuringTheDownloadIsRetried(t *testing.T) {
	var attempts atomic.Int32
	provider := downloadProvider(t, func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			// The shape the dialer produces: the reset happened while connecting, so the request never
			// reached the server.
			return nil, dialer.ErrNetworkChanged
		}
		return responseFor(`{"outbounds":[]}`), nil
	})

	request, err := http.NewRequest(http.MethodGet, "https://example.com/sub.json", nil)
	require.NoError(t, err)
	response, err := provider.downloadDocument(context.Background(), request)
	require.NoError(t, err, "the reset that never reached the server must be retried")
	require.NotNil(t, response)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 2, attempts.Load(), "exactly one retry was needed")
}

func TestAnOrdinaryTransportFailureIsNotRetried(t *testing.T) {
	var attempts atomic.Int32
	ordinary := http.ErrHandlerTimeout
	provider := downloadProvider(t, func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, ordinary
	})

	request, err := http.NewRequest(http.MethodGet, "https://example.com/sub.json", nil)
	require.NoError(t, err)
	_, err = provider.downloadDocument(context.Background(), request)
	require.ErrorIs(t, err, ordinary)
	require.EqualValues(t, 1, attempts.Load(),
		"only a failure that never reached the server may be retried; anything else could double-apply")
}

func TestTheRetryIsBounded(t *testing.T) {
	var attempts atomic.Int32
	provider := downloadProvider(t, func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, dialer.ErrNetworkChanged
	})

	request, err := http.NewRequest(http.MethodGet, "https://example.com/sub.json", nil)
	require.NoError(t, err)
	_, err = provider.downloadDocument(context.Background(), request)
	require.ErrorIs(t, err, dialer.ErrNetworkChanged)
	require.EqualValues(t, providerDownloadRetries+1, attempts.Load(),
		"a persistent reset must not loop forever")
}

func TestACancelledContextStopsTheRetry(t *testing.T) {
	var attempts atomic.Int32
	provider := downloadProvider(t, func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, dialer.ErrNetworkChanged
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequest(http.MethodGet, "https://example.com/sub.json", nil)
	require.NoError(t, err)
	_, err = provider.downloadDocument(ctx, request)
	require.Error(t, err)
	require.EqualValues(t, 1, attempts.Load(), "a cancelled caller must not wait through a backoff")
}

// keep the adapter import used if the stub above is ever simplified.
var _ adapter.CacheFile
