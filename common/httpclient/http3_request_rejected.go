package httpclient

import (
	"errors"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	E "github.com/sagernet/sing/common/exceptions"
)

// errH3RequestRejected marks a request the peer EXPLICITLY did not process (RFC 9114
// H3_REQUEST_REJECTED, 0x10b).
//
// Two consequences, and both matter: the request may be retried on another version without a
// duplicate side effect, and the rejection is NOT evidence that HTTP/3 is unavailable - arming the
// failure ladder for it would make later requests skip a healthy HTTP/3 path.
//
// quic-go surfaces the reset as a *quic.StreamError wrapped by the response-read path, so the
// classification is on the error chain.
var errH3RequestRejected = E.New("the peer explicitly did not process the HTTP/3 request")

func isH3RequestRejected(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errH3RequestRejected) {
		return true
	}
	var streamErr *quic.StreamError
	if errors.As(err, &streamErr) {
		return uint64(streamErr.ErrorCode) == h3ErrCodeRequestRejected
	}
	// quic-go wraps the same condition as its own *http3.Error once its internal retry is exhausted.
	var h3Err *http3.Error
	if errors.As(err, &h3Err) {
		return uint64(h3Err.ErrorCode) == h3ErrCodeRequestRejected
	}
	return false
}

// h3ErrCodeRequestRejected is RFC 9114 H3_REQUEST_REJECTED. Spelled out here so this package does
// not depend on the http3 package's exported constant for an error code it has to compare
// numerically anyway.
const h3ErrCodeRequestRejected = 0x10b
