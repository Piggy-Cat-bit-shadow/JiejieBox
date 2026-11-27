package daemon

// G08: the WHOLE-Box reload contract, with live traffic on both transports.
//
// The product's reload is sequential: StartOrReloadService closes the current instance and then builds
// the next one, so every live flow of the old instance must end at that point - and the new instance
// must serve new flows on the same listener ports. A reload that cannot build must leave the running
// instance alone, which is asserted through the live TCP flow.
//
// TCP goes through the HTTP inbound; UDP goes through a real SOCKS5 UDP ASSOCIATE on a SOCKS inbound,
// because a UDP association is exactly the flow shape that has no transport retransmission. The old
// generation must also release its records and references: that is the "old generation released" half
// of the contract.

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sock5UDPAssociation is a minimal SOCKS5 UDP ASSOCIATE client, enough to hold one live association.
type sock5UDPAssociation struct {
	control net.Conn
	relay   *net.UDPAddr
	udp     *net.UDPConn
	buffer  []byte
}

func startSocksUDPAssociation(t *testing.T, socksPort int) *sock5UDPAssociation {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = control.Close() })
	// greeting: version 5, one method, no authentication
	_, err = control.Write([]byte{0x05, 0x01, 0x00})
	require.NoError(t, err)
	greeting := make([]byte, 2)
	_, err = io.ReadFull(control, greeting)
	require.NoError(t, err)
	require.Equal(t, []byte{0x05, 0x00}, greeting)
	// UDP ASSOCIATE with 0.0.0.0:0 (the relay address is chosen by the server)
	_, err = control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	require.NoError(t, err)
	reply := make([]byte, 10)
	_, err = io.ReadFull(control, reply)
	require.NoError(t, err)
	require.Equal(t, byte(0x05), reply[0])
	require.Zero(t, reply[1], "the SOCKS inbound must accept UDP ASSOCIATE: %x", reply)
	require.Equal(t, byte(0x01), reply[3])
	relay := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:10]))}
	if relay.IP.IsUnspecified() {
		relay.IP = net.ParseIP("127.0.0.1")
	}
	udpConn, err := net.DialUDP("udp", nil, relay)
	require.NoError(t, err)
	return &sock5UDPAssociation{control: control, relay: relay, udp: udpConn, buffer: make([]byte, 4096)}
}

func (a *sock5UDPAssociation) send(t *testing.T, destination *net.UDPAddr, payload []byte) {
	t.Helper()
	packet := []byte{0x00, 0x00, 0x00, 0x01}
	packet = append(packet, destination.IP.To4()...)
	packet = binary.BigEndian.AppendUint16(packet, uint16(destination.Port))
	packet = append(packet, payload...)
	_, err := a.udp.Write(packet)
	require.NoError(t, err)
}

func (a *sock5UDPAssociation) receive(t *testing.T, budget time.Duration) ([]byte, error) {
	t.Helper()
	require.NoError(t, a.udp.SetReadDeadline(time.Now().Add(budget)))
	read, err := a.udp.Read(a.buffer)
	if err != nil {
		return nil, err
	}
	if read < 10 {
		return nil, fmt.Errorf("short SOCKS datagram: %d bytes", read)
	}
	// header: RSV(2) FRAG(1) ATYP(1) ADDR(4) PORT(2)
	return append([]byte(nil), a.buffer[10:read]...), nil
}

