package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"crypto/tls"
)

// The environment half of the benchmark: everything that must be identical for
// two clients to be comparable.
//
// The reference server is a real Xray process and both clients are real
// processes, started from generated configuration files. Nothing is stubbed and
// nothing is measured in-process, because the question ("is our XHTTP client
// cheaper than Xray's?") is about what an operator runs, not about what a unit
// test can construct.
//
// Two rules keep the comparison honest:
//
//  1. Both clients receive the same XHTTP settings, byte for byte, in their own
//     schema. A cell where our client silently gets a different mode, padding
//     range or XMUX ceiling is not a comparison.
//  2. The reference server is ONE process per environment, shared by both
//     clients, so a server-side difference cannot be mistaken for a client-side
//     win; its CPU is charged to whichever client's phase it served.

const benchUUID = "2f7c1f14-2f1f-4d1a-9a1e-6b1c0a7d5e33"

const benchServerName = "bench.local"

const benchXHTTPPath = "/bench"

// benchVerbose raises both sides' log level, which is how a cell that fails at
// the TLS or transport layer is diagnosed from its own log rather than guessed at.
var benchVerbose bool

func benchXrayLogLevel() string {
	if benchVerbose {
		return "debug"
	}
	return "warning"
}

func benchOurLogLevel() string {
	if benchVerbose {
		return "debug"
	}
	return "warn"
}

// benchEnv is the fixed part of a measurement: the transport shape under test.
type benchEnv struct {
	Mode       string // stream-one | stream-up | packet-up | auto
	Security   string // tls | reality
	Version    string // h1 | h2 | h3 —the HTTP version, forced through ALPN
	Impairment string // "" | rtt50 | rtt200 | lossy
	Xmux       string // default | single | rotate
}

func (env benchEnv) h3() bool {
	return env.Version == "h3"
}

func (env benchEnv) name() string {
	name := fmt.Sprintf("%s-%s-%s-%s", env.Security, env.Version, env.Mode, env.Xmux)
	if env.Impairment != "" {
		name += "-" + env.Impairment
	}
	return name
}

// alpnList forces the HTTP version on both sides.
//
// Both implementations decide the version by the same rule (our decideHTTPVersion
// mirrors Xray's): REALITY is always HTTP/2 and ignores ALPN, no TLS is HTTP/1.1,
// and otherwise only a SINGLE-entry ALPN of exactly "http/1.1" or "h3" moves off
// HTTP/2. So a cell that claims to measure HTTP/1.1 must configure exactly one
// entry —a list would silently be HTTP/2 on both sides and the cell would lie.
func (env benchEnv) alpnList() []string {
	switch env.Version {
	case "h1":
		return []string{"http/1.1"}
	case "h2":
		return []string{"h2"}
	case "h3":
		return []string{"h3"}
	default:
		return nil
	}
}

// xmuxBlock is the XMUX section in our client's schema. An empty map would take
// Xray's all-or-nothing defaults, so "default" emits no section at all.
func (env benchEnv) xmuxBlock() map[string]any {
	switch env.Xmux {
	case "single":
		return map[string]any{"max_connections": "1-1"}
	case "rotate":
		return map[string]any{"h_max_request_times": "5-5"}
	default:
		return nil
	}
}

// xmuxBlockXray is the same section in Xray's camelCase schema.
func (env benchEnv) xmuxBlockXray() map[string]any {
	switch env.Xmux {
	case "single":
		return map[string]any{"maxConnections": "1-1"}
	case "rotate":
		return map[string]any{"hMaxRequestTimes": "5-5"}
	default:
		return nil
	}
}

// benchPaths is where one environment's artifacts live.
type benchPaths struct {
	Dir       string
	ServerLog string
	OursLog   string
	XrayLog   string
	TargetLog string
	ShimLog   string
	CertFile  string
	KeyFile   string
	CAFile    string
	CoverLog  string
}

