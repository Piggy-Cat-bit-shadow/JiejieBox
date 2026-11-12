package schannel

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	E "github.com/sagernet/sing/common/exceptions"
)

// ErrVersionUnavailable reports that the caller REQUIRED a TLS version this
// machine cannot negotiate at all.
//
// It is separate from ErrVersionNotNegotiated on purpose. Both are refusals,
// but they answer different questions: "can this install ever speak TLS 1.3"
// (a capability of the OS, testable before a single byte leaves the process)
// versus "did the peer agree to the version this caller required" (a fact that
// only exists after the handshake). Collapsing them into one error would make
// an operator read "the server refused us" when the truth is "this Windows
// build has no TLS 1.3".
var ErrVersionUnavailable = E.New("schannel: the requested TLS version is unavailable on this machine")

// ErrVersionNotNegotiated reports that a handshake completed at a TLS version
// outside the range the caller asked for.
var ErrVersionNotNegotiated = E.New("schannel: negotiated TLS version is outside the requested range")

// tls13MinimumBuild is the first Windows build whose Schannel offers TLS 1.3 to
// stream clients (Windows 10 Insider build 20152; generally available in
// Windows 11 build 22000 and Windows Server 2022 build 20348).
//
// It is used only to decide whether a negative capability probe is conclusive.
// Builds at or above it may have TLS 1.3 enabled or disabled by policy, so the
// probe answer alone is not grounds for refusal there.
const tls13MinimumBuild = 20152

// tls13ProbeMask enables exactly the two stream protocols that matter for the
// TLS 1.3 capability probe: TLS 1.2 and TLS 1.3.
//
// TLS 1.2 is left enabled because a mask that leaves TLS 1.3 as the only
// stream protocol makes Schannel emit a DTLS-shaped ClientHello (record
// version 0xfefd, epoch and sequence fields) on a stream socket, which every
// TLS peer rejects and this package then surfaced as "unexpected EOF". The
// probe must therefore ask the same question a working handshake asks: "given
// a TLS hello, does this machine advertise TLS 1.3 in supported_versions?"
var tls13ProbeMask = uint32(spProtAllTLSClients &^ (spProtTLS12Client | spProtTLS13Client))

// tls13Support caches the probe result for the lifetime of the process.
//
// The probe is an SSPI call, not a network round trip: it builds a credential,
// asks Schannel for one ClientHello and inspects the offered versions. That
// costs well under a millisecond, but resolving it once keeps every
// NewClientContext call free while the OS state it reads (build, registry
// policy) is constant for the process.
var tls13Support struct {
	once      sync.Once
	supported bool
	probeErr  error
}

// tls13SupportOverride is the test seam for the capability oracle.
//
// The capability is a property of the machine, not of this package's logic, so
// the only way to exercise BOTH branches of the production handshake path on a
// single machine is to substitute the answer. Production code never sets it;
// tests restore it through setTLS13Support.
var tls13SupportOverride struct {
	access sync.RWMutex
	value  *bool
}

// TLS13Supported reports whether Schannel on this machine can negotiate
// TLS 1.3 for a stream client.
//
// The answer comes from asking Schannel itself instead of comparing the build
// number with a table: TLS 1.3 availability depends on the build, the edition,
// and registry policy, and a table would either refuse a machine that works or
// accept one that cannot.
func TLS13Supported() bool {
	tls13SupportOverride.access.RLock()
	override := tls13SupportOverride.value
	tls13SupportOverride.access.RUnlock()
	if override != nil {
		return *override
	}
	return probeTLS13Support()
}

// TLS13ProbeError reports why the capability probe failed, if it did.
//
// A probe failure is not the same as "unsupported": it means the platform could
// not be asked, and CheckRequestedVersion then refuses to promise TLS 1.3
// rather than pretending either way.
func TLS13ProbeError() error {
	probeTLS13Support()
	return tls13Support.probeErr
}

func probeTLS13Support() bool {
	tls13Support.once.Do(func() {
		tls13Support.supported, tls13Support.probeErr = probeTLS13Hello()
	})
	return tls13Support.supported
}

