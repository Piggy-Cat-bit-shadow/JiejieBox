package http

import E "github.com/sagernet/sing/common/exceptions"

// errHTTP3ResponsePending marks a setup window that expired AFTER the QUIC connection was
// established: the transport came up, but the server had not answered the CONNECT yet.
//
// It is deliberately not ErrHTTP3Unavailable. A slow application response says nothing about
// whether the HTTP/3 path is reachable; treating it as a version verdict would make the next
// tunnel setup skip a path that works. The deadline error is kept in the chain, so callers that
// check context.DeadlineExceeded still see it.
//
// The definition lives in an untagged file because tunnel_client.go is untagged and classifies
// this error; only the producer is behind with_quic.
var errHTTP3ResponsePending = E.New("HTTP/3 tunnel response pending at setup deadline")

type http3ResponsePendingError struct {
	deadline error
}

func (e *http3ResponsePendingError) Error() string {
	return "HTTP/3 connection established but the CONNECT response did not arrive before the setup deadline: " + e.deadline.Error()
}

func (e *http3ResponsePendingError) Unwrap() []error {
	return []error{errHTTP3ResponsePending, e.deadline}
}

func newHTTP3ResponsePendingError(deadline error) error {
	return &http3ResponsePendingError{deadline: deadline}
}
