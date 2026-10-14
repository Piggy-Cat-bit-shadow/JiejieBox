//go:build windows

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"golang.org/x/sys/windows"
)

// TestWinTrustErrorClassificationIsAnAllowlist pins the FIRST layer of the Authenticode path.
//
// The previous fix removed the NotBefore/NotAfter gate inside `validateUntrustedSelfSignedCertificate`
// but left `authenticodeSigner` rejecting everything except CERT_E_UNTRUSTEDROOT and CERT_E_CHAINING
// at the `WinVerifyTrustEx` call in front of it. An expired signature makes Windows report
// CERT_E_EXPIRED, so the function returned before ever reaching the inner check — the repair was
// unreachable for the very failure it was written for.
//
// This table is the contract that closes that gap, and it is deliberately an ALLOWLIST. The tension is
// real in both directions: too narrow and the product stops launching on an expired CI certificate
// again, too wide and "no signature at all" becomes an accepted signer, which would let the daemon and
// worker both present a nil signer and make the byte-equality pin compare `nil` with `nil`.
func TestWinTrustErrorClassificationIsAnAllowlist(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		err      error
		accepted bool
		why      string
	}{
		{
			name:     "no error at all",
			err:      nil,
			accepted: true,
			why:      "WinVerifyTrustEx succeeded, so nothing had to be tolerated",
		},
		{
			name:     "CERT_E_UNTRUSTEDROOT",
			err:      windows.Errno(windows.CERT_E_UNTRUSTEDROOT),
			accepted: true,
			why:      "the root is not in the machine trust store; expected for a self-signed pinned certificate",
		},
		{
			name:     "CERT_E_CHAINING",
			err:      windows.Errno(windows.CERT_E_CHAINING),
			accepted: true,
			why:      "the chain could not be built; expected for a self-signed pinned certificate",
		},
		{
			name:     "CERT_E_EXPIRED",
			err:      windows.Errno(windows.CERT_E_EXPIRED),
			accepted: true,
			why:      "THE regression: the certificate's clock window is not an identity property",
		},
		{
			name:     "CERT_E_VALIDITYPERIODNESTING",
			err:      windows.Errno(windows.CERT_E_VALIDITYPERIODNESTING),
			accepted: true,
			why:      "the same clock-window family; Windows picks among these by circumstance",
		},
		{
			name:     "TRUST_E_NOSIGNATURE",
			err:      windows.Errno(windows.TRUST_E_NOSIGNATURE),
			accepted: false,
			why:      "there is no signer to pin; accepting would make two nil signers compare equal",
		},
		{
			name:     "CERT_E_WRONG_USAGE",
			err:      windows.Errno(windows.CERT_E_WRONG_USAGE),
			accepted: false,
			why:      "the certificate is not valid for the purpose it is being used for",
		},
		{
			name:     "TRUST_E_SUBJECT_FORM_UNKNOWN",
			err:      windows.Errno(windows.TRUST_E_SUBJECT_FORM_UNKNOWN),
			accepted: false,
			why:      "the MEASURED return code for a file with no signature at all on this platform",
		},
		{
			name:     "TRUST_E_BAD_DIGEST",
			err:      windows.Errno(windows.TRUST_E_BAD_DIGEST),
			accepted: false,
			why:      "the signature is present but corrupt or does not match the file",
		},
		{
			name:     "CERT_E_REVOKED",
			err:      windows.Errno(windows.CERT_E_REVOKED),
			accepted: false,
			why:      "an explicit revocation is not a time-window artefact",
		},
		{
			name:     "CERT_E_MALFORMED",
			err:      windows.Errno(windows.CERT_E_MALFORMED),
			accepted: false,
			why:      "a malformed certificate is not a time-window artefact",
		},
		{
			name:     "unrelated errno",
			err:      windows.ERROR_ACCESS_DENIED,
			accepted: false,
			why:      "an unknown failure must fail closed, never open",
		},
		{
			name:     "arbitrary high errno",
			err:      windows.Errno(0x800BFFFF),
			accepted: false,
			why:      "an unrecognised trust-provider code must fail closed",
		},
		{
			name:     "plain non-errno error",
			err:      errors.New("some unrelated failure"),
			accepted: false,
			why:      "a non-errno error must fail closed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.accepted, isAllowedUntrustedAuthenticodeError(testCase.err),
				"%s: %s", testCase.name, testCase.why)
		})
	}
}

