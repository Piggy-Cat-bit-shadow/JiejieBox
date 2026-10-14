//go:build windows

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// certificateWithEKU builds a genuine self-signed certificate carrying exactly the given extended
// key usages, so the code-signing check can be tested without a certificate store or signtool.
func certificateWithEKU(t *testing.T, usages []x509.ExtKeyUsage) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Jiejiebox EKU Probe"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           usages,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return der
}

// TestCodeSigningEKUIsRequired is the wrong-EKU negative test: a signer certificate that is
// otherwise perfect - genuine, self-signed, unexpired, correctly formed - must still be refused if
// its extended key usage does not permit code signing.
//
// This is the check that makes the time-window relaxation safe. "Ignore certificate time" must not
// be allowed to slide into "ignore what the certificate is FOR", and this pins that distinction.
func TestCodeSigningEKUIsRequired(t *testing.T) {
	codeSigning := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning})
	certificate, err := validateCodeSigningCertificate(codeSigning)
	require.NoError(t, err, "a code-signing EKU certificate must be accepted")
	require.NotNil(t, certificate)

	anyUsage := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageAny})
	_, err = validateCodeSigningCertificate(anyUsage)
	require.NoError(t, err, "ExtKeyUsageAny is deliberately accepted")

	// Client Authentication (1.3.6.1.5.5.7.3.2) - a real EKU, just not the right one. signtool
	// refuses to sign with such a certificate at all, which is why this is tested at this layer:
	// the refusal has to exist in OUR code too, because a file can arrive signed by something else.
	clientAuth := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	_, err = validateCodeSigningCertificate(clientAuth)
	require.Error(t, err, "a certificate without the code-signing EKU must be refused")
	require.Contains(t, err.Error(), "not valid for code signing")

	noEKU := certificateWithEKU(t, nil)
	_, err = validateCodeSigningCertificate(noEKU)
	require.Error(t, err, "a certificate with no EKU at all must be refused")

	// And a certificate that is not a certificate at all.
	_, err = validateCodeSigningCertificate([]byte("this is not DER"))
	require.Error(t, err, "a signer payload that will not parse must be refused")
}

// TestSignerPinDistinguishesTwoValidCertificates is the different-signer negative test at the layer
// where the decision is actually made.
//
// Two files, each validly signed by a different genuine self-signed code-signing certificate. Both
// pass every per-file check. The pin must still tell them apart, because the pin is what binds a
// peer to THIS daemon's certificate rather than to "some certificate that happens to be valid".
func TestSignerPinDistinguishesTwoValidCertificates(t *testing.T) {
	first := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning})
	second := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning})

	// Both are individually acceptable.
	_, err := validateCodeSigningCertificate(first)
	require.NoError(t, err)
	_, err = validateCodeSigningCertificate(second)
	require.NoError(t, err)

	// And they are still different bytes, so bytes.Equal rejects.
	require.False(t, bytes.Equal(first, second),
		"two distinct certificates must not compare equal; if they did, the pin would be a no-op")

	// The same certificate compares equal, so the pin is not simply always false.
	require.True(t, bytes.Equal(first, append([]byte(nil), first...)))
}

// TestNilSignerCannotSatisfyThePin documents the property that makes an unsigned binary safe to
// reject at the first layer rather than at the comparison.
//
// If authenticodeSigner could return (nil, nil) for an unsigned file, then two unsigned endpoints
// would both present a nil signer and bytes.Equal(nil, nil) is TRUE - the pin would silently pass.
// The defence is that every error path returns a non-nil error, so every caller returns before it
// compares. This test pins the bytes.Equal half of that argument; the "error first" half is
// asserted by TestUnsignedFileIsRejectedByTheFirstLayer, which drives the real WinVerifyTrust call.
func TestNilSignerCannotSatisfyThePin(t *testing.T) {
	var unsignedA []byte
	var unsignedB []byte
	require.True(t, bytes.Equal(unsignedA, unsignedB),
		"this is exactly why an unsigned file must never reach the comparison as a success")

	// A real signer never compares equal to nothing.
	real := certificateWithEKU(t, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning})
	require.False(t, bytes.Equal(real, nil))
	require.False(t, bytes.Equal(nil, real))
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

