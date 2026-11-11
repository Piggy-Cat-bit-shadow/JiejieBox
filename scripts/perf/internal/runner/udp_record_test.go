package runner

import (
	"errors"
	"testing"

	"github.com/sagernet/sing-box/scripts/perf/internal/loadgen"
	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

func TestUDPRecordDerivesLossWhenDrainComplete(t *testing.T) {
	result := loadgen.Result{
		Paced:             true,
		DatagramsSent:     100,
		DatagramsReceived: 98,
		DatagramWrites:    100,
		DatagramUnique:    98,
		DatagramReturned:  98,
		DatagramAttempts:  100,
		DatagramScheduled: 100,
		SendWindow:        2000_000_000, // 2s
		DrainDuration:     500_000_000,
		DrainLastReply:    100_000_000,
		DrainComplete:     true,
		AllReturned:       false,
	}
	stats := target.Stats{Datagrams: 100, Echoed: 99}
	record := udpRecordFrom(result, stats, nil, 1200)
	if record.Lost == nil || *record.Lost != 2 {
		t.Fatalf("lost = %v, want 2", record.Lost)
	}
	if record.LossPercent == nil || *record.LossPercent != 2 {
		t.Fatalf("loss = %v, want 2", record.LossPercent)
	}
	if record.TargetReceived == nil || *record.TargetReceived != 100 {
		t.Fatalf("target_received = %v, want 100", record.TargetReceived)
	}
	if record.Echoed == nil || *record.Echoed != 99 {
		t.Fatalf("echoed = %v, want 99", record.Echoed)
	}
	if record.Mode != "paced" || record.AchievedPPS == nil || *record.AchievedPPS != 50 {
		t.Fatalf("mode=%q pps=%v", record.Mode, record.AchievedPPS)
	}
	if record.SentWindowMS == nil || *record.SentWindowMS != 2000 {
		t.Fatalf("sent_window_ms = %v", record.SentWindowMS)
	}
	if record.SendEndToLastReplyMS == nil || *record.SendEndToLastReplyMS != 100 {
		t.Fatalf("send_end_to_last_reply_ms = %v", record.SendEndToLastReplyMS)
	}
	if record.AllReturned == nil || *record.AllReturned {
		t.Fatalf("all_returned = %v, want false", record.AllReturned)
	}
}

func TestUDPRecordNullsLossWhenDrainIncomplete(t *testing.T) {
	result := loadgen.Result{
		Paced:          true,
		DatagramWrites: 100,
		DatagramUnique: 40,
		DrainComplete:  false,
	}
	record := udpRecordFrom(result, target.Stats{}, nil, 1200)
	if record.Lost != nil || record.LossPercent != nil {
		t.Fatalf("a truncated drain must not claim loss: lost=%v pct=%v", record.Lost, record.LossPercent)
	}
	if record.DrainComplete == nil || *record.DrainComplete {
		t.Fatal("drain_complete must be recorded as false")
	}
}

func TestUDPRecordTargetStatsUnavailableStayNull(t *testing.T) {
	record := udpRecordFrom(loadgen.Result{DatagramWrites: 1, DatagramUnique: 1, DrainComplete: true}, target.Stats{}, errors.New("no target"), 1200)
	if record.TargetReceived != nil || record.Echoed != nil {
		t.Fatalf("target stats unavailable but recorded: %v %v", record.TargetReceived, record.Echoed)
	}
}

func TestUDPRecordUnpacedOmitsPacedOnlyFields(t *testing.T) {
	record := udpRecordFrom(loadgen.Result{DatagramWrites: 10, DatagramUnique: 10, DatagramAttempts: 10, DrainComplete: true}, target.Stats{Datagrams: 10, Echoed: 10}, nil, 1200)
	if record.Mode != "unpaced" {
		t.Fatalf("mode = %q", record.Mode)
	}
	if record.Skipped != nil || record.Scheduled != nil || record.AchievedPPS != nil {
		t.Fatalf("paced-only fields leaked into unpaced: skipped=%v scheduled=%v pps=%v", record.Skipped, record.Scheduled, record.AchievedPPS)
	}
	if record.Lost == nil || *record.Lost != 0 {
		t.Fatalf("lost = %v, want 0", record.Lost)
	}
}
