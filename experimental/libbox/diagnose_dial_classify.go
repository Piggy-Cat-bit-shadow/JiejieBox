package libbox

// O02 — turning a settled dial error into the stable code the report carries.
//
// The runner observes the error the REAL dial returned. That is the only honest source: rewording
// the sentence is not an observation, and a deadline the CALLER imposed must not be reported as "the
// peer did not answer" - those are two different facts about the world.

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
)

// classifyConnectionTestDialError maps a settled dial error onto a stage code.
//
// budgetErr is the dial context's error as it stood WHEN THE DIAL SETTLED (nil when it had not
// expired). It is consulted before err so that a budget this runner imposed is never mistaken for an
// observation about the peer:
//
//  1. our own deadline expiring is a fact about the runner - the code is budget-expired and nothing
//     is claimed about the peer, whatever error came back;
//  2. a caller cancellation is not a property of the path;
//  3. a TIMEOUT reported by the transport is the no-ack observation: the request went out and no
//     answer arrived within the time the path allows;
//  4. the explicit negatives follow, and anything left is a genuine protocol-level failure.
func classifyConnectionTestDialError(budgetErr error, err error) string {
	if err == nil {
		return connectionTestCodeOK
	}
	switch {
	case errors.Is(budgetErr, context.DeadlineExceeded):
		return connectionTestCodeDialBudgetExpire
	case errors.Is(budgetErr, context.Canceled):
		return connectionTestCodeDialCancelled
	}
	if errors.Is(err, context.Canceled) {
		return connectionTestCodeDialCancelled
	}
	var timeoutError net.Error
	if errors.As(err, &timeoutError) && timeoutError.Timeout() {
		// A transport-level timeout: something was sent and nothing came back.
		return connectionTestCodeDialNoAck
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		// A deadline error with a live budget still means the transport gave up waiting.
		return connectionTestCodeDialNoAck
	}
	switch classifyDialErrno(err) {
	case dialErrnoRefused:
		return connectionTestCodeDialRefused
	case dialErrnoReset:
		return connectionTestCodeDialReset
	case dialErrnoUnreachable:
		return connectionTestCodeDialUnreachable
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// A stream that closed without answering: an explicit "no", not a silence.
		return connectionTestCodeDialReset
	}
	return connectionTestCodeDialFailed
}
