// Command clienthello-probe prints the key-exchange groups a default crypto/tls client offers, together
// with the Go version and the module's language version that produced them.
//
// It exists to answer one question the release notes cannot: does a standard-library DEFAULT depend on
// the toolchain, on the `go` directive in go.mod, or on both? The answer decides whether an upgrade
// silently changes the ClientHello every TLS outbound sends.
//
// Usage, from a directory containing a go.mod that declares the language version under test:
//
//	cd /tmp/go125 && go mod init probe && echo 'go 1.25.5' > go.mod
//	cp .../scripts/ci/clienthello-probe/main.go . && go run main.go
//
// Measured 2026-11-18 (go1.27.4 toolchain, x86-64):
//
//	go.mod go 1.25.5, toolchain 1.27.4: [4588 29 23 24 25]            (X25519MLKEM768, X25519, P-256/384/521)
//	go.mod go 1.27.4, toolchain 1.27.4: [4588 4587 4589 29 23 24 25] (+ SecP256r1MLKEM768, SecP384r1MLKEM1024)
//	go.mod go 1.25.5, toolchain 1.25.5: [4588 29 23 24 25]
//
// So `tlssecpmlkem` follows the `go` line, not the toolchain: this repository (go 1.25.5) keeps the
// pre-1.26 set even when built by 1.27.4, and the ClientHello changes the day the directive moves.
// W2 in docs/fork/go-1274-regressions.md records the consequence.
package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"time"
)

func main() {
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		connection := tls.Client(client, &tls.Config{
			ServerName:         "example.com",
			InsecureSkipVerify: true,
		})
		_ = connection.Handshake()
	}()

	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 5)
	if _, err := io.ReadFull(server, header); err != nil {
		fmt.Println("read record header:", err)
		os.Exit(1)
	}
	length := int(binary.BigEndian.Uint16(header[3:5]))
	body := make([]byte, length)
	if _, err := io.ReadFull(server, body); err != nil {
		fmt.Println("read handshake body:", err)
		os.Exit(1)
	}
	if len(body) < 39 || body[0] != 1 {
		fmt.Printf("unexpected handshake: len=%d type=%d\n", len(body), body[0])
		os.Exit(1)
	}

	// handshake header (4) + legacy_version (2) + random (32)
	offset := 4 + 2 + 32
	if offset >= len(body) {
		fmt.Println("truncated before the session id")
		os.Exit(1)
	}
	offset += 1 + int(body[offset])
	if offset+2 > len(body) {
		fmt.Println("truncated before the cipher suites")
		os.Exit(1)
	}
	offset += 2 + int(binary.BigEndian.Uint16(body[offset:offset+2]))
	if offset+1 > len(body) {
		fmt.Println("truncated before the compression methods")
		os.Exit(1)
	}
	offset += 1 + int(body[offset])
	if offset+2 > len(body) {
		fmt.Println("no extensions in the ClientHello")
		os.Exit(1)
	}
	extensionsLength := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	end := offset + extensionsLength
	if end > len(body) {
		end = len(body)
	}

	var groups, keyShares []uint16
	for offset+4 <= end {
		extensionType := binary.BigEndian.Uint16(body[offset : offset+2])
		extensionLength := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		data := body[offset+4 : offset+4+extensionLength]
		switch extensionType {
		case 10: // supported_groups
			if len(data) >= 2 {
				count := int(binary.BigEndian.Uint16(data[:2]))
				for position := 2; position+1 < 2+count && position+1 < len(data); position += 2 {
					groups = append(groups, binary.BigEndian.Uint16(data[position:position+2]))
				}
			}
		case 51: // key_share
			if len(data) >= 2 {
				count := int(binary.BigEndian.Uint16(data[:2]))
				position := 2
				for range count {
					if position+4 > len(data) {
						break
					}
					keyShares = append(keyShares, binary.BigEndian.Uint16(data[position:position+2]))
					position += 4 + int(binary.BigEndian.Uint16(data[position+2:position+4]))
				}
			}
		}
		offset += 4 + extensionLength
	}

	fmt.Printf("go=%s groups=%v key_shares=%v\n", runtime.Version(), groups, keyShares)
}