// startG08UDPEcho echoes datagrams back to whoever sent them.
func startG08UDPEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		packet := make([]byte, 2048)
		for {
			read, from, readErr := conn.ReadFromUDP(packet)
			if readErr != nil {
				return
			}
			_, _ = conn.WriteToUDP(packet[:read], from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

// g08Profile is the loaded configuration: an HTTP inbound for TCP, a SOCKS inbound for the UDP
// association, a remote provider document, and a rule that routes the inbound traffic explicitly.
func g08Profile(httpPort, socksPort int, providerURL string) string {
	return g08ProfileWithLog(httpPort, socksPort, providerURL, "error")
}

func g08ProfileWithLog(httpPort, socksPort int, providerURL string, logLevel string) string {
	return fmt.Sprintf(`{
  "log": {"level": %q},
  "inbounds": [
    {"type": "http", "tag": "tcp-in", "listen": "127.0.0.1", "listen_port": %d},
    {"type": "socks", "tag": "udp-in", "listen": "127.0.0.1", "listen_port": %d}
  ],
  "providers": [{"type": "remote", "tag": "lib", "url": %q, "update_interval": "1h"}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {
    "rules": [{"inbound": ["tcp-in", "udp-in"], "outbound": "direct"}],
    "final": "direct"
  }
}`, logLevel, httpPort, socksPort, providerURL)
}

// TestG08WholeBoxReloadClosesLiveTCPAndUDPAndServesNewOnes is the sequential reload contract.
func TestG08WholeBoxReloadClosesLiveTCPAndUDPAndServesNewOnes(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hold" {
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("held-first\n"))
			if flusher != nil {
				flusher.Flush()
			}
			<-request.Context().Done()
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)
	udpEcho := startG08UDPEcho(t)

	library := newW7LibraryServer(t, w7LibraryDocument(2, 0, -1, 0))
	httpPort := freeLoopbackPort(t)
	socksPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, t.TempDir(), nil)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(),
		g08Profile(httpPort, socksPort, library.server.URL), nil))
	firstInstance := service.Instance()
	require.NotNil(t, firstInstance)
	firstBox := firstInstance.Box()

	// Live TCP through the HTTP inbound.
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", httpPort)}
	tcpClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 30 * time.Second}
	tcpResponse, err := tcpClient.Get(target.URL + "/hold")
	require.NoError(t, err)
	tcpReader := bufio.NewReader(tcpResponse.Body)
	firstLine, err := tcpReader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "held-first\n", firstLine)

	// Live UDP association through the SOCKS inbound.
	association := startSocksUDPAssociation(t, socksPort)
	defer association.udp.Close()
	association.send(t, udpEcho, []byte("udp-before-reload"))
	reply, err := association.receive(t, 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, "udp-before-reload", string(reply))

	// A Provider batch update while both flows are live.
	_, err = service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, 2, firstBox.CatalogStats().Records)

	// The sequential reload: the old instance is closed before the new one is built. The document has to
	// DIFFER, otherwise the daemon correctly reports "unchanged" and keeps the running Box.
	reloadReport, err := service.ApplyServiceChange(context.Background(),
		g08ProfileWithLog(httpPort, socksPort, library.server.URL, "warn"), nil)
	require.NoError(t, err)
	require.Equal(t, ServiceChangeStatusRestarted, reloadReport.Status,
		"a changed document must restart the whole Box: %+v", reloadReport)
	secondInstance := service.Instance()
	require.NotNil(t, secondInstance)
	require.NotSame(t, firstInstance, secondInstance)
	secondBox := secondInstance.Box()

	// 1. The live TCP flow of the OLD instance is closed, not left hanging.
	_, readErr := tcpReader.ReadString('\n')
	require.Error(t, readErr, "the old instance's live TCP flow must end when it is closed")
	tcpResponse.Body.Close()

	// 2. The live UDP association of the OLD instance stops carrying traffic (its relay belongs to the
	//    closed instance); a read either errors or times out.
	association.send(t, udpEcho, []byte("udp-after-close"))
	_, udpErr := association.receive(t, 2*time.Second)
	require.Error(t, udpErr, "the old instance's live UDP association must stop serving when it is closed")

	// 3. The old generation is released: no records or references survive it.
	closed := firstBox.CatalogStats()
	require.True(t, closed.Closed, "the old Box must be closed by the reload")
	require.Zero(t, closed.Records)
	require.Zero(t, closed.Refs)

	// 4. The NEW instance serves both transports on the same ports.
	newTCP, err := tcpClient.Get(target.URL + "/plain")
	require.NoError(t, err)
	newBody, err := io.ReadAll(newTCP.Body)
	newTCP.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "ok", string(newBody))

	newAssociation := startSocksUDPAssociation(t, socksPort)
	defer newAssociation.udp.Close()
	newAssociation.send(t, udpEcho, []byte("udp-after-reload"))
	newReply, err := newAssociation.receive(t, 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, "udp-after-reload", string(newReply))

	// 5. The new generation owns its own records; nothing was inherited from the closed one.
	require.Equal(t, 2, secondBox.CatalogStats().Records)
}

