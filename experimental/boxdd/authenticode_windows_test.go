//go:build windows

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// signedCertificate builds a genuine self-signed code-signing certificate with the given validity
// window, so the tests below exercise real parsing and real signatures rather than a struct with
// fields filled in.
func signedCertificate(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Jiejiebox CI Test Code Signing"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return certificate
}

// TestExpiredCertificateIsNoLongerFatal is the regression for the failure this change exists to fix.
//
// Before: an expired signer certificate made `validateUntrustedSelfSignedCertificate` return
// "untrusted Authenticode signer certificate is not currently valid", which `authenticodeSigner`
// propagated, which made the daemon refuse to launch the core. The daemon reported
// "FATAL authenticate daemon executable: …" and the GUI showed the service as not running.
//
// After: an expired certificate produces a DESCRIPTION from `authenticodeCertificateTimeProblem`
// and the certificate is still accepted as a signer. The identity that actually restricts which
// programs may run or connect - the fixed installed path, the same-file check and the signer-byte
// equality - is untouched, because it never depended on this window.
func TestExpiredCertificateIsNoLongerFatal(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	certificate := signedCertificate(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))

	require.NoError(t, validateUntrustedSelfSignedCertificate(certificate),
		"an expired self-signed signer certificate must still be accepted")

	problem := authenticodeCertificateTimeProblem(certificate, now)
	require.NotEmpty(t, problem, "the expiry must still be reported")
	require.Contains(t, problem, "has expired")
}

// TestNotYetValidCertificateIsNoLongerFatal covers the other half of the same window, which the
// removed check also rejected. A clock skew or a not-yet-valid certificate must not stop the core
// from starting either.
func TestNotYetValidCertificateIsNoLongerFatal(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	certificate := signedCertificate(t, now.Add(24*time.Hour), now.Add(48*time.Hour))

	require.NoError(t, validateUntrustedSelfSignedCertificate(certificate))
	problem := authenticodeCertificateTimeProblem(certificate, now)
	require.Contains(t, problem, "is not valid yet")
}

// TestValidCertificateReportsNothing keeps the reporting honest in the other direction: a
// certificate inside its window must not be described as a problem, or the warning becomes noise
// that hides the real case.
func TestValidCertificateReportsNothing(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	certificate := signedCertificate(t, now.Add(-time.Hour), now.Add(time.Hour))
	require.Empty(t, authenticodeCertificateTimeProblem(certificate, now))
	require.NoError(t, validateUntrustedSelfSignedCertificate(certificate))
}

// TestTimeProblemBoundaries pins the exact edges, because an off-by-one at a boundary is precisely
// the kind of defect that would reintroduce a hard failure at one instant and nowhere else.
func TestTimeProblemBoundaries(t *testing.T) {
	notBefore := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(time.Hour)
	certificate := signedCertificate(t, notBefore, notAfter)

	require.Empty(t, authenticodeCertificateTimeProblem(certificate, notBefore),
		"the first valid instant is valid")
	require.Empty(t, authenticodeCertificateTimeProblem(certificate, notAfter),
		"the last valid instant is valid")
	require.NotEmpty(t, authenticodeCertificateTimeProblem(certificate, notBefore.Add(-time.Nanosecond)))
	require.NotEmpty(t, authenticodeCertificateTimeProblem(certificate, notAfter.Add(time.Nanosecond)))
}

// TestSelfSignedValidationStillRejectsWhatItShould confirms the parts of the check that were NOT
// removed still work. Only the time window was dropped; a certificate that is not self-signed is
// still refused, so the change is not a general "accept any certificate" relaxation.
func TestSelfSignedValidationStillRejectsWhatItShould(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Jiejiebox CI Test Code Signing"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCertificate, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	// A leaf issued by that CA is NOT self-signed.
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Jiejiebox CI Test Code Signing Leaf"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCertificate, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(leafDER)
	require.NoError(t, err)

	require.Error(t, validateUntrustedSelfSignedCertificate(leaf),
		"a certificate that is not self-signed must still be refused")
}
