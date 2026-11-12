package tls

// The TLS/REALITY handshake, observed for an active diagnostic.
//
// # Why this seam
//
// Every TLS-based outbound in this build ends up here: the shared `tls.Dialer` wraps an outbound
// dialer and performs the handshake on the connection it produces, and the outbounds that drive
// their own handshake call `ClientHandshake`. Instrumenting the two call sites gives the diagnostic
// the depth a client was missing - a handshake that never completed because nothing came back, one
// the peer refused, and one this DEVICE rejected because it does not trust the certificate are
// three different answers to "why did the connection fail", and none of them was visible before.
//
// # What is recorded
//
// One stage per handshake attempt: `tls`, or `reality` when the config is REALITY's. The status is
// the handshake's own outcome and the code is the fact that produced it (a timeout, a certificate
// rejection, a peer that answered with something that is not TLS, a close, a cancellation). The
// stage is recorded even when the diagnostic is not observing - the record is a context value, so
// "not observing" costs one lookup.
//
// # What is NOT recorded
//
// No key material, no certificate subject, no server name. The detail is the handshake's own error
// text, which is what a person reads; a client branches on the code.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	aTLS "github.com/sagernet/sing/common/tls"
)

// clientHandshakeWithDiagnostic performs the client handshake and records the protocol-depth stage.
//
// It is the ONE place the two handshake call sites share, so the report cannot describe a handshake
// that one of them skipped.
func clientHandshakeWithDiagnostic(ctx context.Context, conn net.Conn, config Config) (aTLS.Conn, error) {
	started := time.Now()
	tlsConn, err := aTLS.ClientHandshake(ctx, conn, config)
	adapter.RecordDiagnoseStage(ctx, handshakeStage(config, started, err))
	return tlsConn, err
}

// handshakeStage renders one handshake attempt as a diagnostic stage.
func handshakeStage(config Config, started time.Time, err error) adapter.DiagnoseStage {
	stage := adapter.DiagnoseStage{
		Stage:      adapter.DiagnoseStageTLS,
		Status:     adapter.DiagnoseStageStatusOK,
		Code:       adapter.DiagnoseCodeTLSOK,
		DurationMs: adapter.DiagnoseStageDuration(started),
	}
	if isRealityConfig(config) {
		stage.Stage = adapter.DiagnoseStageReality
	}
	if err == nil {
		stage.Detail = "the handshake completed"
		return stage
	}
	stage.Status = adapter.DiagnoseStageStatusFailed
	stage.Code = classifyHandshakeFailure(err)
	stage.Detail = err.Error()
	return stage
}

// classifyHandshakeFailure turns one handshake error into the fact it is.
//
// The order matters, and every branch reads a typed signal rather than the error's wording:
//
//  1. cancellation and deadline - OUR deadline, not the peer's answer;
//  2. a certificate this device rejected;
//  3. a peer that answered with an alert or with bytes that are not TLS;
//  4. a peer that closed or reset the connection mid-handshake;
//  5. anything else.
func classifyHandshakeFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return adapter.DiagnoseCodeTLSCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return adapter.DiagnoseCodeTLSTimeout
	}
	var timeoutError net.Error
	if errors.As(err, &timeoutError) && timeoutError.Timeout() {
		return adapter.DiagnoseCodeTLSTimeout
	}
	var verificationError *tls.CertificateVerificationError
	if errors.As(err, &verificationError) {
		return adapter.DiagnoseCodeTLSCertificate
	}
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostnameError x509.HostnameError
	if errors.As(err, &unknownAuthority) || errors.As(err, &certificateInvalid) || errors.As(err, &hostnameError) {
		return adapter.DiagnoseCodeTLSCertificate
	}
	var alertError tls.AlertError
	if errors.As(err, &alertError) {
		return adapter.DiagnoseCodeTLSProtocol
	}
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &recordHeaderError) {
		return adapter.DiagnoseCodeTLSProtocol
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return adapter.DiagnoseCodeTLSClosed
	}
	return adapter.DiagnoseCodeTLSFailed
}