// TestG08AFailedReloadFollowsTheDocumentedRecoveryContract pins the two failure shapes the product
// actually distinguishes:
//
//  1. a candidate that cannot be PARSED never touches the running Box (C04: parse and plan before the
//     old instance is touched) - status failed, Retained true, traffic still served;
//  2. a candidate that parses but FAILS TO START is a restart that could not complete: the report says
//     failed and, honestly, does not claim retention. Recovery is then the caller's re-apply of the
//     last-good document, which must restore service - asserted here instead of pretending the failed
//     start kept the old Box.
func TestG08AFailedReloadFollowsTheDocumentedRecoveryContract(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)
	library := newW7LibraryServer(t, w7LibraryDocument(2, 0, -1, 0))
	httpPort := freeLoopbackPort(t)
	socksPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, t.TempDir(), nil)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	goodProfile := g08Profile(httpPort, socksPort, library.server.URL)
	firstReport, err := service.ApplyServiceChange(context.Background(), goodProfile, nil)
	require.NoError(t, err)
	require.Equal(t, ServiceChangeStatusStarted, firstReport.Status)
	runningInstance := service.Instance()
	require.NotNil(t, runningInstance)

	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", httpPort)}
	// A fresh client per request: a pooled connection to the OLD listener is not what this test is
	// about, and reusing one would conflate transport retry behaviour with Box state.
	serve := func(path string) {
		t.Helper()
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
		response, requestErr := client.Get(target.URL + path)
		require.NoError(t, requestErr)
		body, readErr := io.ReadAll(response.Body)
		defer response.Body.Close()
		require.NoError(t, readErr)
		require.Equal(t, 200, response.StatusCode, "status for %s", path)
		require.Equal(t, "ok", string(body), "body for %s", path)
	}
	serve("/before")

	// 1. Unparsable: refused before the running Box is touched.
	report, reloadErr := service.ApplyServiceChange(context.Background(), `{"inbounds": [`, nil)
	require.Error(t, reloadErr)
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status, "%+v", report)
	require.True(t, report.Retained, "an unparsable candidate must not cost the running configuration: %+v", report)
	require.Same(t, runningInstance, service.Instance(), "nothing was rebuilt")
	serve("/after-unparsable")

	// 2. Parses but cannot start (two inbounds on one port): the restart cannot complete, and the
	//    report says so without claiming retention.
	broken := fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [
    {"type": "http", "tag": "tcp-in", "listen": "127.0.0.1", "listen_port": %d},
    {"type": "http", "tag": "duplicate", "listen": "127.0.0.1", "listen_port": %d}
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`, httpPort, httpPort)
	report, reloadErr = service.ApplyServiceChange(context.Background(), broken, nil)
	require.Error(t, reloadErr)
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status, "%+v", report)
	require.False(t, report.Retained, "a start failure is not retention; the report must not pretend: %+v", report)

	// 3. Recovery: re-applying the last-good document restores service on the same ports.
	recovered, recoverErr := service.ApplyServiceChange(context.Background(), goodProfile, nil)
	require.NoError(t, recoverErr)
	require.Contains(t, []ServiceChangeStatus{ServiceChangeStatusStarted, ServiceChangeStatusRestarted}, recovered.Status,
		"%+v", recovered)
	serve("/after-recovery")
}