// benchPorts are the loopback ports one environment uses.
type benchPorts struct {
	Server int // the reference server (or the shim, when impaired)
	Shim   int
	Ours   int // our client's SOCKS inbound
	Xray   int // Xray client's SOCKS inbound
	Cover  int // REALITY camouflage TLS server
	Target int
}

// freePort asks the kernel for an unused loopback port. The listener is closed
// immediately: a port observed free and used a moment later is exactly what a
// benchmark needs, and a collision surfaces as a failed readiness probe rather
// than as a wrong number.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

func allocatePorts() (benchPorts, error) {
	ports := benchPorts{}
	var err error
	if ports.Server, err = freePort(); err != nil {
		return ports, err
	}
	if ports.Shim, err = freePort(); err != nil {
		return ports, err
	}
	if ports.Ours, err = freePort(); err != nil {
		return ports, err
	}
	if ports.Xray, err = freePort(); err != nil {
		return ports, err
	}
	if ports.Cover, err = freePort(); err != nil {
		return ports, err
	}
	if ports.Target, err = freePort(); err != nil {
		return ports, err
	}
	return ports, nil
}

// benchCertificates are the three files a TLS cell needs.
//
// A real CA signs a real server certificate, and BOTH clients verify the chain
// against that CA. Two shortcuts were available and both were rejected:
// disabling verification (which the reference no longer even accepts —// `allowInsecure` was removed in favour of pinning) and pinning the leaf hash.
// Chain verification is what a deployment does, it costs both clients the same
// work, and it keeps a cell from measuring a client that skipped a step.
type benchCertificates struct {
	CAFile     string
	ServerCert string
	ServerKey  string
}

