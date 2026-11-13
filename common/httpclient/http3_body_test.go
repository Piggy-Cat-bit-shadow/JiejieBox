//go:build with_quic

package httpclient

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestCancelAttemptOnCloseBodyReleasesTheAttempt pins the response-body lifetime wrapper: closing the body
// cancels the establishment context exactly once, so a long-lived parent does not retain it after
// the response is done.
func TestCancelAttemptOnCloseBodyReleasesTheAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &cancelAttemptOnCloseBody{
		ReadCloser: io.NopCloser(strings.NewReader("")),
		cancel:     cancel,
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("closing the response body did not cancel the H3 attempt context")
	}
	if err := body.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
