package runner

import (
	"errors"
	"testing"

	"github.com/sagernet/sing-box/scripts/perf/internal/loadgen"
	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

func TestUDPRecordDerivesDeadlineLossWhenEstimable(t *testing.T) {
	result := loadgen.Result{
		Paced:                    true,
		DatagramsSent:            100,
		DatagramsReceived:        98,
		DatagramWrites:           100,
		DatagramUnique:           98,
		DatagramReturned:         98,
		DatagramAttempts:         100,
		DatagramScheduled:        100,
		SendWindow:               2_000_000_000,
		ObservationDuration:      500_000_000,
		ObservationElapsed:       500_000_000,
		ObservationComplete:      true,
		QuietPeriod:              200_000_000,
		DrainStatus:              "complete_quiet",
		LossEstimable:            true,
		SequenceTrackingComplete: true,
		NotReturned:              2,
		NotReturnedRatio:         0.02,
		SendPhaseDuration:        2_000_000_000,
		ConfiguredPPS:            50,
		ScheduledPPS:             50,
		AttemptPPS:               50,
		WriteCompletionPPS:       50,
	}
	stats := target.Stats{
		Datagrams:      100,
		Echoed:         99,
		EpochDatagrams: map[uint32]int64{7: 100},
		EpochEchoed:    map[uint32]int64{7: 99},
	}
	record := udpRecordFrom(result, stats, nil, 1200, 7, "confirmed")
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
	if record.TargetEpoch == nil || *record.TargetEpoch != 7 || record.TargetEpochIsolated == nil || !*record.TargetEpochIsolated {
		t.Fatalf("epoch provenance missing: %#v", record)
	}
	if record.Mode != "paced" || record.AchievedPPS == nil || *record.AchievedPPS != 50 {
		t.Fatalf("mode=%q pps=%v", record.Mode, record.AchievedPPS)
	}
	if record.SendPhaseMS == nil || *record.SendPhaseMS != 2000 {
		t.Fatalf("send_phase_ms = %v", record.SendPhaseMS)
	}
	if record.NotReturned == nil || *record.NotReturned != 2 {
		t.Fatalf("not_returned = %v", record.NotReturned)
	}
	if record.DrainStatus != "complete_quiet" || record.LossEstimable == nil || !*record.LossEstimable {
		t.Fatalf("drain state missing: %#v", record)
	}
}

func TestUDPRecordNullsLossWhenNotEstimable(t *testing.T) {
	result := loadgen.Result{
		Paced:                    true,
		DatagramWrites:           100,
		DatagramUnique:           40,
		NotReturned:              60,
		NotReturnedRatio:         0.6,
		DrainStatus:              "window_elapsed_tail_active",
		ObservationComplete:      false,
		LossEstimable:            false,
		SequenceTrackingComplete: true,
	}
	record := udpRecordFrom(result, target.Stats{}, nil, 1200, 7, "timeout")
	if record.Lost != nil || record.LossPercent != nil {
		t.Fatalf("a tail-active observation must not claim loss: lost=%v pct=%v", record.Lost, record.LossPercent)
	}
	if record.DrainComplete == nil || *record.DrainComplete {
		t.Fatal("drain_complete must be recorded as false")
	}
	if record.NotReturned == nil || *record.NotReturned != 60 {
		t.Fatalf("deadline miss must remain visible: %v", record.NotReturned)
	}
}

func TestUDPRecordSequenceTruncationForcesNullLoss(t *testing.T) {
	result := loadgen.Result{
		Paced:                    true,
		DatagramWrites:           100,
		DatagramUnique:           90,
		LossEstimable:            false,
		SequenceTrackingComplete: false,
		DatagramUnattributed:     5,
	}
	record := udpRecordFrom(result, target.Stats{}, nil, 1200, 7, "confirmed")
	if record.Lost != nil || record.LossPercent != nil {
		t.Fatalf("truncated sequence window must not emit loss: %#v", record)
	}
	if record.SequenceTrackingComplete == nil || *record.SequenceTrackingComplete {
		t.Fatal("sequence_tracking_complete must be false")
	}
}

func TestUDPRecordTargetEpochIsolation(t *testing.T) {
	result := loadgen.Result{DatagramWrites: 10, DatagramUnique: 10, LossEstimable: true, SequenceTrackingComplete: true}
	stats := target.Stats{
		Datagrams:      999, // warmup + previous traffic
		Echoed:         999,
		EpochDatagrams: map[uint32]int64{7: 10}, // measured epoch only
		EpochEchoed:    map[uint32]int64{7: 10},
	}
	record := udpRecordFrom(result, stats, nil, 1200, 7, "confirmed")
	if record.TargetReceived == nil || *record.TargetReceived != 10 {
		t.Fatalf("target_received = %v, want 10 (epoch-isolated)", record.TargetReceived)
	}
	if record.TargetEpochIsolated == nil || !*record.TargetEpochIsolated {
		t.Fatal("epoch isolation not recorded")
	}
}

func TestUDPRecordTargetStatsUnavailableStayNull(t *testing.T) {
	record := udpRecordFrom(loadgen.Result{DatagramWrites: 1, DatagramUnique: 1, LossEstimable: true}, target.Stats{}, errors.New("no target"), 1200, 7, "unavailable")
	if record.TargetReceived != nil || record.Echoed != nil {
		t.Fatalf("target stats unavailable but recorded: %v %v", record.TargetReceived, record.Echoed)
	}
}

func TestUDPRecordUnpacedOmitsPacedOnlyFields(t *testing.T) {
	record := udpRecordFrom(loadgen.Result{DatagramWrites: 10, DatagramUnique: 10, DatagramAttempts: 10, LossEstimable: true, SequenceTrackingComplete: true}, target.Stats{Datagrams: 10, Echoed: 10}, nil, 1200, 7, "confirmed")
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
