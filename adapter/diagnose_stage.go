package adapter

// DiagnoseStage — the PROTOCOL-DEPTH record of one diagnostic dial.
//
// # The gap this closes
//
// A diagnostic could say "the connection failed" and, with DialPath, which object carried the dial.
// It could not say WHERE the failure happened once the transport was up: a TLS handshake the peer
// refused is a different fact from a certificate the device does not trust, and both are different
// from a successful TLS handshake followed by an HTTP CONNECT the proxy answered with 407. The
// report called all of them "dial failed", and the client had a sentence to read instead of a fact
// to branch on.
//
// # Who records, and why the seam is a context
//
// The layer that PERFORMS the handshake is the only one that knows it happened, and it is deep
// inside the outbound: the TLS client, the v2ray stream transport, the HTTP tunnel. They all
// already receive the dial's context, and none of them may depend on the diagnostic package. So the
// recorder travels in the context, exactly like DialPath, and every recording is nil-safe: with no
// recorder installed the cost is one context lookup and nothing is allocated.
//
// # What this is NOT
//
// It is not a general-purpose tracer and it does not describe the whole process. It records the
// protocol layers this build actually instruments - the TLS/REALITY handshake, the v2ray stream
// transports, the HTTP CONNECT tunnel - and nothing else. A layer that is not instrumented records
// nothing, and the report says the depth was not observed rather than inventing a stage for it.
//
// A stage is only recorded at the point where its outcome was OBSERVED: the handshake returned, the
// tunnel reply was parsed. Nothing here is inferred from a later error's wording.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Stage ids. They are stable strings because they travel through the report and the event stream.
const (
	// DiagnoseStageTLS is a TLS handshake performed by the shared TLS client.
	DiagnoseStageTLS = "tls"
	// DiagnoseStageReality is a REALITY handshake: the same TLS client, in its camouflage mode.
	DiagnoseStageReality = "reality"
	// DiagnoseStageStream is a v2ray stream transport handshake (HTTP request, WebSocket upgrade,
	// and the transports built on them).
	DiagnoseStageStream = "stream"
	// DiagnoseStageHTTP is an HTTP CONNECT tunnel (HTTP/1.1, HTTP/2 or HTTP/3).
	DiagnoseStageHTTP = "http"
)

// Stage statuses. They are the same three words the connection test already uses.
const (
	DiagnoseStageStatusOK      = "ok"
	DiagnoseStageStatusFailed  = "failed"
	DiagnoseStageStatusSkipped = "skipped"
)

// Stage codes. Each one names the fact that was observed, not a category invented later.
const (
	// The handshake completed.
	DiagnoseCodeTLSOK = "tls-ok"
	// Nothing came back inside the time allowed. The peer may be slow or the path blackholed; this
	// is not a refusal.
	DiagnoseCodeTLSTimeout = "tls-timeout"
	// The peer's certificate was rejected by this device (untrusted, mismatched or expired).
	DiagnoseCodeTLSCertificate = "tls-certificate"
	// The peer answered the handshake with something that is not an acceptable TLS answer: an
	// alert, or bytes that are not a TLS record.
	DiagnoseCodeTLSProtocol = "tls-protocol"
	// The peer closed or reset the connection during the handshake.
	DiagnoseCodeTLSClosed = "tls-closed"
	// The caller cancelled the diagnostic.
	DiagnoseCodeTLSCancelled = "tls-cancelled"
	// Anything else the handshake returned.
	DiagnoseCodeTLSFailed = "tls-failed"

	// The stream transport was established.
	DiagnoseCodeStreamOK = "stream-ok"
	// The stream transport refused or failed to establish.
	DiagnoseCodeStreamFailed = "stream-failed"
	// The caller cancelled while the stream transport was being established.
	DiagnoseCodeStreamCancelled = "stream-cancelled"

	// The HTTP tunnel was accepted.
	DiagnoseCodeHTTPTunnelOK = "http-tunnel-ok"
	// The proxy ANSWERED the CONNECT and the answer was "no" (a non-200 status, e.g. 407).
	DiagnoseCodeHTTPTunnelRefused = "http-tunnel-refused"
	// The tunnel attempt failed before a reply was read.
	DiagnoseCodeHTTPTunnelFailed = "http-tunnel-failed"
	// The caller cancelled while the tunnel was being established.
	DiagnoseCodeHTTPTunnelCancelled = "http-tunnel-cancelled"
)