// probeTLS13Hello builds one ClientHello with TLS 1.2 and 1.3 enabled and
// reports whether it advertises TLS 1.3 in the supported_versions extension.
func probeTLS13Hello() (bool, error) {
	err := CheckPlatform()
	if err != nil {
		return false, err
	}
	credential, err := acquireClientCredential(tls13ProbeMask)
	if err != nil {
		return false, err
	}
	defer credential.free()
	hello, err := credential.clientHello()
	if err != nil {
		return false, err
	}
	if looksDTLS(hello) {
		// This machine answered a stream hello with a datagram record. It is a
		// platform defect this package works around elsewhere, but it must not
		// be read as "TLS 1.3 supported".
		return false, E.New("schannel: capability probe produced a DTLS-shaped ClientHello")
	}
	return offersTLS13(hello), nil
}

// CheckRequestedVersion refuses a request whose REQUIRED version this machine
// cannot negotiate.
//
// Only a minimum of TLS 1.3 is refused. A caller that asks for "TLS 1.2 up to
// TLS 1.3" has a working fallback, so it is admitted and the negotiated version
// is checked afterwards; a caller that requires TLS 1.3 has no fallback and
// must fail before writing a hello the peer can only reject.
func CheckRequestedVersion(minVersion, maxVersion uint16) error {
	effectiveMin, _, err := effectiveVersionRange(minVersion, maxVersion)
	if err != nil {
		return err
	}
	if effectiveMin < tls.VersionTLS13 {
		return nil
	}
	if TLS13Supported() {
		return nil
	}
	if probeErr := TLS13ProbeError(); probeErr != nil {
		// "Could not ask" is not "no". The handshake may still negotiate TLS 1.3,
		// and CheckNegotiatedVersion refuses precisely if it does not, so refusing
		// here would turn a probe failure into a false negative.
		return nil
	}
	if windowsBuildNumber() >= tls13MinimumBuild {
		// The probe found no TLS 1.3 offer, but the build is new enough that
		// policy (registry, TLS 1.3 enable/disable) can differ from the default.
		// Let the handshake decide and enforce the requirement afterwards.
		return nil
	}
	return E.Cause(ErrVersionUnavailable, fmt.Sprintf(
		"requested minimum %s, but this Windows build (%d) predates Schannel TLS 1.3 client support (build %d)",
		VersionName(tls.VersionTLS13), windowsBuildNumber(), tls13MinimumBuild))
}

// CheckNegotiatedVersion refuses a completed handshake whose version is outside
// the requested range.
//
// It is the half that a capability check cannot cover: a machine that speaks
// TLS 1.3 can still be answered with TLS 1.2 by a peer, and silently accepting
// that would downgrade a caller who required TLS 1.3.
func CheckNegotiatedVersion(negotiated, minVersion, maxVersion uint16) error {
	effectiveMin, effectiveMax, err := effectiveVersionRange(minVersion, maxVersion)
	if err != nil {
		return err
	}
	if negotiated == 0 {
		return E.New("schannel: handshake reported no negotiated TLS version")
	}
	if negotiated < effectiveMin {
		return E.Cause(ErrVersionNotNegotiated, fmt.Sprintf("negotiated %s, but the requested minimum is %s",
			VersionName(negotiated), VersionName(effectiveMin)))
	}
	if negotiated > effectiveMax {
		return E.Cause(ErrVersionNotNegotiated, fmt.Sprintf("negotiated %s, but the requested maximum is %s",
			VersionName(negotiated), VersionName(effectiveMax)))
	}
	return nil
}

// effectiveVersionRange turns the "zero means default" option range into the
// explicit bounds this package enforces.
//
// The defaults have to match disabledProtocolsMask, otherwise the mask could
// allow a version the post-handshake check refuses (or the reverse). They are
// derived here once and shared by both.
func effectiveVersionRange(minVersion, maxVersion uint16) (uint16, uint16, error) {
	if minVersion != 0 && maxVersion != 0 && minVersion > maxVersion {
		return 0, 0, E.New("schannel: minimum TLS version ", VersionName(minVersion), " is above maximum ", VersionName(maxVersion))
	}
	effectiveMin := minVersion
	if effectiveMin == 0 {
		effectiveMin = tls.VersionTLS12
		if maxVersion != 0 && maxVersion < tls.VersionTLS12 {
			effectiveMin = tls.VersionTLS10
		}
	}
	effectiveMax := maxVersion
	if effectiveMax == 0 {
		effectiveMax = tls.VersionTLS13
	}
	return effectiveMin, effectiveMax, nil
}

