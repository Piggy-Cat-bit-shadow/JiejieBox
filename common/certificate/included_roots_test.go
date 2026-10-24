package certificate

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
)

// The built-in stores are generated data. A truncated block, a duplicated root or a PEM that
// no longer parses would silently weaken or bloat the trust set, so pin both the parsed shape
// and a checksum of the canonical DER sequence.
func TestIncludedRootCAsAreValid(t *testing.T) {
	testCases := []struct {
		name          string
		pem           string
		expectedCount int
		expectedSum   string
	}{
		{"chrome", chromeIncludedPEM(), 92, "094afa0be36d7f2cbe7f5e78e11185e3ecb0488ca09d1291b7423032b2a1b3dc"},
		{"mozilla", mozillaIncludedPEM(), 161, "e3d363576501d314d1823e09ef73e8db9872e5df0092cfd636a6f778191fb9c3"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			hashes := make(map[[32]byte]bool)
			var count int
			var canonical strings.Builder
			remaining := []byte(testCase.pem)
			for {
				block, rest := pem.Decode(remaining)
				if block == nil {
					break
				}
				remaining = rest
				if block.Type != "CERTIFICATE" {
					continue
				}
				certificate, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					t.Fatalf("certificate %d does not parse: %v", count, err)
				}
				if certificate.Subject.String() == "" {
					t.Fatalf("certificate %d has an empty subject", count)
				}
				hash := sha256.Sum256(block.Bytes)
				if hashes[hash] {
					t.Fatalf("certificate %d is a duplicate: %s", count, certificate.Subject)
				}
				hashes[hash] = true
				canonical.Write(block.Bytes)
				count++
			}
			if count != testCase.expectedCount {
				t.Fatalf("parsed %d certificates, want %d; update the pinned count only with the generated data", count, testCase.expectedCount)
			}
			digest := sha256.Sum256([]byte(canonical.String()))
			if testCase.expectedSum != "" && hex.EncodeToString(digest[:]) != testCase.expectedSum {
				t.Fatalf("certificate set checksum = %s, want %s", hex.EncodeToString(digest[:]), testCase.expectedSum)
			}
		})
	}
}
