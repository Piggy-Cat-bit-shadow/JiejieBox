//go:build windows

package schannel

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestDisabledProtocolsMaskKeepsTLS12WithTLS13 pins the workaround for the
// Schannel DTLS-shaped hello: a TLS 1.3 requirement must still leave TLS 1.2
// enabled on the credential, because a mask that leaves TLS 1.3 alone makes
// Schannel write a datagram hello to a stream socket.
func TestDisabledProtocolsMaskKeepsTLS12WithTLS13(t *testing.T) {
	testCases := []struct {
		name       string
		minVersion uint16
		maxVersion uint16
		want       uint32
	}{
		{
			name:       "tls13 required",
			minVersion: tls.VersionTLS13,
			maxVersion: tls.VersionTLS13,
			want:       spProtAllTLSClients &^ (spProtTLS12Client | spProtTLS13Client),
		},
		{
			name:       "tls13 ceiling",
			minVersion: tls.VersionTLS12,
			maxVersion: tls.VersionTLS13,
			want:       spProtAllTLSClients &^ (spProtTLS12Client | spProtTLS13Client),
		},
		{
			name:       "tls13 as default maximum",
			minVersion: tls.VersionTLS13,
			want:       spProtAllTLSClients &^ (spProtTLS12Client | spProtTLS13Client),
		},
		{
			name:       "tls12 only",
			minVersion: tls.VersionTLS12,
			maxVersion: tls.VersionTLS12,
			want:       spProtAllTLSClients &^ spProtTLS12Client,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := disabledProtocolsMask(testCase.minVersion, testCase.maxVersion)
			if got != testCase.want {
				t.Fatalf("disabledProtocolsMask(%#x, %#x) = %#x, want %#x", testCase.minVersion, testCase.maxVersion, got, testCase.want)
			}
			if got&spProtTLS13Client == 0 && got&spProtTLS12Client != 0 {
				t.Fatalf("mask %#x enables TLS 1.3 while disabling TLS 1.2, which Schannel answers with a DTLS record", got)
			}
		})
	}
}

// TestClientHelloIsNeverDTLS drives the real credential path for every
// supported version range and asserts the first record is a plain TLS
// handshake.
//
// This is the defect's fingerprint: the "TLS 1.3 only" mask used to produce
// `16fefd...` (a DTLS record), which every peer rejected and which reached
// callers as "read handshake: unexpected EOF".
func TestClientHelloIsNeverDTLS(t *testing.T) {
	err := CheckPlatform()
	if err != nil {
		t.Skip(err)
	}
	combinations := [][2]uint16{
		{0, 0},
		{tls.VersionTLS10, tls.VersionTLS13},
		{tls.VersionTLS12, 0},
		{0, tls.VersionTLS12},
		{tls.VersionTLS12, tls.VersionTLS12},
		{tls.VersionTLS13, 0},
		{tls.VersionTLS13, tls.VersionTLS13},
	}
	for _, combination := range combinations {
		mask := disabledProtocolsMask(combination[0], combination[1])
		credential, acquireErr := acquireClientCredential(mask)
		if acquireErr != nil {
			t.Fatalf("acquire credential for mask %#x: %v", mask, acquireErr)
		}
		hello, helloErr := credential.clientHello()
		credential.free()
		if helloErr != nil {
			t.Fatalf("client hello for mask %#x: %v", mask, helloErr)
		}
		if looksDTLS(hello) {
			t.Fatalf("mask %#x produced a DTLS-shaped hello (head % x)", mask, hello[:8])
		}
		if len(hello) < 5 || hello[0] != 0x16 {
			t.Fatalf("mask %#x produced a non-handshake record (head % x)", mask, hello[:8])
		}
		recordVersion := uint16(hello[1])<<8 | uint16(hello[2])
		if recordVersion < 0x0301 || recordVersion > 0x0303 {
			t.Fatalf("mask %#x produced record version %#04x", mask, recordVersion)
		}
		if len(supportedVersions(hello)) == 0 {
			t.Fatalf("mask %#x produced a hello without any version (head % x)", mask, hello[:8])
		}
	}
}

// TestSupportedVersionsParserMatchesGoClientHello parses a ClientHello that is
// known to carry supported_versions (Go's own TLS client) so the probe's parser
// is checked against bytes this package did not produce.
func TestSupportedVersionsParserMatchesGoClientHello(t *testing.T) {
	hello := captureGoClientHello(t, tls.VersionTLS12, tls.VersionTLS13)
	versions := supportedVersions(hello)
	if len(versions) == 0 {
		t.Fatal("parser found no versions in a TLS 1.3 client hello")
	}
	var sawTLS12, sawTLS13 bool
	for _, version := range versions {
		switch version {
		case tls.VersionTLS12:
			sawTLS12 = true
		case tls.VersionTLS13:
			sawTLS13 = true
		}
	}
	if !sawTLS13 || !sawTLS12 {
		t.Fatalf("parser returned %v, expected both TLS 1.2 and TLS 1.3", versions)
	}
	if !offersTLS13(hello) {
		t.Fatal("offersTLS13 rejected a hello that advertises TLS 1.3")
	}

	tls12Only := captureGoClientHello(t, tls.VersionTLS12, tls.VersionTLS12)
	if offersTLS13(tls12Only) {
		t.Fatal("offersTLS13 accepted a TLS 1.2-only hello")
	}
}