// maxDiagnoseStages bounds one diagnostic's protocol-depth record.
//
// A dial can retry a handshake (an ECH retry, a group failover), and a pathological configuration
// could stack layers. The report is read on a phone, so the record is bounded and a truncated
// record SAYS it is truncated rather than silently dropping the tail.
const maxDiagnoseStages = 8

// DiagnoseStage is one observed protocol-depth fact.
type DiagnoseStage struct {
	// Stage is the layer: tls, reality, stream or http.
	Stage string
	// Status is ok, failed or skipped.
	Status string
	// Code is the stable causal code (see the constants above).
	Code string
	// Detail is the layer's own words for a reader. A client must branch on Code.
	Detail string
	// DurationMs is how long the layer took.
	DurationMs int64
}

// DiagnoseStageResult is an immutable read of one record.
type DiagnoseStageResult struct {
	Stages []DiagnoseStage
	// Truncated reports that more stages were observed than the record can carry.
	Truncated bool
}

// DiagnoseStages is one diagnostic dial's protocol-depth record. A nil *DiagnoseStages is valid
// and records nothing.
type DiagnoseStages struct {
	access    sync.Mutex
	stages    []DiagnoseStage
	truncated bool
}

// NewDiagnoseStages creates an empty record.
func NewDiagnoseStages() *DiagnoseStages {
	return &DiagnoseStages{}
}

// Record appends one observed stage, or marks the record truncated once it is full.
func (s *DiagnoseStages) Record(stage DiagnoseStage) {
	if s == nil || stage.Stage == "" {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	if len(s.stages) >= maxDiagnoseStages {
		s.truncated = true
		return
	}
	s.stages = append(s.stages, stage)
}

// Result reads the record. The returned slice is a copy: a later stage cannot rewrite a report that
// was already read.
func (s *DiagnoseStages) Result() DiagnoseStageResult {
	if s == nil {
		return DiagnoseStageResult{}
	}
	s.access.Lock()
	defer s.access.Unlock()
	result := DiagnoseStageResult{Truncated: s.truncated}
	if len(s.stages) > 0 {
		result.Stages = make([]DiagnoseStage, len(s.stages))
		copy(result.Stages, s.stages)
	}
	return result
}

// diagnoseStagesKey is unexported on purpose: a caller cannot fabricate a recorder by using a
// string key, and a stray context value cannot be mistaken for one.
type diagnoseStagesKey struct{}

// WithDiagnoseStages installs a record in the context. A nil record leaves the context unchanged,
// so a caller that decided not to observe does not have to special-case it.
func WithDiagnoseStages(ctx context.Context, stages *DiagnoseStages) context.Context {
	if stages == nil {
		return ctx
	}
	return context.WithValue(ctx, diagnoseStagesKey{}, stages)
}

// DiagnoseStagesFromContext reads the installed record, or nil when nothing is observing.
func DiagnoseStagesFromContext(ctx context.Context) *DiagnoseStages {
	if ctx == nil {
		return nil
	}
	stages, _ := ctx.Value(diagnoseStagesKey{}).(*DiagnoseStages)
	return stages
}

// RecordDiagnoseStage records one stage on the context's record, if any.
//
// This is the call every instrumented layer makes. It is safe on a nil context, on a context with
// no record, and after the record is full.
func RecordDiagnoseStage(ctx context.Context, stage DiagnoseStage) {
	DiagnoseStagesFromContext(ctx).Record(stage)
}

// DiagnoseStageDuration renders how long a layer took, in the report's unit.
func DiagnoseStageDuration(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	return time.Since(started).Milliseconds()
}

// DiagnoseStreamStage renders one v2ray stream-transport attempt.
//
// The transports share it so two of them cannot classify the same failure differently: the name is
// the transport's own ("v2ray http", "v2ray websocket"), the status is the attempt's outcome, and a
// cancellation is reported as a cancellation rather than as a transport verdict.
func DiagnoseStreamStage(name string, started time.Time, err error) DiagnoseStage {
	stage := DiagnoseStage{
		Stage:      DiagnoseStageStream,
		Status:     DiagnoseStageStatusOK,
		Code:       DiagnoseCodeStreamOK,
		Detail:     name + " stream established",
		DurationMs: DiagnoseStageDuration(started),
	}
	if err == nil {
		return stage
	}
	stage.Status = DiagnoseStageStatusFailed
	stage.Detail = err.Error()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		stage.Code = DiagnoseCodeStreamCancelled
		return stage
	}
	stage.Code = DiagnoseCodeStreamFailed
	return stage
}