func ensureCertificates(directory string) (benchCertificates, error) {
	certificates := benchCertificates{
		CAFile:     filepath.Join(directory, "bench-ca.crt"),
		ServerCert: filepath.Join(directory, "bench-server.crt"),
		ServerKey:  filepath.Join(directory, "bench-server.key"),
	}
	if fileExists(certificates.CAFile) && fileExists(certificates.ServerCert) && fileExists(certificates.ServerKey) {
		return certificates, nil
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificates, err
	}
	caSerial, err := randomSerial()
	if err != nil {
		return certificates, err
	}
	caTemplate := x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "sing-box xhttp benchmark CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return certificates, err
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return certificates, err
	}
	if err = writePEM(certificates.CAFile, "CERTIFICATE", caDER); err != nil {
		return certificates, err
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificates, err
	}
	serverSerial, err := randomSerial()
	if err != nil {
		return certificates, err
	}
	serverTemplate := x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: benchServerName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{benchServerName, "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, &serverTemplate, caCertificate, &serverKey.PublicKey, caKey)
	if err != nil {
		return certificates, err
	}
	if err = writePEM(certificates.ServerCert, "CERTIFICATE", serverDER); err != nil {
		return certificates, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return certificates, err
	}
	return certificates, writePEM(certificates.ServerKey, "EC PRIVATE KEY", keyBytes)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func writePEM(path string, blockType string, der []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err = pem.Encode(file, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// benchRealityKeys is one matched REALITY triple, in the base64url-without-padding
// form both implementations expect.
type benchRealityKeys struct {
	PublicKey  string
	PrivateKey string
	ShortID    string
}

func generateRealityKeys() (benchRealityKeys, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return benchRealityKeys{}, err
	}
	shortID := make([]byte, 8)
	if _, err = rand.Read(shortID); err != nil {
		return benchRealityKeys{}, err
	}
	return benchRealityKeys{
		PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		PrivateKey: base64.RawURLEncoding.EncodeToString(privateKey.Bytes()),
		ShortID:    hex.EncodeToString(shortID),
	}, nil
}

// writeJSONConfig renders one generated configuration file.
func writeJSONConfig(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}

// xrayServerConfig builds the reference server: VLESS + XHTTP, TLS or REALITY,
// with the same mode, path and path-level options as the clients.
func xrayServerConfig(env benchEnv, ports benchPorts, paths benchPaths, keys benchRealityKeys, targetAddress string) map[string]any {
	streamSettings := map[string]any{
		"network": "xhttp",
		"xhttpSettings": map[string]any{
			"path": benchXHTTPPath,
			"mode": env.Mode,
		},
	}
	switch {
	case env.Security == "reality":
		streamSettings["security"] = "reality"
		streamSettings["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        fmt.Sprintf("127.0.0.1:%d", ports.Cover),
			"xver":        0,
			"serverNames": []string{benchServerName},
			"privateKey":  keys.PrivateKey,
			"shortIds":    []string{keys.ShortID},
		}
	default:
		streamSettings["security"] = "tls"
		tlsSettings := map[string]any{
			"certificates": []map[string]any{{
				"certificateFile": paths.CertFile,
				"keyFile":         paths.KeyFile,
			}},
		}
		// The server advertises exactly the version under test: for H3 the ALPN
		// is also what makes the reference bind UDP instead of TCP.
		if alpn := env.alpnList(); len(alpn) > 0 {
			tlsSettings["alpn"] = alpn
		}
		streamSettings["tlsSettings"] = tlsSettings
	}
	return map[string]any{
		"log": map[string]any{"loglevel": benchXrayLogLevel()},
		"inbounds": []map[string]any{{
			"listen":   "127.0.0.1",
			"port":     ports.Server,
			"protocol": "vless",
			"settings": map[string]any{
				"decryption": "none",
				"clients":    []map[string]any{{"id": benchUUID}},
			},
			"streamSettings": streamSettings,
		}},
		"outbounds": []map[string]any{{
			"protocol": "freedom",
			"tag":      "direct",
			// The reference blocks private destinations by default behind a
			// proxied inbound, and the target is loopback by design. Without
			// this rule every cell fails for a reason unrelated to the
			// transport.
			"settings": map[string]any{
				"finalRules": []map[string]any{{
					"action": "allow",
					"ip":     []string{"127.0.0.0/8", "::1/128"},
				}},
			},
		}},
	}
}

// oursClientConfig builds our client: a mixed inbound whose only outbound is the
// XHTTP tunnel.
func oursClientConfig(env benchEnv, listenPort int, serverPort int, xmux map[string]any, keys benchRealityKeys, caFile string) map[string]any {
	transport := map[string]any{
		"type": "xhttp",
		"path": benchXHTTPPath,
		"mode": env.Mode,
	}
	if xmux != nil {
		transport["xmux"] = xmux
	}
	outbound := map[string]any{
		"type":        "vless",
		"tag":         "tunnel",
		"server":      "127.0.0.1",
		"server_port": serverPort,
		"uuid":        benchUUID,
		"transport":   transport,
	}
	if env.Security == "reality" {
		outbound["tls"] = map[string]any{
			"enabled":     true,
			"server_name": benchServerName,
			"utls":        map[string]any{"enabled": true, "fingerprint": "chrome"},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": keys.PublicKey,
				"short_id":   keys.ShortID,
			},
		}
	} else {
		tlsBlock := map[string]any{
			"enabled":          true,
			"server_name":      benchServerName,
			"certificate_path": caFile,
		}
		if alpn := env.alpnList(); len(alpn) > 0 {
			tlsBlock["alpn"] = alpn
		}
		outbound["tls"] = tlsBlock
	}
	return map[string]any{
		"log": map[string]any{"level": benchOurLogLevel(), "timestamp": true},
		"inbounds": []map[string]any{{
			"type":        "mixed",
			"tag":         "in",
			"listen":      "127.0.0.1",
			"listen_port": listenPort,
		}},
		"outbounds": []map[string]any{outbound},
		"route":     map[string]any{"final": "tunnel"},
	}
}

// xrayClientConfig builds the reference CLIENT with the same transport shape.
func xrayClientConfig(env benchEnv, listenPort int, serverPort int, xmux map[string]any, keys benchRealityKeys, caFile string) map[string]any {
	xhttpSettings := map[string]any{
		"path": benchXHTTPPath,
		"mode": env.Mode,
	}
	if xmux != nil {
		xhttpSettings["xmux"] = xmux
	}
	streamSettings := map[string]any{
		"network":       "xhttp",
		"xhttpSettings": xhttpSettings,
		"security":      "tls",
	}
	if env.Security == "reality" {
		streamSettings["security"] = "reality"
		streamSettings["realitySettings"] = map[string]any{
			"serverName":  benchServerName,
			"publicKey":   keys.PublicKey,
			"shortId":     keys.ShortID,
			"fingerprint": "chrome",
			"show":        false,
		}
	} else {
		// The reference removed allowInsecure; a CA loaded with usage "verify"
		// is the supported way to trust this harness's own certificate, and
		// disableSystemRoot keeps the trust decision identical to our client's
		// certificate_path —one CA, no ambient machine state.
		tlsSettings := map[string]any{
			"serverName":        benchServerName,
			"disableSystemRoot": true,
			"certificates": []map[string]any{{
				"usage":           "verify",
				"certificateFile": caFile,
			}},
		}
		if alpn := env.alpnList(); len(alpn) > 0 {
			tlsSettings["alpn"] = alpn
		}
		streamSettings["tlsSettings"] = tlsSettings
	}
	return map[string]any{
		"log": map[string]any{"loglevel": benchXrayLogLevel()},
		"inbounds": []map[string]any{{
			"listen":   "127.0.0.1",
			"port":     listenPort,
			"protocol": "socks",
			"settings": map[string]any{"udp": false, "auth": "noauth"},
		}},
		"outbounds": []map[string]any{{
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []map[string]any{{
					"address": "127.0.0.1",
					"port":    serverPort,
					"users":   []map[string]any{{"id": benchUUID, "encryption": "none"}},
				}},
			},
			"streamSettings": streamSettings,
		}},
	}
}

