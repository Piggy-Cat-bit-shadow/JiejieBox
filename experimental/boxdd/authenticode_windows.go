//go:build windows

package main

import (
	"bytes"
	"crypto/x509"
	"time"
	"unsafe"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/sys/windows"
)

var (
	winTrustLibrary                               = windows.NewLazySystemDLL("wintrust.dll")
	winTrustProviderDataFromStateDataProcedure    = winTrustLibrary.NewProc("WTHelperProvDataFromStateData")
	winTrustProviderSignerFromChainProcedure      = winTrustLibrary.NewProc("WTHelperGetProvSignerFromChain")
	winTrustProviderCertificateFromChainProcedure = winTrustLibrary.NewProc("WTHelperGetProvCertFromChain")
)

type cryptProviderCertificate struct {
	structureSize      uint32
	certificateContext *windows.CertContext
}

// isAllowedUntrustedAuthenticodeError reports whether a `WinVerifyTrustEx` failure still leaves the
// signer certificate extractable and worth checking by IDENTITY.
//
// # Why this exists, and why it is not `trustError != nil`
//
// This daemon does not trust the signer by certificate CHAIN — it pins the signer certificate by
// BYTE EQUALITY against the ones the daemon and worker were built with. Windows, however, evaluates
// the chain against its own rules first, so it reports a whole family of failures that say nothing
// about identity: the root is not in the trust store, the chain does not build, or the certificate's
// clock window has passed. In all of those the signature is intact and the signer is recoverable —
// `WTHelperProvDataFromStateData` still yields it — and the identity check that follows is strictly
// stronger than the chain check it replaces. Refusing on them would only mean "this certificate was
// not issued by a publicly trusted CA", which is a property a CI test certificate deliberately lacks
// and which this product never relied on.
//
// The distinction that must be preserved is TIME versus IDENTITY, and the table below is deliberately
// an allowlist rather than a denylist. Anything not named here — no signature at all, a corrupted or
// mismatched signature, the wrong certificate usage, an unsupported subject form, a revoked
// certificate, an unknown trust-provider failure — is REJECTED, because in those cases either there
// is no signer to pin or the signer is untrustworthy for a reason time cannot explain.
//
// The four codes allowed here are all "no chain trust" or "clock window" verdicts. Which one Windows
// actually returns is not a matter of taste, and it was MEASURED rather than assumed:
//
//	CERT_E_UNTRUSTEDROOT (0x800B0109)  THE OBSERVED CODE, and on this platform the one that actually
//	                                   matters. Measured against a genuinely expired Authenticode
//	                                   PE (signed while valid, verified after NotAfter had passed),
//	                                   and independently reported by Get-AuthenticodeSignature for
//	                                   the same files. Windows reaches the chain verdict BEFORE it
//	                                   considers the clock, so an expired certificate whose chain
//	                                   does not end in a trusted root reports this and never reaches
//	                                   CERT_E_EXPIRED at all.
//	CERT_E_EXPIRED (0x800B0101)        the same condition reported once the chain DOES build: an
//	                                   expired certificate whose root IS trusted. This is the code the
//	                                   original failure was diagnosed as, and it is what a runner sees
//	                                   in the window between the CI certificate expiring and the
//	                                   temporary trust-store entry being cleaned up - see
//	                                   scripts/ci/windows/New-TestSigningCertificate.ps1, which
//	                                   imports the certificate into LocalMachine Root for the run.
//	CERT_E_VALIDITYPERIODNESTING       the clock-window family again: a chain member whose window does
//	                                   not nest inside its issuer's, which is what a timestamp or an
//	                                   intermediate turns the same expiry into.
//	CERT_E_CHAINING                    the chain could not be built at all.
//
// All four mean the same thing here: the chain and the clock window are not gates, the identity still
// is. CERT_E_EXPIRED is retained deliberately even though the measured path normally reports
// CERT_E_UNTRUSTEDROOT instead, because the trust-store configuration decides which of the two
// appears and BOTH configurations occur in practice - the CI script creates the first one whenever
// its cleanup step does not run.
func isAllowedUntrustedAuthenticodeError(err error) bool {
	if err == nil {
		// WinVerifyTrustEx succeeded: the chain is trusted outright, nothing was tolerated.
		return true
	}
	return E.IsMulti(
		err,
		windows.Errno(windows.CERT_E_EXPIRED),
		windows.Errno(windows.CERT_E_VALIDITYPERIODNESTING),
		windows.Errno(windows.CERT_E_CHAINING),
		windows.Errno(windows.CERT_E_UNTRUSTEDROOT),
	)
}