// fixtureCertificate returns the path to a PFX and its password for signing the test PE.
//
// It prefers a certificate supplied through the environment by CI, and only mints one itself when none
// is given. That is not laziness: creating a certificate requires spawning `powershell.exe`, and a
// `powershell.exe` spawned from this test binary behaves differently from one spawned by a shell.
// `Import-Module PKI` there reports success while `-CertStoreLocation 'Cert:\CurrentUser\My'` still
// fails with `DriveNotFound`, even though a bare subprocess launched from PowerShell can see the drive.
// CI therefore mints the certificate once in a shell that provably works and passes the PFX in.
//
// The self-minting path is kept so the test still runs on a developer machine with no setup.
func fixtureCertificate(t *testing.T) (pfxPath string, password string, cleanup func()) {
	t.Helper()
	if supplied := os.Getenv("BOXFDD_TEST_PFX"); supplied != "" {
		if _, err := os.Stat(supplied); err != nil {
			t.Fatalf("BOXFDD_TEST_PFX is set to %q but unreadable: %v", supplied, err)
		}
		return supplied, os.Getenv("BOXFDD_TEST_PFX_PASSWORD"), func() {}
	}
	thumbprint, err := createStoreCertificate(t)
	if err != nil {
		t.Skipf("SKIP (not a pass): could not create a signing certificate: %v", err)
	}
	// Export the freshly created certificate so signing can go through the same code path as the
	// supplied-PFX case.
	password = "boxdd-fixture"
	exportDir := t.TempDir()
	pfxPath = filepath.Join(exportDir, "fixture.pfx")
	script := `$ErrorActionPreference='Stop'
Import-Module PKI -ErrorAction Stop
$secure = ConvertTo-SecureString -String '` + password + `' -AsPlainText -Force
Export-PfxCertificate -Cert 'Cert:\CurrentUser\My\` + thumbprint + `' -FilePath '` + pfxPath + `' -Password $secure | Out-Null`
	scriptPath := filepath.Join(exportDir, "export-fixture-pfx.ps1")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	out, exportErr := runPowerShellFile(scriptPath)
	if exportErr != nil {
		t.Skipf("SKIP (not a pass): could not export the signing certificate (%v); powershell said: %s",
			exportErr, strings.TrimSpace(out))
	}
	return pfxPath, password, func() {}
}

// signFixturePE creates a code-signing certificate, signs a copy of a real Windows PE with it, and
// returns the signed path. It skips - never passes - when signtool or a suitable base image is
// unavailable, so the test cannot silently degrade into proving nothing.
func signFixturePE(t *testing.T) string {
	t.Helper()
	signtool := findSignTool(t)
	if signtool == "" {
		t.Skip("SKIP (not a pass): no signtool.exe found, so a real signed PE cannot be produced here")
	}

	// Any PE with room for an embedded signature works as the body being signed.
	var source string
	for _, candidate := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe"),
		filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			source = candidate
			break
		}
	}
	if source == "" {
		t.Skip("SKIP (not a pass): no suitable base PE found to sign")
	}

	directory := t.TempDir()
	signed := filepath.Join(directory, "signed-fixture.exe")
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(signed, data, 0o644))

	// `signtool remove` first, so a catalog-signed base does not leave a stale embedded signature
	// that would make the "signer is mine" assertion below pass for the wrong reason.
	_ = runSignTool(signtool, "remove", "/s", signed)

	pfxPath, password, cleanup := fixtureCertificate(t)
	defer cleanup()
	out, err := runSignToolOutput(signtool,
		"sign", "/f", pfxPath, "/p", password, "/fd", "SHA256", signed)
	if err != nil {
		t.Skipf("SKIP (not a pass): signtool sign failed (%v): %s", err, out)
	}

	// Verify the signature really is embedded and really is ours before returning.
	rawCode := winVerifyTrustForTest(t, signed)
	require.True(t, isAllowedUntrustedAuthenticodeError(rawCode),
		"the freshly signed fixture must be acceptable to the first layer, got: %v", rawCode)
	return signed
}

// TestTamperedSignedPEIsRejectedByTheFirstLayer is the integrity negative test, and it is the one
// that proves the time-window relaxation did not also relax signature integrity.
//
// A signed PE is signed, then ONE byte range inside its code section is overwritten. The file is
// still a loadable PE and its embedded signature is still present and still names the same
// certificate - only the hash no longer matches. Windows reports TRUST_E_BAD_DIGEST, which is NOT in
// the allowlist, so authenticodeSigner must refuse it.
//
// If this test ever passes with the tampered file ACCEPTED, the fix has traded authentication for
// convenience and must be reverted.
func TestTamperedSignedPEIsRejectedByTheFirstLayer(t *testing.T) {
	signed := signFixturePE(t)

	// Measure the untampered file first, so the difference is attributable to the tamper alone.
	cleanCode := winVerifyTrustForTest(t, signed)
	require.True(t, isAllowedUntrustedAuthenticodeError(cleanCode),
		"the untampered fixture must be tolerated, got: %v", cleanCode)

	tampered := signed + ".tampered"
	data, err := os.ReadFile(signed)
	require.NoError(t, err)
	require.Greater(t, len(data), 0x2000+64, "fixture too small to tamper safely")

	// Overwrite inside the mapped image, away from the headers and away from the certificate table
	// at end of file, so the PE still parses and the signature block is untouched.
	for i := 0; i < 32; i++ {
		data[0x2000+i] ^= 0xFF
	}
	require.NoError(t, os.WriteFile(tampered, data, 0o644))

	tamperedCode := winVerifyTrustForTest(t, tampered)
	var errno windows.Errno
	hasErrno := errors.As(tamperedCode, &errno)
	if hasErrno {
		t.Logf("measured WinVerifyTrustEx for a tampered signed PE: 0x%08X (%v)", uintptr(errno), errno)
	} else {
		t.Logf("measured WinVerifyTrustEx for a tampered signed PE: %v", tamperedCode)
	}

	require.Error(t, tamperedCode, "a tampered signed file must not verify")
	require.False(t, isAllowedUntrustedAuthenticodeError(tamperedCode),
		"TRUST_E_BAD_DIGEST and its relatives must never be tolerated: integrity is not a time policy")

	// And the production entry point must refuse it too, not merely the classifier.
	file, err := os.Open(tampered)
	require.NoError(t, err)
	_, signerError := authenticodeSigner(tampered, windows.Handle(file.Fd()))
	file.Close()
	require.Error(t, signerError, "authenticodeSigner must refuse a tampered signed file")
}

