package target

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// TestUDPEchoCountsByEpoch pins the isolation the runner relies on: a target
// must be able to report ingress for the measured epoch without counting warmup
// or previous-rep datagrams.
func TestUDPEchoCountsByEpoch(t *testing.T) {
	server, err := Start(UDPEcho, "127.0.0.1:0", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.Dial("udp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	packet := make([]byte, 64)
	binary.BigEndian.PutUint32(packet[0:4], 0x504c4231)
	binary.BigEndian.PutUint32(packet[4:8], 42)
	if _, err = conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(buffer); err != nil {
		t.Fatal(err)
	}

	stats := server.Snapshot()
	if stats.EpochDatagrams[42] != 1 || stats.EpochEchoed[42] != 1 {
		t.Fatalf("epoch 42 counts = %v/%v, want 1/1", stats.EpochDatagrams, stats.EpochEchoed)
	}
	if stats.Datagrams != 1 || stats.Echoed != 1 {
		t.Fatalf("global counts = %d/%d, want 1/1", stats.Datagrams, stats.Echoed)
	}

	// Another epoch must stay separate.
	binary.BigEndian.PutUint32(packet[4:8], 43)
	if _, err = conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(buffer); err != nil {
		t.Fatal(err)
	}
	stats = server.Snapshot()
	if stats.EpochDatagrams[42] != 1 || stats.EpochDatagrams[43] != 1 {
		t.Fatalf("epoch split = %v, want 42:1 43:1", stats.EpochDatagrams)
	}

	// A packet without the header is counted globally but not attributed.
	if _, err = conn.Write([]byte("not a generator datagram")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(buffer); err != nil {
		t.Fatal(err)
	}
	stats = server.Snapshot()
	if stats.Datagrams != 3 {
		t.Fatalf("global datagrams = %d, want 3", stats.Datagrams)
	}
	if _, ok := stats.EpochDatagrams[0]; ok {
		t.Fatalf("a headerless datagram was attributed to epoch 0")
	}

	server.Reset()
	stats = server.Snapshot()
	if len(stats.EpochDatagrams) != 0 || len(stats.EpochEchoed) != 0 {
		t.Fatalf("Reset left epoch counters: %v/%v", stats.EpochDatagrams, stats.EpochEchoed)
	}
}
