package libbox

// Q22 鈥?the protocol-depth stages a diagnostic reports, end to end through the production dial.
//
// The reviewed gap was that the diagnostic stopped at "the dial failed": a TLS handshake the device
// rejected, a proxy that answered the CONNECT with 407, and a stream transport that never came up
// were all the same row in the report. These tests drive real outbounds against real loopback
// servers, so what they assert is the production path, not a fixture that mocks it:
//
//   - an HTTP proxy that accepts the tunnel -> http-tunnel-ok, and the dial succeeds;
//   - an HTTP proxy that refuses it -> http-tunnel-refused, and the dial fails;
//   - a TLS node whose certificate this device does not trust -> tls-certificate;
//   - the same node with verification disabled -> tls-ok;
//   - a direct dial, which performs no such layer -> ONE summary stage saying the depth was not
//     observed, never a guessed "tls: skipped".
//
// Every stage is a CHILD of the dial stage, because that is what it describes.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

// proxyConfig builds a box whose only outbound is an HTTP proxy.
func proxyConfig(host string, port uint16) string {
	return fmt.Sprintf(`{"log":{"level":"error"},
		"outbounds":[{"type":"http","tag":"proxy","server":%q,"server_port":%d}],
		"route":{"final":"proxy"}}`, host, port)
}

// trojanConfig builds a box whose only outbound is a TLS-based node on loopback.
func trojanConfig(port uint16, insecure bool) string {
	insecureField := ""
	if insecure {
		insecureField = `,"insecure":true`
	}
	return fmt.Sprintf(`{"log":{"level":"error"},
		"outbounds":[{"type":"trojan","tag":"tls-node","server":"127.0.0.1","server_port":%d,
			"password":"secret","tls":{"enabled":true,"server_name":"localhost"%s}}],
		"route":{"final":"tls-node"}}`, port, insecureField)
}

// startRefusingProxy answers every CONNECT with 407, which is a proxy that ANSWERED.
func startRefusingProxy(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
				buffer := make([]byte, 1024)
				_, _ = conn.Read(buffer)
				_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
			}(conn)
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// startDiagnoseTLSServer accepts TLS handshakes on loopback and drains what the client sends.
func startDiagnoseTLSServer(t *testing.T) uint16 {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{newDiagnoseTLSCertificate(t)},
	})
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
				if tlsConn.Handshake() != nil {
					return
				}
				// Drain whatever the protocol writes, so a successful handshake is not turned into
				// a write failure by a server that hung up first.
				_, _ = io.Copy(io.Discard, tlsConn)
			}(conn)
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// newDiagnoseTLSCertificate makes a certificate this device does not trust.
func newDiagnoseTLSCertificate(t *testing.T) tls.Certificate {
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

// protocolStage returns the first stage reported for one protocol layer.
func protocolStage(t *testing.T, report connectionTestReport, layer string) connectionTestStage {
	t.Helper()
	for _, stage := range report.Stages {
		if stage.ID == layer {
			return stage
		}
	}
	t.Fatalf("no %q stage was reported; stages: %v", layer, reportedStages(report))
	return connectionTestStage{}
}

// reportedStages lists what the report carried, for a failure message.
func reportedStages(report connectionTestReport) []string {
	stages := make([]string, 0, len(report.Stages))
	for _, stage := range report.Stages {
		stages = append(stages, stage.ID+"/"+stage.Parent+"/"+stage.Status+"/"+stage.Code)
	}
	return stages
}

// TestTheDiagnosticReportsAnAcceptedHTTPTunnel is the HTTP depth, through the production HTTP proxy
// outbound and a real CONNECT.
func TestTheDiagnosticReportsAnAcceptedHTTPTunnel(t *testing.T) {
	target := newCountingListener(t)
	proxyAddress, _ := startConnectProxyRewriter(t, target.port())
	host, port := hostPortOf(t, proxyAddress)
	server := startDiagnoseTestCommandServer(t, proxyConfig(host, port))

	report := runDiagnoseTest(t, server, "", "tcp", "http://example.com:443/path")

	tunnel := protocolStage(t, report, adapter.DiagnoseStageHTTP)
	require.Equal(t, connectionTestStageDial, tunnel.Parent, "a protocol stage describes the dial")
	require.Equal(t, connectionTestStageOK, tunnel.Status)
	require.Equal(t, adapter.DiagnoseCodeHTTPTunnelOK, tunnel.Code)
	require.Greater(t, target.count.Load(), int32(0), "the tunnel really forwarded")

	dial := stagesByID(report)[connectionTestStageDial]
	require.Equal(t, connectionTestStageOK, dial.Status)
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
}

// TestTheDiagnosticReportsAProxyRefusingTheTunnel is the fact a sentence could not carry: the proxy
// ANSWERED, and the answer was no. It is not a timeout and not a path failure.
func TestTheDiagnosticReportsAProxyRefusingTheTunnel(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, proxyConfig("127.0.0.1", startRefusingProxy(t)))

	report := runDiagnoseTest(t, server, "", "tcp", "http://example.com:443/path")

	tunnel := protocolStage(t, report, adapter.DiagnoseStageHTTP)
	require.Equal(t, connectionTestStageFailed, tunnel.Status)
	require.Equal(t, adapter.DiagnoseCodeHTTPTunnelRefused, tunnel.Code)

	dial := stagesByID(report)[connectionTestStageDial]
	require.Equal(t, connectionTestStageFailed, dial.Status)
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
}

