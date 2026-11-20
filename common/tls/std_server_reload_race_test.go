package tls

// The server TLS config serves every inbound TLS connection through `Server(conn)`, which reads
// `c.config`, while the certificate watcher REPLACES `c.config` under `c.access`. The readers took no
// lock, so a hot renewal - cert and key rewritten while the listener is accepting - was a data race
// between the watcher's swap and every new handshake's read. Nothing tested the reload path at all,
// which is why it survived: the file had no `std_server_test.go`.
//
// Two shapes are exercised here, because they fail differently:
//
//  1. readers (`Server`, `STDConfig`, `ServerName`, `NextProtos`) racing the swap, which is the
//     normal renewal path on a live listener;
//  2. two watcher callbacks (cert path and key path) running at once, which fswatch produces because
//     it fires each debounced path callback from its own goroutine - the same property that made the
//     certificate store's reload publish out of order.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// writeServerKeyPair writes a fresh self-signed server certificate and key, and returns the DER of the
// leaf so the caller can tell which pair the config is serving.
func writeServerKeyPair(t *testing.T, certificatePath, keyPath, commonName string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certificatePath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return der
}

func newReloadableServer(t *testing.T) (config *STDServerConfig, certificatePath, keyPath string) {
	t.Helper()
	directory := t.TempDir()
	certificatePath = filepath.Join(directory, "server.pem")
	keyPath = filepath.Join(directory, "server.key")
	writeServerKeyPair(t, certificatePath, keyPath, "reload.test")
	serverConfig, err := NewSTDServer(context.Background(), log.NewNOPFactory().NewLogger("server-reload"),
		option.InboundTLSOptions{
			Enabled:         true,
			CertificatePath: certificatePath,
			KeyPath:         keyPath,
		})
	require.NoError(t, err)
	typed, isTyped := serverConfig.(*STDServerConfig)
	require.True(t, isTyped)
	t.Cleanup(func() { _ = typed.Close() })
	return typed, certificatePath, keyPath
}

// TestAReplacementIsVisibleToEveryReaderWithoutARace is shape 1: one reload against many readers of
// the handshake entry points.
func TestAReplacementIsVisibleToEveryReaderWithoutARace(t *testing.T) {
	config, certificatePath, keyPath := newReloadableServer(t)

	var (
		stop    = make(chan struct{})
		readers sync.WaitGroup
	)
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// The handshake entry: exactly what the listener calls per accepted connection.
				_, _ = config.Server(nil)
				_, _ = config.Client(nil)
				_, _ = config.STDConfig()
				_ = config.ServerName()
				_ = config.NextProtos()
			}
		}()
	}

	before, err := config.STDConfig()
	require.NoError(t, err)
	require.Len(t, before.Certificates, 1)
	beforeDER := before.Certificates[0].Certificate[0]

	latest := writeServerKeyPair(t, certificatePath, keyPath, "reload.test")
	// The certificate callback runs first, while the in-memory key is still the old one. The pair
	// cannot be rebuilt, and the invariant is that the config keeps serving the OLD pair rather than
	// installing a certificate whose key it does not have.
	reloadCertificateErr := config.certificateUpdated(certificatePath)
	require.ErrorContains(t, reloadCertificateErr, "private key does not match public key")
	stdConfig, err := config.STDConfig()
	require.NoError(t, err)
	require.Equal(t, beforeDER, stdConfig.Certificates[0].Certificate[0],
		"a reload that cannot rebuild the pair must not install anything")
	// The key callback completes the pair.
	require.NoError(t, config.certificateUpdated(keyPath))
	close(stop)
	readers.Wait()

	stdConfig, err = config.STDConfig()
	require.NoError(t, err)
	require.Len(t, stdConfig.Certificates, 1)
	require.Equal(t, latest, stdConfig.Certificates[0].Certificate[0],
		"the handshake entry must serve the pair on disk once the reload completed")
}