// VersionName renders a TLS version the way an operator reads it.
func VersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("TLS version 0x%04x", version)
	}
}

// looksDTLS reports whether a record uses the DTLS wire version.
//
// DTLS records carry ten extra bytes (epoch and sequence) and version 0xfeXX;
// the shape is the fingerprint of the Schannel defect this package works
// around, so tests assert on it directly instead of on a downstream EOF.
func looksDTLS(record []byte) bool {
	return len(record) >= 3 && record[0] == 0x16 && record[1] == 0xfe
}

// offersTLS13 parses a TLS ClientHello and reports whether supported_versions
// lists TLS 1.3.
func offersTLS13(hello []byte) bool {
	for _, version := range supportedVersions(hello) {
		if version == tls.VersionTLS13 {
			return true
		}
	}
	return false
}

// supportedVersions returns the versions listed in the ClientHello's
// supported_versions extension (RFC 8446 §4.2.1), or the client_version field
// when the extension is absent.
func supportedVersions(hello []byte) []uint16 {
	// TLS plaintext record: type(1) version(2) length(2).
	if len(hello) < 5 || hello[0] != 0x16 {
		return nil
	}
	recordLength := int(binary.BigEndian.Uint16(hello[3:5]))
	body := hello[5:]
	if recordLength < len(body) {
		body = body[:recordLength]
	}
	// Handshake: type(1) length(3).
	if len(body) < 4 || body[0] != 0x01 {
		return nil
	}
	handshakeLength := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	message := body[4:]
	if handshakeLength < len(message) {
		message = message[:handshakeLength]
	}
	// ClientHello: version(2) random(32) session_id(1+n) cipher_suites(2+n)
	// compression_methods(1+n) extensions(2+n).
	if len(message) < 2+32+1 {
		return nil
	}
	legacyVersion := binary.BigEndian.Uint16(message[0:2])
	offset := 2 + 32
	sessionIDLength := int(message[offset])
	offset += 1 + sessionIDLength
	if len(message) < offset+2 {
		return []uint16{legacyVersion}
	}
	cipherSuitesLength := int(binary.BigEndian.Uint16(message[offset : offset+2]))
	offset += 2 + cipherSuitesLength
	if len(message) < offset+1 {
		return []uint16{legacyVersion}
	}
	compressionLength := int(message[offset])
	offset += 1 + compressionLength
	if len(message) < offset+2 {
		return []uint16{legacyVersion}
	}
	extensionsLength := int(binary.BigEndian.Uint16(message[offset : offset+2]))
	offset += 2
	extensionsEnd := offset + extensionsLength
	if extensionsEnd > len(message) {
		extensionsEnd = len(message)
	}
	for offset+4 <= extensionsEnd {
		extensionType := binary.BigEndian.Uint16(message[offset : offset+2])
		extensionLength := int(binary.BigEndian.Uint16(message[offset+2 : offset+4]))
		offset += 4
		extensionEnd := offset + extensionLength
		if extensionEnd > extensionsEnd {
			return []uint16{legacyVersion}
		}
		if extensionType != extensionSupportedVersions {
			offset = extensionEnd
			continue
		}
		payload := message[offset:extensionEnd]
		if len(payload) < 1 {
			return []uint16{legacyVersion}
		}
		listLength := int(payload[0])
		if listLength+1 > len(payload) {
			return []uint16{legacyVersion}
		}
		versions := make([]uint16, 0, listLength/2)
		for index := 1; index+1 < len(payload) && index+1 <= listLength; index += 2 {
			versions = append(versions, binary.BigEndian.Uint16(payload[index:index+2]))
		}
		if len(versions) == 0 {
			return []uint16{legacyVersion}
		}
		return versions
	}
	return []uint16{legacyVersion}
}

const extensionSupportedVersions = 0x002b

// windowsBuildNumber is the 16-bit build component RtlGetNtVersionNumbers reports.
//
// Only this component is comparable with tls13MinimumBuild; the number carries flag bits above
// 0xffff, which is why they are masked off. There is no separate branch for a future major version:
// the build numbering is what the TLS 1.3 threshold is stated in, and inventing a second scale for
// an unobserved major would just be a guess.
func windowsBuildNumber() uint32 {
	_, _, build := windows.RtlGetNtVersionNumbers()
	return build & 0xffff
}

