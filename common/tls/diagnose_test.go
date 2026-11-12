package tls

// The protocol-depth record: what a diagnostic learns about a handshake, and what it must not claim.
//
// These tests hold two things:
//
//   - the CLASSIFICATION is a fact about a typed error, not a match on a sentence. Each case below
//     builds the error a real peer would produce and asserts the code the client will branch on;
//   - the record is only written when the handshake was really performed. A handshake against a
//     real loopback TLS server, with the device refusing its certificate and then accepting it,
//     proves both directions through the production handshake path instead of a fake.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestTheHandshakeCodeIsTheFactThatProducedIt(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		code string
	}{
		{"a certificate this device rejects", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, adapter.DiagnoseCodeTLSCertificate},
		{"a hostname that is not in the certificate", x509.HostnameError{Host: "example.com"}, adapter.DiagnoseCodeTLSCertificate},
		{"a peer that answered with an alert", tls.AlertError(40), adapter.DiagnoseCodeTLSProtocol},
		{"a peer that answered with something that is not TLS", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, adapter.DiagnoseCodeTLSProtocol},
		{"a peer that closed the connection", io.EOF, adapter.DiagnoseCodeTLSClosed},
		{"our own deadline", context.DeadlineExceeded, adapter.DiagnoseCodeTLSTimeout},
		{"the caller cancelling", context.Canceled, adapter.DiagnoseCodeTLSCancelled},
		{"anything else", errors.New("something the build has not seen"), adapter.DiagnoseCodeTLSFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.code, classifyHandshakeFailure(testCase.err))
		})
	}
}

// TestARealHandshakeAgainstALocalServerRecordsTheFact drives the production handshake helper
// against a loopback TLS server.
//
// Two directions, because one alone would pass on a build that never records anything:
//
//   - the device does not trust the server's self-signed certificate -> the stage is failed with the
//     certificate code;
//   - the same handshake with verification disabled -> the stage is ok.
func TestARealHandshakeAgainstALocalServerRecordsTheFact(t *testing.T) {
	port := startLoopbackTLSServer(t)

	verifying := newLoopbackClientConfig(t, port, false)
	stages := adapter.NewDiagnoseStages()
	ctx := adapter.WithDiagnoseStages(context.Background(), stages)
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", localPort(port)))
	require.NoError(t, err)
	_, err = ClientHandshake(ctx, conn, verifying)
	require.Error(t, err, "a self-signed certificate must not be trusted")
	_ = conn.Close()
	refused := stages.Result()
	require.Len(t, refused.Stages, 1)
	require.Equal(t, adapter.DiagnoseStageTLS, refused.Stages[0].Stage)
	require.Equal(t, adapter.DiagnoseStageStatusFailed, refused.Stages[0].Status)
	require.Equal(t, adapter.DiagnoseCodeTLSCertificate, refused.Stages[0].Code)

	accepting := newLoopbackClientConfig(t, port, true)
	acceptedStages := adapter.NewDiagnoseStages()
	acceptedCtx := adapter.WithDiagnoseStages(context.Background(), acceptedStages)
	acceptedConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", localPort(port)))
	require.NoError(t, err)
	tlsConn, err := ClientHandshake(acceptedCtx, acceptedConn, accepting)
	require.NoError(t, err)
	_ = tlsConn.Close()
	accepted := acceptedStages.Result()
	require.Len(t, accepted.Stages, 1)
	require.Equal(t, adapter.DiagnoseStageStatusOK, accepted.Stages[0].Status)
	require.Equal(t, adapter.DiagnoseCodeTLSOK, accepted.Stages[0].Code)
}

// TestNoRecorderMeansNoCost: an ordinary dial in an unobserved process records nothing and does not
// fail because there is nowhere to record.
func TestNoRecorderMeansNoCost(t *testing.T) {
	port := startLoopbackTLSServer(t)
	config := newLoopbackClientConfig(t, port, true)
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", localPort(port)))
	require.NoError(t, err)
	tlsConn, err := ClientHandshake(context.Background(), conn, config)
	require.NoError(t, err)
	require.Nil(t, adapter.DiagnoseStagesFromContext(context.Background()))
	_ = tlsConn.Close()
}

// localPort renders a port of a loopback address these tests build themselves.
func localPort(port uint16) string {
	return strconv.Itoa(int(port))
}

// newLoopbackClientConfig builds the production TLS client for 127.0.0.1:port.
func newLoopbackClientConfig(t *testing.T, port uint16, insecure bool) Config {
	t.Helper()
	config, err := NewClientWithOptions(ClientOptions{
		Context:       context.Background(),
		ServerAddress: net.JoinHostPort("127.0.0.1", localPort(port)),
		Options: option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: "localhost",
			Insecure:   insecure,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, config)
	return config
}

// startLoopbackTLSServer serves one TLS handshake at a time on a loopback port and closes.
func startLoopbackTLSServer(t *testing.T) uint16 {
	t.Helper()
	certificate := newSelfSignedCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				tlsConn, isTLS := conn.(*tls.Conn)
				if !isTLS {
					return
				}
				_ = tlsConn.HandshakeContext(context.Background())
			}(conn)
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// newSelfSignedCertificate makes the certificate a device does NOT trust.
func newSelfSignedCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "loopback"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{encoded}, PrivateKey: key}
}
