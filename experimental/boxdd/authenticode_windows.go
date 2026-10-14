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
	if trustError != nil && !E.IsMulti(
		trustError,
		windows.Errno(windows.CERT_E_UNTRUSTEDROOT),
		windows.Errno(windows.CERT_E_CHAINING),
	) {
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
		// ONLY the certificate's TIME VALIDITY is no longer a gate. Everything that actually
		// restricts which programs may run or connect is untouched and lives elsewhere: the fixed
		// installed path, the same-file check, the process session and role checks, and the
		// signer-byte equality that binds every peer to this daemon's own certificate.
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
