package adapter

import (
	"errors"
)

// FailureStage is WHERE an attempt failed, as far as the reporting layer can
// prove. It is deliberately coarse: a wrong stage is worse than Unknown,
// because retry and health policies read it.
type FailureStage uint8

const (
	FailureStageUnknown FailureStage = iota
	// FailureStageDial is socket establishment, DNS/address selection or the
	// underlay before the proxy protocol can speak.
	FailureStageDial
	// FailureStageHandshake is the proxy protocol's own handshake or
	// authentication exchange. It is not a TLS/QUIC library failure by itself;
	// the protocol decides whether that distinction matters.
	FailureStageHandshake
	// FailureStageTarget means the proxy path worked and the destination
	// refused, timed out or was rejected by the server's target policy.
	FailureStageTarget
	// FailureStageData is after a usable connection was handed over.
	FailureStageData
)

func (s FailureStage) String() string {
	switch s {
	case FailureStageDial:
		return "dial"
	case FailureStageHandshake:
		return "handshake"
	case FailureStageTarget:
		return "target"
	case FailureStageData:
		return "data"
	default:
		return "unknown"
	}
}

// FailureOrigin is WHO the failure belongs to.
type FailureOrigin uint8

const (
	FailureOriginUnknown FailureOrigin = iota
	// FailureOriginCaller is cancellation or deadline withdrawal by the
	// requester.
	FailureOriginCaller
	// FailureOriginLocal is this process/configuration: DNS policy, bind,
	// permission, unsupported configuration.
	FailureOriginLocal
	// FailureOriginPath is the network path to the proxy endpoint.
	FailureOriginPath
	// FailureOriginProxy is the proxy endpoint/protocol/credentials.
	FailureOriginProxy
	// FailureOriginTarget is the destination behind the proxy.
	FailureOriginTarget
)

func (o FailureOrigin) String() string {
	switch o {
	case FailureOriginCaller:
		return "caller"
	case FailureOriginLocal:
		return "local"
	case FailureOriginPath:
		return "path"
	case FailureOriginProxy:
		return "proxy"
	case FailureOriginTarget:
		return "target"
	default:
		return "unknown"
	}
}

// FailureFacts is an immutable, per-attempt explanation. It is not a health
// verdict and must not be stored on a shared outbound.
type FailureFacts struct {
	Stage  FailureStage
	Origin FailureOrigin
}

func (f FailureFacts) Known() bool {
	return f.Stage != FailureStageUnknown || f.Origin != FailureOriginUnknown
}

// FailureFactsProvider is implemented by an error type that carries facts.
// Consumers use errors.As through FailureFactsOf, so wrapping preserves the
// original error chain and existing errors.Is/As decisions.
type FailureFactsProvider interface {
	FailureFacts() FailureFacts
}

type failureError struct {
	err   error
	facts FailureFacts
}

func (e *failureError) Error() string              { return e.err.Error() }
func (e *failureError) Unwrap() error              { return e.err }
func (e *failureError) FailureFacts() FailureFacts { return e.facts }

// WrapFailure attaches facts to an error without replacing its chain. A nil
// error stays nil; a zero fact set is not attached (Unknown means "no fact",
// not a claim about the failure).
func WrapFailure(err error, facts FailureFacts) error {
	if err == nil || !facts.Known() {
		return err
	}
	return &failureError{err: err, facts: facts}
}

// FailureFactsOf returns the nearest facts in the error chain. It reports
// false when no layer attached facts, so callers keep their old errno-based
// policy rather than treating Unknown as a verdict.
func FailureFactsOf(err error) (FailureFacts, bool) {
	if err == nil {
		return FailureFacts{}, false
	}
	var provider FailureFactsProvider
	if !errors.As(err, &provider) {
		return FailureFacts{}, false
	}
	facts := provider.FailureFacts()
	if !facts.Known() {
		return FailureFacts{}, false
	}
	return facts, true
}

// Failure facts for a proxy-target error and a first-hop path error are the
// two facts the group classifiers need first. They are helpers, not a second
// policy layer: the consumer still decides retry and penalty.
func TargetFailureFacts() FailureFacts {
	return FailureFacts{Stage: FailureStageTarget, Origin: FailureOriginTarget}
}

func PathFailureFacts(stage FailureStage) FailureFacts {
	return FailureFacts{Stage: stage, Origin: FailureOriginPath}
}
