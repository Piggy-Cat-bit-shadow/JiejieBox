package v2rayxhttp

import (
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingReadCloser struct {
	reads  int
	closes int
	access sync.Mutex
}

func (c *countingReadCloser) Read([]byte) (int, error) {
	c.access.Lock()
	c.reads++
	c.access.Unlock()
	return 0, io.EOF
}

func (c *countingReadCloser) Close() error {
	c.access.Lock()
	c.closes++
	c.access.Unlock()
	return nil
}

// The request body XHTTP hands to net/http is a readCloser wrapper. net/http closes Request.Body when
// the request ends (success, cancel, timeout, stream reset), so the wrapper's Close is the only chance
// the underlying upload pipe gets a close from that side. Before this test the wrapper's Close was a
// no-op, which meant an abandoned upload relied solely on the conn teardown to close the pipe.
func TestRequestBodyWrapperClosesTheUnderlyingBody(t *testing.T) {
	body := &countingReadCloser{}
	requestBody := readCloser{body}
	require.NoError(t, requestBody.Close())
	require.Equal(t, 1, body.closes, "the wrapper did not delegate Close")
}

// Closing the request body must break the upload pipe: that is what wakes a writer parked on the pipe
// when a request is cancelled or reset before the peer reads it.
func TestClosingTheRequestBodyUnblocksTheUploadPipe(t *testing.T) {
	reader, writer := io.Pipe()
	requestBody := readCloser{newAdoptedBody(reader)}
	require.NoError(t, requestBody.Close())

	_, err := writer.Write([]byte("after close"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

// A body that does not implement io.Closer keeps the historical no-op Close rather than panicking.
func TestRequestBodyWrapperAcceptsANonClosableBody(t *testing.T) {
	requestBody := readCloser{readerOnly{}}
	require.NoError(t, requestBody.Close())
}

type readerOnly struct{}

func (readerOnly) Read([]byte) (int, error) { return 0, errors.New("not used") }