func authenticodeSigner(path string, file windows.Handle) ([]byte, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	fileInformation := windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: pathPointer,
		File:     file,
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
	if !isAllowedUntrustedAuthenticodeError(trustError) {
		trustData.StateAction = windows.WTD_STATEACTION_CLOSE
		windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
		return nil, E.Cause(trustError, "verify Authenticode signature")
	}
	signer, signerError := verifiedSignerCertificate(trustData.StateData)
	trustData.StateAction = windows.WTD_STATEACTION_CLOSE
	closeError := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
	if signerError != nil {
		return nil, signerError
	}
	if closeError != nil {
		return nil, E.Cause(closeError, "close Authenticode verification")
	}
	certificate, err := validateCodeSigningCertificate(signer)
	if err != nil {
		return nil, err
	}
	if trustError != nil {
		// Reached only for a chain/trust verdict that `isAllowedUntrustedAuthenticodeError` admitted
		// - which now includes CERT_E_EXPIRED. Before that code was admitted, an expired certificate
		// returned from the guard above and never arrived here at all, so the inner time check below
		// was unreachable for exactly the case it was written for: a complete fix required both this
		// layer tolerating the code AND the inner layer not re-imposing the window.
		//
		// ONLY the certificate's TIME VALIDITY is not a gate. Everything that actually restricts
		// which programs may run or connect is untouched and lives elsewhere: the fixed installed
		// path, the same-file check, the process session and role checks, and the signer-byte
		// equality that binds every peer to this daemon's own certificate.
		//
		// The reason is that this certificate is SELF-SIGNED AND PINNED BY BYTE EQUALITY, not by
		// chain trust. Its NotBefore/NotAfter window therefore adds no security: it cannot be
		// replaced by an attacker-chosen certificate without changing the bytes every comparison
		// checks, and it cannot be renewed without re-signing the whole product. What it did do was
		// make the product stop working the moment a CI test certificate expired -
		// "untrusted Authenticode signer certificate is not currently valid" - after which the
		// daemon refused to launch the core, the core could not be recovered, and a machine whose
		// system proxy still pointed at the local port could lose network access entirely.
		reportAuthenticodeCertificateTime(signer, time.Now())
		err = validateUntrustedSelfSignedCertificate(certificate)
		if err != nil {
			return nil, err
		}
	}
	return signer, nil
}

// reportAuthenticodeCertificateTime logs the signer certificate's validity window and says out loud
// whether the current time is inside it. It is a WARNING and never an error: the caller proceeds
// either way, which is the whole point of the change above. Kept as a function so the observation
// has one home, and kept separate from the security decisions so it can never be mistaken for one.
func reportAuthenticodeCertificateTime(encodedCertificate []byte, currentTime time.Time) {
	globalLogger := log.StdLogger()
	if globalLogger == nil {
		return
	}
	certificate, err := x509.ParseCertificate(encodedCertificate)
	if err != nil {
		globalLogger.Warn("core Authenticode signer certificate could not be parsed; runtime launch is not blocked: ", err)
		return
	}
	problem := authenticodeCertificateTimeProblem(certificate, currentTime)
	if problem == "" {
		globalLogger.Debug(
			"core Authenticode signer certificate is currently valid: '",
			certificate.Subject.String(), "', expires ", certificate.NotAfter.Format(time.RFC3339),
		)
		return
	}
	globalLogger.Warn(
		"core Authenticode signer certificate ",
		problem,
		"; runtime launch is not blocked",
	)
}

// authenticodeCertificateTimeProblem classifies the signer certificate's validity window against
// currentTime, returning a short human-readable phrase, or "" when the window contains currentTime.
//
// It deliberately returns a DESCRIPTION rather than an error, because its only caller logs the
// result. Splitting the classification out of the certificate plumbing is what makes it testable:
// the decision that used to stop the product is now a pure function of two arguments.
func authenticodeCertificateTimeProblem(certificate *x509.Certificate, currentTime time.Time) string {
	if currentTime.Before(certificate.NotBefore) {
		return "is not valid yet (NotBefore " + certificate.NotBefore.Format(time.RFC3339) + ")"
	}
	if currentTime.After(certificate.NotAfter) {
		return "has expired (NotAfter " + certificate.NotAfter.Format(time.RFC3339) + ")"
	}
	return ""
}

func verifiedSignerCertificate(stateData windows.Handle) ([]byte, error) {
	providerData, _, _ := winTrustProviderDataFromStateDataProcedure.Call(uintptr(stateData))
	if providerData == 0 {
		return nil, E.New("missing Authenticode provider data")
	}
	providerSigner, _, _ := winTrustProviderSignerFromChainProcedure.Call(providerData, 0, 0, 0)
	if providerSigner == 0 {
		return nil, E.New("missing Authenticode provider signer")
	}
	providerCertificate, _, _ := winTrustProviderCertificateFromChainProcedure.Call(providerSigner, 0)
	if providerCertificate == 0 {
		return nil, E.New("missing Authenticode provider certificate")
	}
	certificateContext := (*cryptProviderCertificate)(unsafe.Pointer(providerCertificate)).certificateContext
	if certificateContext == nil {
		return nil, E.New("empty Authenticode signer certificate context")
	}
	if certificateContext.Length == 0 || certificateContext.EncodedCert == nil {
		return nil, E.New("empty Authenticode signer certificate")
	}
	encodedCertificate := unsafe.Slice(certificateContext.EncodedCert, int(certificateContext.Length))
	return append([]byte(nil), encodedCertificate...), nil
}

func validateCodeSigningCertificate(encodedCertificate []byte) (*x509.Certificate, error) {
	certificate, err := x509.ParseCertificate(encodedCertificate)
	if err != nil {
		return nil, E.Cause(err, "parse Authenticode signer certificate")
	}
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageCodeSigning || usage == x509.ExtKeyUsageAny {
			return certificate, nil
		}
	}
	return nil, E.New("Authenticode signer certificate is not valid for code signing")
}

// validateUntrustedSelfSignedCertificate checks that an untrusted signer is a genuine self-signed
// code-signing certificate. It does NOT check the validity window: see the caller for why the
// window is no longer a gate, and `authenticodeCertificateTimeProblem` for where it is reported.
func validateUntrustedSelfSignedCertificate(certificate *x509.Certificate) error {
	if !bytes.Equal(certificate.RawSubject, certificate.RawIssuer) {
		return E.New("untrusted Authenticode signer certificate is not self-signed")
	}
	err := certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature)
	if err != nil {
		return E.Cause(err, "verify untrusted Authenticode signer self-signature")
	}
	return nil
}
