package adapter

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFailureFactsPreserveErrorChain(t *testing.T) {
	base := fmt.Errorf("read response: %w", io.EOF)
	facts := FailureFacts{Stage: FailureStageHandshake, Origin: FailureOriginProxy}
	wrapped := WrapFailure(base, facts)
	require.Equal(t, base.Error(), wrapped.Error())
	require.ErrorIs(t, wrapped, io.EOF)
	require.Equal(t, facts, FailureFactsOfOrZero(wrapped))

	// A second layer may add the outer fact without losing the inner one.
	outer := WrapFailure(wrapped, TargetFailureFacts())
	require.ErrorIs(t, outer, io.EOF)
	require.Equal(t, TargetFailureFacts(), FailureFactsOfOrZero(outer))
}

func TestFailureFactsDoNotInventUnknown(t *testing.T) {
	require.Nil(t, WrapFailure(nil, TargetFailureFacts()))
	plain := errors.New("plain")
	require.Same(t, plain, WrapFailure(plain, FailureFacts{}))
	_, ok := FailureFactsOf(plain)
	require.False(t, ok)

	_, ok = FailureFactsOf(errors.Join(errors.New("a"), errors.New("b")))
	require.False(t, ok)
}

func TestFailureFactsOnAggregate(t *testing.T) {
	joined := errors.Join(io.EOF, fmt.Errorf("timeout: %w", errors.New("x")))
	wrapped := WrapFailure(joined, PathFailureFacts(FailureStageDial))
	require.ErrorIs(t, wrapped, io.EOF)
	require.Equal(t, FailureOriginPath, FailureFactsOfOrZero(wrapped).Origin)
}

func FailureFactsOfOrZero(err error) FailureFacts {
	facts, _ := FailureFactsOf(err)
	return facts
}

func BenchmarkFailureFactsOf(b *testing.B) {
	plain := errors.New("connection refused")
	wrapped := WrapFailure(plain, TargetFailureFacts())
	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = FailureFactsOf(plain)
		}
	})
	b.Run("wrapped", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = FailureFactsOf(wrapped)
		}
	})
}