// TestAllowedErrorsAreExactlyTheTimeAndChainFamily is the other half of the contract: it asserts the
// allowlist has not silently grown. If a future change adds a code to the helper, this fails and
// forces the author to say which code and why, which is the conversation that must happen.
func TestAllowedErrorsAreExactlyTheTimeAndChainFamily(t *testing.T) {
	// Every CERT_E_*/TRUST_E_* code the Windows headers define in the 0x800B01xx range, plus the
	// 0x800B00xx provider codes and the digest code.
	candidates := map[string]windows.Handle{
		"TRUST_E_PROVIDER_UNKNOWN":     windows.TRUST_E_PROVIDER_UNKNOWN,
		"TRUST_E_SUBJECT_FORM_UNKNOWN": windows.TRUST_E_SUBJECT_FORM_UNKNOWN,
		"TRUST_E_BAD_DIGEST":           windows.TRUST_E_BAD_DIGEST,
		"TRUST_E_NOSIGNATURE":          windows.TRUST_E_NOSIGNATURE,
		"CERT_E_EXPIRED":               windows.CERT_E_EXPIRED,
		"CERT_E_VALIDITYPERIODNESTING": windows.CERT_E_VALIDITYPERIODNESTING,
		"CERT_E_ROLE":                  windows.CERT_E_ROLE,
		"CERT_E_PATHLENCONST":          windows.CERT_E_PATHLENCONST,
		"CERT_E_CRITICAL":              windows.CERT_E_CRITICAL,
		"CERT_E_PURPOSE":               windows.CERT_E_PURPOSE,
		"CERT_E_ISSUERCHAINING":        windows.CERT_E_ISSUERCHAINING,
		"CERT_E_MALFORMED":             windows.CERT_E_MALFORMED,
		"CERT_E_UNTRUSTEDROOT":         windows.CERT_E_UNTRUSTEDROOT,
		"CERT_E_CHAINING":              windows.CERT_E_CHAINING,
		"TRUST_E_FAIL":                 windows.TRUST_E_FAIL,
		"CERT_E_REVOKED":               windows.CERT_E_REVOKED,
		"CERT_E_UNTRUSTEDTESTROOT":     windows.CERT_E_UNTRUSTEDTESTROOT,
		"CERT_E_REVOCATION_FAILURE":    windows.CERT_E_REVOCATION_FAILURE,
		"CERT_E_CN_NO_MATCH":           windows.CERT_E_CN_NO_MATCH,
		"CERT_E_WRONG_USAGE":           windows.CERT_E_WRONG_USAGE,
		"TRUST_E_EXPLICIT_DISTRUST":    windows.TRUST_E_EXPLICIT_DISTRUST,
		"CERT_E_UNTRUSTEDCA":           windows.CERT_E_UNTRUSTEDCA,
		"CERT_E_INVALID_POLICY":        windows.CERT_E_INVALID_POLICY,
		"CERT_E_INVALID_NAME":          windows.CERT_E_INVALID_NAME,
	}
	// The complete accepted set. Anything absent here must be rejected; anything present here must
	// be accepted. Both directions are asserted, so the list cannot drift silently either way.
	accepted := map[string]bool{
		"CERT_E_EXPIRED":               true,
		"CERT_E_VALIDITYPERIODNESTING": true,
		"CERT_E_CHAINING":              true,
		"CERT_E_UNTRUSTEDROOT":         true,
	}

	acceptedCount := 0
	for name, code := range candidates {
		allowed := isAllowedUntrustedAuthenticodeError(windows.Errno(code))
		if allowed {
			acceptedCount++
		}
		if accepted[name] {
			require.True(t, allowed,
				"%s is in the time/chain family and must be allowed, but the helper rejected it", name)
		} else {
			require.False(t, allowed,
				"%s is allowed but is NOT in the time/chain family. If that is deliberate, add it to "+
					"the accepted set above and state why in the helper's comment; if it is not, the "+
					"allowlist has grown past 'ignore certificate TIME', which is the whole contract",
				name)
		}
	}
	require.Equal(t, len(accepted), acceptedCount,
		"the helper accepted a code that is not in the expected set, or missed one that is")
}

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