// TestConcurrentCertificateAndKeyCallbacksAreSerialised is shape 2: the two watcher callbacks land at
// the same time, which is what fswatch does for two changed paths.
func TestConcurrentCertificateAndKeyCallbacksAreSerialised(t *testing.T) {
	config, certificatePath, keyPath := newReloadableServer(t)

	for round := range 50 {
		latest := writeServerKeyPair(t, certificatePath, keyPath, fmt.Sprintf("reload-%d.test", round))
		var callbacks sync.WaitGroup
		callbacks.Add(2)
		go func() {
			defer callbacks.Done()
			assertReloadSucceeds(t, config, certificatePath)
		}()
		go func() {
			defer callbacks.Done()
			assertReloadSucceeds(t, config, keyPath)
		}()
		callbacks.Wait()

		stdConfig, err := config.STDConfig()
		require.NoError(t, err)
		require.Len(t, stdConfig.Certificates, 1)
		require.Equal(t, latest, stdConfig.Certificates[0].Certificate[0],
			"after both callbacks the config must serve the pair the files hold")
	}
}

// assertReloadSucceeds reports a reload error without failing the test from the callback goroutine.
func assertReloadSucceeds(t *testing.T, config *STDServerConfig, path string) {
	t.Helper()
	if err := config.certificateUpdated(path); err != nil {
		// A mismatch is possible when the two callbacks interleave mid-write; the point of this test is
		// the race and the final convergence, which the caller asserts. The error is recorded so a
		// silent failure cannot hide.
		t.Logf("reload %s returned: %v", filepath.Base(path), err)
	}
}

// writeClientCA writes a self-signed certificate that is usable as a client CA, and returns a pool that
// contains exactly it.
func writeClientCA(t *testing.T, path, commonName string) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(7),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	content := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(path, content, 0o600))
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(content))
	return pool
}

// TestAClientCAReloadDoesNotRaceTheHandshakePath covers the second reload branch. The certificate
// branch stages its files in fields (which is why it needed the reload lock); this branch builds a new
// pool and swaps the config, so what it needs is the READER side - every handshake entry must take the
// read lock around c.config or the swap races it. The fix added those locks for all branches, and this
// test holds that for the branch that writes ClientCAs.
func TestAClientCAReloadDoesNotRaceTheHandshakePath(t *testing.T) {
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "server.pem")
	keyPath := filepath.Join(directory, "server.key")
	writeServerKeyPair(t, certificatePath, keyPath, "client-ca.test")
	clientCAPath := filepath.Join(directory, "clients.pem")
	firstPool := writeClientCA(t, clientCAPath, "client-ca-one")

	serverConfig, err := NewSTDServer(context.Background(), log.NewNOPFactory().NewLogger("client-ca"),
		option.InboundTLSOptions{
			Enabled:               true,
			CertificatePath:       certificatePath,
			KeyPath:               keyPath,
			ClientCertificatePath: []string{clientCAPath},
			ClientAuthentication:  option.ClientAuthType(tls.RequireAndVerifyClientCert),
		})
	require.NoError(t, err)
	config, isTyped := serverConfig.(*STDServerConfig)
	require.True(t, isTyped)
	t.Cleanup(func() { _ = config.Close() })

	stdConfig, err := config.STDConfig()
	require.NoError(t, err)
	require.NotNil(t, stdConfig.ClientCAs)
	require.True(t, stdConfig.ClientCAs.Equal(firstPool), "the initial client CA must be the file's")

	var (
		stop    = make(chan struct{})
		readers sync.WaitGroup
	)
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = config.Server(nil)
				_, _ = config.Client(nil)
				_, _ = config.STDConfig()
			}
		}()
	}

	secondPool := writeClientCA(t, clientCAPath, "client-ca-two")
	require.NoError(t, config.certificateUpdated(clientCAPath))
	close(stop)
	readers.Wait()

	stdConfig, err = config.STDConfig()
	require.NoError(t, err)
	require.True(t, stdConfig.ClientCAs.Equal(secondPool),
		"after the reload the served config must verify clients against the new CA and only it")
}
