package certificate

// Overlapping reloads must not publish a stale trust set (audit P3, window C).
//
// The certificate store is the only owner of the process trust roots, and it reloads them from disk
// behind a file watcher. fswatch fires each debounced callback from its own timer goroutine, so two
// watched paths - or two writes to one path close enough together - run two update() calls at once.
//
// update() reads every configured file into a NEW pool and then publishes it. Without serialisation
// the two calls interleave: the one that read the files FIRST can publish LAST, and the store is then
// left holding a trust set older than the file content until some later write event happens to fix it.
// The user-visible shapes are "the CA I just added is not trusted" and, worse, "the CA I just removed
// is still trusted" - the reload did run, and it published the wrong snapshot.
//
// This test holds one reload between its read and its publish through the package's test hook, lets a
// second reload read the newer file and publish, then releases the first one. The invariant is that
// the store ends up describing the CURRENT file content, whichever order the calls were started in.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

type testCA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         []byte
}

func newTestCA(t *testing.T, commonName string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{
		certificate: certificate,
		key:         key,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// leaf signs a server certificate with this CA, which is what a pool built from it must verify.
func (ca *testCA) leaf(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf.test"},
		DNSNames:     []string{"leaf.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return certificate
}

func poolTrusts(pool *x509.CertPool, leaf *x509.Certificate) bool {
	if pool == nil {
		return false
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err == nil
}

// TestOverlappingReloadsCannotPublishAStaleTrustSet is the regression.
func TestOverlappingReloadsCannotPublishAStaleTrustSet(t *testing.T) {
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "ca.pem")
	first := newTestCA(t, "test-ca-first")
	second := newTestCA(t, "test-ca-second")
	require.NoError(t, os.WriteFile(certificatePath, first.pem, 0o600))

	store, err := NewStore(context.Background(), logger.NOP(), option.CertificateOptions{
		Store:           C.CertificateStoreNone,
		CertificatePath: []string{certificatePath},
	})
	require.NoError(t, err)
	require.True(t, poolTrusts(store.Pool(), first.leaf(t)), "the initial store must trust the initial file")
	secondLeaf := second.leaf(t)
	require.False(t, poolTrusts(store.Pool(), secondLeaf), "the initial store must not trust the second CA")

	// Hold the FIRST reload between its read and its publish.
	paused := make(chan struct{})
	release := make(chan struct{})
	var fired atomic.Bool
	testHookStoreLoad = func() {
		// CompareAndSwap, not sync.Once: Do blocks every other caller until the first body returns,
		// which would serialise the two reloads in the TEST and hide the defect it exists to pin.
		if !fired.CompareAndSwap(false, true) {
			return
		}
		close(paused)
		<-release
	}
	t.Cleanup(func() { testHookStoreLoad = nil })

	firstDone := make(chan error, 1)
	go func() { firstDone <- store.update() }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reload never reached its publish window")
	}

	// The file changes while the first reload is held, and a second reload (the second watcher
	// callback for that change) reads the new content.
	require.NoError(t, os.WriteFile(certificatePath, second.pem, 0o600))
	secondDone := make(chan error, 1)
	go func() { secondDone <- store.update() }()
	secondFinishedEarly := false
	select {
	case err = <-secondDone:
		require.NoError(t, err)
		secondFinishedEarly = true
	case <-time.After(2 * time.Second):
		// Reloads are serialised: the second one is waiting for the first. Releasing it below lets
		// both finish, and the second then reads the current file.
	}
	close(release)
	require.NoError(t, <-firstDone)
	if !secondFinishedEarly {
		select {
		case err = <-secondDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the second reload never finished")
		}
	}

	require.True(t, poolTrusts(store.Pool(), secondLeaf),
		"the store published the trust set read BEFORE the file changed over the one read after it")
}