// TestUnsignedFileIsRejectedByTheFirstLayer walks the REAL WinVerifyTrustEx call for a file that
// carries no Authenticode signature, which is the case that must keep failing.
//
// This matters more than it looks. `authenticodeSigner` returns the signer's raw certificate bytes,
// and the daemon/worker role check compares those bytes for equality. If an unsigned file were
// tolerated here, `verifiedSignerCertificate` would find no provider certificate and both sides could
// end up presenting a nil signer — and `bytes.Equal(nil, nil)` is true, so the pin that binds a peer
// to this daemon's own certificate would compare equal to nothing at all. `UNSIGNED_PE_ALLOWED = NO`
// is therefore a security boundary, not tidiness.
//
// It exercises the first layer end to end: a real path, a real WinVerifyTrustEx call, and the real
// classification helper on whatever code Windows actually returns. On this platform an unsigned file
// yields TRUST_E_SUBJECT_FORM_UNKNOWN (0x800B0003), measured, not assumed.
func TestUnsignedFileIsRejectedByTheFirstLayer(t *testing.T) {
	unsignedPath := filepath.Join(t.TempDir(), "unsigned.bin")
	require.NoError(t, os.WriteFile(unsignedPath, []byte("this file carries no Authenticode signature"), 0o644))

	trustError := winVerifyTrustForTest(t, unsignedPath)
	require.Error(t, trustError,
		"WinVerifyTrustEx must not succeed for a file with no signature")
	t.Logf("measured WinVerifyTrustEx code for an unsigned file: %v (0x%08X)",
		trustError, uintptrOfErrno(trustError))
	require.False(t, isAllowedUntrustedAuthenticodeError(trustError),
		"an unsigned file must be rejected by the first layer; tolerating it would let both endpoints "+
			"present a nil signer and make the byte-equality pin compare nil with nil")
}

// winVerifyTrustForTest performs exactly the WinVerifyTrustEx call `authenticodeSigner` performs, and
// returns its raw error. It exists so the classification layer can be tested against the real call
// rather than only against constructed errnos.
func winVerifyTrustForTest(t *testing.T, path string) error {
	t.Helper()
	pathPointer, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	fileInformation := windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: pathPointer,
	}
	trustData := windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fileInformation),
		ProvFlags: windows.WTD_CACHE_ONLY_URL_RETRIEVAL |
			windows.WTD_REVOCATION_CHECK_NONE |
			windows.WTD_DISABLE_MD2_MD4,
		UIContext: windows.WTD_UICONTEXT_EXECUTE,
	}
	trustError := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
	trustData.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
	return trustError
}

// uintptrOfErrno renders a WinTrust error code for logging, and returns 0 when the error carries no
// errno, so the log line is never misleading.
func uintptrOfErrno(err error) uintptr {
	var errno windows.Errno
	if errors.As(err, &errno) {
		return uintptr(errno)
	}
	return 0
}

// TestSignedSystemBinaryIsAcceptedByTheFirstLayer is the positive control for the real call: a
// genuinely signed Windows binary must pass WinVerifyTrustEx outright, so a future change that made
// the first layer reject everything would fail here rather than in production.
func TestSignedSystemBinaryIsAcceptedByTheFirstLayer(t *testing.T) {
	signedPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(signedPath); err != nil {
		t.Skipf("no signed system binary available at %s: %v", signedPath, err)
	}
	trustError := winVerifyTrustForTest(t, signedPath)
	require.True(t, isAllowedUntrustedAuthenticodeError(trustError),
		"a genuinely signed system binary must be accepted by the first layer, got: %v", trustError)
}
