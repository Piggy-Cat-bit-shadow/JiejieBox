// Package certs mints the throwaway PKI a lab run uses.
//
// Every run gets its own CA and leaf, written under the run directory and
// deleted with it. Nothing here is ever reused between runs: a certificate that
// outlives a run makes "the client verified the server" a weaker claim.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Files are the paths a config template needs.
type Files struct {
	Dir        string
	CA         string
	Cert       string
	Key        string
	ServerName string
}

// Generate writes a CA and a server certificate for serverName into dir.
func Generate(dir, serverName string, ips []net.IP) (Files, error) {
	files := Files{Dir: dir, ServerName: serverName}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return files, err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return files, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "perf-lab-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return files, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return files, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return files, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: serverName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{serverName},
		IPAddresses:  ips,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return files, err
	}

	files.CA = filepath.Join(dir, "ca.pem")
	files.Cert = filepath.Join(dir, "cert.pem")
	files.Key = filepath.Join(dir, "key.pem")
	if err = writePEM(files.CA, "CERTIFICATE", caDER); err != nil {
		return files, err
	}
	if err = writePEM(files.Cert, "CERTIFICATE", leafDER); err != nil {
		return files, err
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return files, err
	}
	if err = writePEM(files.Key, "EC PRIVATE KEY", keyDER); err != nil {
		return files, err
	}
	return files, nil
}

// GenerateCAOnly writes only the CA certificate, for clients that pin a CA
// rather than a leaf.
func (f Files) EnsureCA() error {
	if _, err := os.Stat(f.CA); err != nil {
		return fmt.Errorf("CA missing: %w", err)
	}
	return nil
}

func writePEM(path, blockType string, der []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return pem.Encode(file, &pem.Block{Type: blockType, Bytes: der})
}