// createStoreCertificate creates a self-signed code-signing certificate in the user store and
// returns its SHA-1 thumbprint, or an error when the platform tooling is unavailable.
//
// Every failure path carries the POWERSHELL OUTPUT, not just the exit status. The first CI run of this
// test skipped with "could not create a signing certificate: exit status 1" and nothing else, which
// named the step and not the cause - the run had to be dug through to learn anything. A diagnostic that
// discards its own evidence is worse than no diagnostic, because it looks like one.
//
// The script is written to a FILE and run with `-File`, not passed with `-Command`. Two separate CI
// failures came out of the `-Command` form and neither was a certificate problem:
//
//  1. "Cannot find drive. A drive with the name 'Cert' does not exist" - the `Cert:` drive comes from
//     the PKI module, and `Import-Module PKI` must therefore come first. That error reads like a broken
//     certificate store rather than an unloaded module, so it is stated here explicitly.
//  2. Even WITH the import, the runner still reported the same error while the identical script
//     succeeded through `-File`. `exec.Command` passes the script as a separate argv element and
//     Windows PowerShell's `-Command` argument handling does not preserve a multi-line script that
//     reliably across hosts.
//
// `-File` has neither problem: no argument quoting, no newline handling, no drive-resolution ordering.
func createStoreCertificate(t *testing.T) (string, error) {
	t.Helper()
	script := `$ErrorActionPreference='Stop'
Import-Module PKI -ErrorAction Stop
$c = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=Jiejiebox Integration Fixture' -CertStoreLocation 'Cert:\CurrentUser\My' -NotBefore (Get-Date).AddMinutes(-5) -NotAfter (Get-Date).AddHours(2) -KeyUsage DigitalSignature -KeyExportPolicy Exportable
Write-Output $c.Thumbprint`
	scriptPath := filepath.Join(t.TempDir(), "create-fixture-certificate.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return "", err
	}
	out, err := runPowerShellFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("New-SelfSignedCertificate failed (%w); powershell said: %s", err, strings.TrimSpace(out))
	}
	// PowerShell may emit warnings around the value, so the thumbprint is the LAST non-empty line
	// rather than the whole output. Requiring the whole output to be 40 characters made a harmless
	// warning look like a broken certificate.
	var thumbprint string
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r")); trimmed != "" {
			thumbprint = trimmed
		}
	}
	if len(thumbprint) != 40 {
		return "", fmt.Errorf("unexpected thumbprint %q; powershell said: %s", thumbprint, strings.TrimSpace(out))
	}
	t.Cleanup(func() {
		// The store path is built with a backslash, so it is concatenated rather than written as a
		// single quoted literal - a Windows path inside a Go string literal is an escape hazard.
		cleanup := `Import-Module PKI -ErrorAction SilentlyContinue` + "\n" +
			`Remove-Item -LiteralPath 'Cert:\CurrentUser\My\` + thumbprint + `' -Force -ErrorAction SilentlyContinue`
		cleanupPath := filepath.Join(t.TempDir(), "remove-fixture-certificate.ps1")
		if err := os.WriteFile(cleanupPath, []byte(cleanup), 0o600); err == nil {
			_, _ = runPowerShellFile(cleanupPath)
		}
	})
	return thumbprint, nil
}

func findSignTool(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("SIGNTOOL"); configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
	}
	root := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Windows Kits", "10", "bin")
	matches, _ := filepath.Glob(filepath.Join(root, "*", "x64", "signtool.exe"))
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func runSignTool(signtool string, args ...string) error {
	_, err := runSignToolOutput(signtool, args...)
	return err
}

func runSignToolOutput(signtool string, args ...string) (string, error) {
	command := exec.Command(signtool, args...)
	output, err := command.CombinedOutput()
	return string(output), err
}

// runPowerShellFile runs a script that already exists on disk.
//
// This is the ONLY form used here, deliberately. `-Command` takes the script as an argv element and its
// multi-line handling is not preserved reliably across hosts, which cost two CI round-trips that both
// looked like certificate failures and were argument-passing failures. A file removes the question
// entirely: no argv quoting, no newline handling, no provider-drive resolution ordering.
func runPowerShellFile(scriptPath string) (string, error) {
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	output, err := command.CombinedOutput()
	return string(output), err
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
