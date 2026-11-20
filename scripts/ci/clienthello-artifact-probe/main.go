// Command clienthello-artifact-probe measures the key-exchange groups a BUILT sing-box binary sends, by
// running it against a local recorder and reading the ClientHello off the wire.
//
// # Why a second probe exists
//
// scripts/ci/clienthello-probe measures a MODULE: it compiles a few lines of crypto/tls with a given
// go.mod and prints the groups. That answers "what does the toolchain default to", but not "what does
// the artifact we ship send" - the two differ exactly when someone passes `-X runtime.godebugDefault`,
// because that form REPLACES the go.mod-computed default instead of merging into it (see go.mod and
// cmd/internal/build_shared/flags.go).
//
// This probe answers the artifact question end to end:
//
//	go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -trimpath \
//	  -ldflags "$(cat release/LDFLAGS) -s -w -buildid=" -o sing-box ./cmd/sing-box
//	go run ./scripts/ci/clienthello-artifact-probe ./sing-box
//
// # Measured 2026-11-20 (Go 1.27.4, windows/amd64), through a real SOCKS -> Trojan TLS handshake
//
//	release LDFLAGS ................. groups=[4588 29 23 24 25] key_shares=[4588 29]
//	dev build, no LDFLAGS ........... groups=[4588 29 23 24 25] key_shares=[4588 29]
//	old build_shared short LDFLAGS .. groups=[4588 4587 4589 29 23 24 25] key_shares=[4588 29]
//
// The third row is the defect the short list shipped: SecP256r1MLKEM768 and SecP384r1MLKEM1024 added to
// every ClientHello. The probe exists so that "the artifact sends the camouflage set" can be re-checked
// rather than assumed.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: clienthello-artifact-probe <path-to-sing-box-binary>")
		os.Exit(2)
	}
	binaryPath, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail("resolve binary: %v", err)
	}
	if _, err = os.Stat(binaryPath); err != nil {
		fail("binary: %v", err)
	}

	directory, err := os.MkdirTemp("", "clienthello-artifact")
	if err != nil {
		fail("temp dir: %v", err)
	}
	defer os.RemoveAll(directory)

	// The recorder stands where the Trojan server would be: it reads the first TLS record and parses the
	// ClientHello. The handshake is never completed, because the ClientHello is the whole measurement.
	recorder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("recorder listen: %v", err)
	}
	defer recorder.Close()
	recorderPort := recorder.Addr().(*net.TCPAddr).Port
	measured := make(chan string, 1)
	go func() {
		conn, acceptErr := recorder.Accept()
		if acceptErr != nil {
			measured <- "accept: " + acceptErr.Error()
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		measured <- readClientHello(conn)
	}()

	// A free port for the SOCKS inbound: bind, read, close, reuse.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("socks probe: %v", err)
	}
	socksPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	configPath := filepath.Join(directory, "config.json")
	config := fmt.Sprintf(`{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{
    "type": "trojan", "tag": "out",
    "server": "127.0.0.1", "server_port": %d, "password": "probe",
    "tls": {"enabled": true, "server_name": "example.com", "insecure": true}
  }],
  "route": {"final": "out"}
}`, socksPort, recorderPort)
	if err = os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		fail("write config: %v", err)
	}

	command := exec.Command(binaryPath, "run", "-c", configPath)
	command.Dir = directory
	// The child's output goes to a file in the temp directory, NOT to this process's stderr: a
	// child that inherits our stderr keeps the pipe open after we exit, and every caller that captures
	// our output (go run, a shell pipeline) then waits for an EOF that never comes.
	logPath := filepath.Join(directory, "sing-box.log")
	logFile, logErr := os.Create(logPath)
	if logErr != nil {
		fail("open sing-box log: %v", logErr)
	}
	defer logFile.Close()
	command.Stdout = logFile
	command.Stderr = logFile
	if err = command.Start(); err != nil {
		fail("start sing-box: %v", err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()

	var socks net.Conn
	for attempt := 0; attempt < 100; attempt++ {
		socks, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort))
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if socks == nil {
		fail("sing-box never listened on its SOCKS inbound: %v", err)
	}
	if err = socksConnect(socks, "example.com", 443); err != nil {
		fail("socks connect: %v", err)
	}
	_ = socks.Close()

	select {
	case line := <-measured:
		fmt.Println(line)
	case <-time.After(15 * time.Second):
		fail("no ClientHello reached the recorder")
	}
}

// socksConnect performs the minimal SOCKS5 handshake: no-auth greeting and one CONNECT request.
func socksConnect(conn net.Conn, host string, port uint16) error {
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		return fmt.Errorf("unexpected greeting reply %v", reply)
	}
	request := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	request = append(request, host...)
	request = binary.BigEndian.AppendUint16(request, port)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[1] != 0x00 {
		// EXPECTED, not an error: the recorder answers the ClientHello by closing, so the Trojan
		// handshake fails and the CONNECT is refused - after the measurement is already on the wire.
		return nil
	}
	switch header[3] {
	case 0x01:
		_, err := io.ReadFull(conn, make([]byte, 4+2))
		return err
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		_, err := io.ReadFull(conn, make([]byte, int(length[0])+2))
		return err
	case 0x04:
		_, err := io.ReadFull(conn, make([]byte, 16+2))
		return err
	}
	return fmt.Errorf("unexpected address type %#x", header[3])
}

// readClientHello reads one TLS record and reports the supported_groups and key_share entries.
func readClientHello(conn net.Conn) string {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "read record header: " + err.Error()
	}
	body := make([]byte, binary.BigEndian.Uint16(header[3:5]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return "read handshake body: " + err.Error()
	}
	if len(body) < 39 || body[0] != 1 {
		return fmt.Sprintf("unexpected handshake: len=%d type=%d", len(body), body[0])
	}
	offset := 4 + 2 + 32
	offset += 1 + int(body[offset])
	offset += 2 + int(binary.BigEndian.Uint16(body[offset:offset+2]))
	offset += 1 + int(body[offset])
	if offset+2 > len(body) {
		return "no extensions"
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
	return fmt.Sprintf("groups=%v key_shares=%v", groups, keyShares)
}

func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