// benchProcess is one child process with its log.
type benchProcess struct {
	Name string
	cmd  *exec.Cmd
	PID  int
	Log  string
	file *os.File
}

func startBenchProcess(name string, executable string, arguments []string, logPath string, affinity uintptr) (*benchProcess, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, arguments...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err = command.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	process := &benchProcess{Name: name, cmd: command, PID: command.Process.Pid, Log: logPath, file: logFile}
	if affinity != 0 {
		// Best effort: an unpinned process is a noisier measurement, not a
		// failed one, and saying so is better than failing the run.
		_ = configureProcess(process.PID, affinity, priorityHigh)
	}
	return process, nil
}

func (process *benchProcess) Stop(grace time.Duration) {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return
	}
	_ = process.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = process.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		_ = process.cmd.Process.Kill()
		<-done
	}
	if process.file != nil {
		_ = process.file.Close()
	}
}

// tailLog returns the last lines of a process log, which is what makes a failed
// cell diagnosable instead of mysterious.
func tailLog(path string, lines int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(content)
	start := len(text)
	for count := 0; count < lines && start > 0; count++ {
		start--
		for start > 0 && text[start-1] != '\n' {
			start--
		}
	}
	return text[start:]
}

// runSelf executes this same binary with a subcommand and returns stdout.
func runSelf(ctx context.Context, args ...string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.SysProcAttr = backgroundProcessAttributes()
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(output), fmt.Errorf("%s: %w: %s", args[0], err, string(exitErr.Stderr))
		}
		return string(output), err
	}
	return string(output), nil
}

// startSelf starts this binary as a long-lived child (the target, the shim and
// the camouflage server are all subcommands).
func startSelf(name string, logPath string, affinity uintptr, args ...string) (*benchProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startBenchProcess(name, executable, args, logPath, affinity)
}

// loadCertificate is used by the camouflage TLS server.
func loadCertificate(certFile string, keyFile string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(certFile, keyFile)
}