func captureGoClientHello(t *testing.T, minVersion, maxVersion uint16) []byte {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() {
		clientSide.Close()
		serverSide.Close()
	})
	client := tls.Client(clientSide, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "example.com",
		MinVersion:         minVersion,
		MaxVersion:         maxVersion,
	})
	go func() {
		_ = client.Handshake()
	}()
	buffer := make([]byte, 8192)
	serverSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	count, readErr := io.ReadAtLeast(serverSide, buffer, 5)
	if readErr != nil {
		serverSide.Close()
		t.Fatalf("read client hello: %v", readErr)
	}
	// The client is now waiting for a ServerHello that this pipe will never
	// carry, so the handshake goroutine is released by the cleanup that closes
	// both ends rather than by waiting for its result here.
	return append([]byte(nil), buffer[:count]...)
}

// TestCheckRequestedVersion pins the capability refusal: it must fire only when
// TLS 1.3 is REQUIRED and the machine provably cannot offer it.
func TestCheckRequestedVersion(t *testing.T) {
	if err := CheckPlatform(); err != nil {
		t.Skip(err)
	}
	supported := TLS13Supported()

	restore := SetTLS13SupportForTest(true)
	if err := CheckRequestedVersion(tls.VersionTLS13, tls.VersionTLS13); err != nil {
		t.Fatalf("a machine with TLS 1.3 must admit a TLS 1.3 requirement, got %v", err)
	}
	restore()

	if err := CheckRequestedVersion(tls.VersionTLS12, 0); err != nil {
		t.Fatalf("a TLS 1.2 minimum must always be admitted, got %v", err)
	}
	if err := CheckRequestedVersion(0, tls.VersionTLS12); err != nil {
		t.Fatalf("a TLS 1.2 maximum must always be admitted, got %v", err)
	}

	restore = SetTLS13SupportForTest(false)
	defer restore()
	err := CheckRequestedVersion(tls.VersionTLS13, 0)
	if supported || windowsBuildNumber() >= tls13MinimumBuild {
		if err != nil {
			t.Fatalf("a machine that may enable TLS 1.3 by policy must not be refused pre-flight, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected a TLS 1.3 requirement to be refused on a build without Schannel TLS 1.3")
	}
	if !errors.Is(err, ErrVersionUnavailable) {
		t.Fatalf("expected ErrVersionUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "TLS 1.3") {
		t.Fatalf("refusal does not name TLS 1.3: %v", err)
	}
}

// TestCheckNegotiatedVersion pins the post-handshake policy table.
func TestCheckNegotiatedVersion(t *testing.T) {
	testCases := []struct {
		name       string
		negotiated uint16
		minVersion uint16
		maxVersion uint16
		wantError  bool
		wantIs     error
	}{
		{name: "tls13 required and negotiated", negotiated: tls.VersionTLS13, minVersion: tls.VersionTLS13},
		{name: "tls13 required but tls12 negotiated", negotiated: tls.VersionTLS12, minVersion: tls.VersionTLS13, wantError: true, wantIs: ErrVersionNotNegotiated},
		{name: "default range admits tls12", negotiated: tls.VersionTLS12},
		{name: "default range admits tls13", negotiated: tls.VersionTLS13},
		{name: "tls12 ceiling refuses tls13", negotiated: tls.VersionTLS13, minVersion: tls.VersionTLS12, maxVersion: tls.VersionTLS12, wantError: true, wantIs: ErrVersionNotNegotiated},
		{name: "default minimum refuses tls11", negotiated: tls.VersionTLS11, wantError: true, wantIs: ErrVersionNotNegotiated},
		{name: "explicit tls10 range admits tls10", negotiated: tls.VersionTLS10, minVersion: tls.VersionTLS10, maxVersion: tls.VersionTLS11},
		{name: "no negotiated version", negotiated: 0, wantError: true},
		{name: "inverted range", negotiated: tls.VersionTLS12, minVersion: tls.VersionTLS13, maxVersion: tls.VersionTLS12, wantError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := CheckNegotiatedVersion(testCase.negotiated, testCase.minVersion, testCase.maxVersion)
			if !testCase.wantError {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if testCase.wantIs != nil && !errors.Is(err, testCase.wantIs) {
				t.Fatalf("expected %v, got %v", testCase.wantIs, err)
			}
		})
	}
}

// TestEffectiveVersionRangeDefaults pins that the mask and the post-handshake
// check share one definition of "zero means default".
func TestEffectiveVersionRangeDefaults(t *testing.T) {
	testCases := []struct {
		minVersion uint16
		maxVersion uint16
		wantMin    uint16
		wantMax    uint16
		wantError  bool
	}{
		{minVersion: 0, maxVersion: 0, wantMin: tls.VersionTLS12, wantMax: tls.VersionTLS13},
		{minVersion: 0, maxVersion: tls.VersionTLS10, wantMin: tls.VersionTLS10, wantMax: tls.VersionTLS10},
		{minVersion: tls.VersionTLS13, maxVersion: 0, wantMin: tls.VersionTLS13, wantMax: tls.VersionTLS13},
		{minVersion: tls.VersionTLS13, maxVersion: tls.VersionTLS12, wantError: true},
	}
	for _, testCase := range testCases {
		gotMin, gotMax, err := effectiveVersionRange(testCase.minVersion, testCase.maxVersion)
		if testCase.wantError {
			if err == nil {
				t.Fatalf("range (%#x, %#x) should be rejected", testCase.minVersion, testCase.maxVersion)
			}
			continue
		}
		if err != nil {
			t.Fatalf("range (%#x, %#x): %v", testCase.minVersion, testCase.maxVersion, err)
		}
		if gotMin != testCase.wantMin || gotMax != testCase.wantMax {
			t.Fatalf("range (%#x, %#x) = (%#x, %#x), want (%#x, %#x)",
				testCase.minVersion, testCase.maxVersion, gotMin, gotMax, testCase.wantMin, testCase.wantMax)
		}
	}
}