// setTLS13Support overrides the capability oracle for one test and restores the
// previous value when the returned function is called.
func setTLS13Support(supported bool) func() {
	tls13SupportOverride.access.Lock()
	previous := tls13SupportOverride.value
	tls13SupportOverride.value = &supported
	tls13SupportOverride.access.Unlock()
	return func() {
		tls13SupportOverride.access.Lock()
		tls13SupportOverride.value = previous
		tls13SupportOverride.access.Unlock()
	}
}

// SetTLS13SupportForTest substitutes the TLS 1.3 capability oracle and returns
// the function that restores the previous answer.
//
// It exists because the capability is a fact about the host: without it, the
// branch that refuses a TLS 1.3 requirement can only be exercised on a machine
// without TLS 1.3, and the branch that lets TLS 1.3 through only on a machine
// with it. A test suite that has to be run twice on two operating systems to
// see both halves of its own code is a suite that will be run on one. Tests
// call this; production code does not.
func SetTLS13SupportForTest(supported bool) func() {
	return setTLS13Support(supported)
}

// credentialHandle owns one short-lived Schannel credential.
//
// Client credentials are otherwise cached for the life of the process (see
// clientCredentialCache). The capability probe is a one-shot question, so it
// frees both the security context it creates and the credential, leaving no
// handle behind for a probe that is never asked again.
type credentialHandle struct {
	handle secHandle
	valid  bool
}

func acquireClientCredential(disabledProtocols uint32) (*credentialHandle, error) {
	tlsParams := tlsParameters{grbitDisabledProtocols: disabledProtocols}
	sch := schCredentials{
		dwVersion:      schCredentialsVersion,
		dwFlags:        clientCredentialFlags,
		cTlsParameters: 1,
		pTlsParameters: &tlsParams,
	}
	pkg, err := windows.UTF16PtrFromString(unispNameW)
	if err != nil {
		return nil, err
	}
	var expiry windows.Filetime
	handle := &credentialHandle{}
	status := sspiAcquireCredentialsHandle(
		nil,
		pkg,
		secPkgCredOutbound,
		nil,
		unsafe.Pointer(&sch),
		0,
		0,
		&handle.handle,
		&expiry,
	)
	if status != secEOK {
		return nil, sspiError("AcquireCredentialsHandle", status)
	}
	handle.valid = true
	return handle, nil
}

// clientHello asks Schannel for the first handshake token of a fresh context
// and returns it, discarding the context.
func (c *credentialHandle) clientHello() ([]byte, error) {
	targetName, err := windows.UTF16PtrFromString("localhost")
	if err != nil {
		return nil, err
	}
	var context secHandle
	var outputBufs [1]secBuffer
	outputBufs[0].bufferType = secbufferToken
	outputDesc := secBufferDesc{ulVersion: secbufferVersion, cBuffers: 1, pBuffers: &outputBufs[0]}
	var contextAttr uint32
	var expiry windows.Filetime
	status := sspiInitializeSecurityContext(
		&c.handle,
		nil,
		targetName,
		handshakeContextReq,
		0,
		0,
		nil,
		0,
		&context,
		&outputDesc,
		&contextAttr,
		&expiry,
	)
	var output []byte
	if outputBufs[0].cbBuffer > 0 && outputBufs[0].pvBuffer != nil {
		output = unsafeSliceCopy(outputBufs[0].pvBuffer, int(outputBufs[0].cbBuffer))
		sspiFreeContextBuffer(outputBufs[0].pvBuffer)
	}
	switch status {
	case secEOK, secICompleteNeeded, secICompleteAndContinue, secIContinueNeeded:
		sspiDeleteSecurityContext(&context)
	case secEIncompleteMessage:
		sspiDeleteSecurityContext(&context)
	default:
		if len(output) == 0 {
			return nil, sspiError("InitializeSecurityContext", status)
		}
	}
	if len(output) == 0 {
		return nil, E.New("schannel: capability probe produced no ClientHello")
	}
	return output, nil
}

func (c *credentialHandle) free() {
	if c == nil || !c.valid {
		return
	}
	sspiFreeCredentialsHandle(&c.handle)
	c.valid = false
}