// TestTheDiagnosticReportsACertificateTheDeviceRejects is the TLS depth on a real handshake.
func TestTheDiagnosticReportsACertificateTheDeviceRejects(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, trojanConfig(startDiagnoseTLSServer(t), false))

	report := runDiagnoseTest(t, server, "", "tcp", "http://example.com:443/path")

	tlsStage := protocolStage(t, report, adapter.DiagnoseStageTLS)
	require.Equal(t, connectionTestStageFailed, tlsStage.Status)
	require.Equal(t, adapter.DiagnoseCodeTLSCertificate, tlsStage.Code,
		"the handshake reached a certificate this device rejected; that is not a dial failure")

	dial := stagesByID(report)[connectionTestStageDial]
	require.Equal(t, connectionTestStageFailed, dial.Status)
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
}

// TestTheDiagnosticReportsASuccessfulTLSHandshake is the other direction: the layer ran and worked,
// even though the dial above it is what succeeded.
func TestTheDiagnosticReportsASuccessfulTLSHandshake(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, trojanConfig(startDiagnoseTLSServer(t), true))

	report := runDiagnoseTest(t, server, "", "tcp", "http://example.com:443/path")

	tlsStage := protocolStage(t, report, adapter.DiagnoseStageTLS)
	require.Equal(t, connectionTestStageOK, tlsStage.Status)
	require.Equal(t, adapter.DiagnoseCodeTLSOK, tlsStage.Code)

	dial := stagesByID(report)[connectionTestStageDial]
	require.Equal(t, connectionTestStageOK, dial.Status)
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
}

// TestTheDiagnosticSaysWhenNoProtocolDepthWasObserved: a direct dial performs no handshake, and the
// report says exactly that instead of inventing a skipped TLS stage for a layer that never existed.
func TestTheDiagnosticSaysWhenNoProtocolDepthWasObserved(t *testing.T) {
	target := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, routeConfig(""))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", target.port()))

	summary := protocolStage(t, report, connectionTestStageProtocol)
	require.Equal(t, connectionTestStageDial, summary.Parent)
	require.Equal(t, connectionTestStageSkipped, summary.Status)
	require.Equal(t, connectionTestCodeProtocolNotObserved, summary.Code)
	require.NotEmpty(t, summary.Detail, "an unexplained absence is what this stage exists to prevent")

	for _, stage := range report.Stages {
		require.NotContains(t, []string{adapter.DiagnoseStageTLS, adapter.DiagnoseStageReality,
			adapter.DiagnoseStageStream, adapter.DiagnoseStageHTTP}, stage.ID,
			"a layer that did not run must not appear; stages: %v", reportedStages(report))
	}
}
