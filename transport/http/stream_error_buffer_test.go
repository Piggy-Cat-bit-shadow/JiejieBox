//go:build with_quic

// Gated on with_quic because it builds on the package's with_quic-gated test doubles:
// extendedStubConn embeds stubConn, which stream_error_test.go declares under the same
// constraint. Without it the untagged test build fails with `undefined: stubConn`, so
// `go vet ./...` and `go test ./...` are red in the configuration a reader gets by default.
// Same fix, and the same reason, as client_h3_strict_fallback_test.go. The production
// behaviour under test - normalizingConn refusing buffered IO it cannot honour - is not
// QUIC-specific, but the fixture that proves the wrapper is not itself an N.ExtendedConn is.
package http

import (
	"errors"
	"testing"

	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"
)

// extendedStubConn is a net.Conn that ALSO honours buffered IO, i.e. what
// v2rayhttp.HTTP2ConnWrapper provides in production. It records whether the buffered path was
// actually used, so a test cannot pass by the wrapper refusing everything.
type extendedStubConn struct {
	stubConn
	writes   int
	writeErr error
	readErr  error
}

func (c *extendedStubConn) WriteBuffer(buffer *buf.Buffer) error {
	c.writes++
	return c.writeErr
}

func (c *extendedStubConn) ReadBuffer(buffer *buf.Buffer) error { return c.readErr }

// TestNormalizingConnRefusesBufferedIOItCannotHonour pins the failure direction of a branch that is
// unreachable today and must never be silent if it becomes reachable.
//
// The buffered copy path is chosen by a type assertion the copy machinery makes against the WRAPPER,
// not against the connection underneath it. `bufio.Copy` sees only normalizingConn, and the named
// `inner` field means it cannot see through it - that is deliberate, see the type's comment and
// TestNormalizingConnHidesUpstream. So ReadBuffer/WriteBuffer existing on this type is the ONLY
// thing that selects the buffered branch, whatever `inner` turns out to be at runtime.
//
// Both construction sites (connect_udp.go:134, server_h2.go:261) pass a
// v2rayhttp.HTTP2ConnWrapper, which embeds an N.ExtendedConn, so the assertion succeeds and this is
// not a live defect - checked at both sites rather than assumed. It is still tested, because the
// alternative failure is invisible: the caller has already taken ownership of the buffer's bytes, so
// returning nil reports a write that never happened and nothing downstream can detect the loss.
func TestNormalizingConnRefusesBufferedIOItCannotHonour(t *testing.T) {
	// stubConn offers only net.Conn. The fixture must genuinely lack the interface, or this test
	// exercises the success path and proves nothing.
	var inner any = &stubConn{}
	if _, isExtended := inner.(N.ExtendedConn); isExtended {
		t.Fatal("fixture is wrong: stubConn must NOT be an N.ExtendedConn")
	}

	wrapped := &normalizingConn{inner: &stubConn{}}

	// The wrapper must STILL expose the buffered methods, or the copy path never reaches the branch
	// under test and this test would pass for the wrong reason.
	if _, isExtended := any(wrapped).(N.ExtendedWriter); !isExtended {
		t.Fatal("normalizingConn must expose WriteBuffer, or the copy path never reaches the branch")
	}
	if _, isExtended := any(wrapped).(N.ExtendedReader); !isExtended {
		t.Fatal("normalizingConn must expose ReadBuffer, or the copy path never reaches the branch")
	}

	if err := wrapped.WriteBuffer(buf.New()); err == nil {
		t.Fatal("WriteBuffer on a non-ExtendedConn returned nil: that reports success for a buffer " +
			"the caller believes was written, and nothing downstream can detect the loss")
	}
	if err := wrapped.ReadBuffer(buf.New()); err == nil {
		t.Fatal("ReadBuffer on a non-ExtendedConn returned nil, reporting a read that did not happen")
	}
}

// The other half, and the one that keeps the test above honest: when the inner connection CAN honour
// buffered IO the wrapper must delegate, and must still normalize the error on the way out.
//
// Without this, "refuse everything" would satisfy the test above while breaking the production path
// the wrapper exists to serve.
func TestNormalizingConnDelegatesBufferedIOWhenItCan(t *testing.T) {
	sentinel := errors.New("inner buffered write failed")
	inner := &extendedStubConn{writeErr: sentinel}
	wrapped := &normalizingConn{inner: inner}

	err := wrapped.WriteBuffer(buf.New())
	if err == nil {
		t.Fatal("WriteBuffer must surface the inner error, not swallow it")
	}
	if inner.writes != 1 {
		t.Fatalf("WriteBuffer must delegate to the inner ExtendedConn exactly once, got %d calls",
			inner.writes)
	}
	// The error must arrive normalized as a visible fault, not raw. This is the property the whole
	// wrapper exists for: route/conn.go logs a copy failure at ERROR unless it recognises the error
	// as a normal closure, so an unnormalized fault would be treated as ordinary and disappear.
	//
	// Note what is deliberately NOT asserted: errors.Is(err, sentinel). visibleFault.Is matches only
	// another visibleFault and it has no Unwrap, so the inner error is unreachable through
	// errors.Is BY DESIGN - see visibleFault's own comment, which exists to stop a fault from ever
	// satisfying E.IsClosedOrCanceled and being silenced. An earlier draft of this test asserted the
	// opposite and failed; the code was right and the test was guessing.
	if _, isFault := err.(*visibleFault); !isFault {
		t.Fatalf("an unclassifiable inner error must be normalized into a visible fault, got %T: %v",
			err, err)
	}
	// The original must still be reachable for diagnostics, through Cause rather than Is.
	if cause := err.(*visibleFault).Cause(); cause != sentinel {
		t.Fatalf("Cause must preserve the inner error for diagnostics, got %v", cause)
	}
}
