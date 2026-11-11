package runner

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

type fakeTargetHandle struct {
	datagrams int64
	grow      bool
}

func (f *fakeTargetHandle) Snapshot() (target.Stats, error) {
	if f.grow {
		f.datagrams++
	}
	return target.Stats{Datagrams: f.datagrams}, nil
}

func (f *fakeTargetHandle) Reset() error { f.datagrams = 0; return nil }

func (f *fakeTargetHandle) Close() error { return nil }

func newQuiesceSession(handle targetHandle) *session {
	return &session{targets: &targetSet{byKind: map[target.Kind]targetHandle{target.UDPEcho: handle}}}
}

func TestQuiesceTargetTimeoutIsExplicit(t *testing.T) {
	sess := newQuiesceSession(&fakeTargetHandle{grow: true})
	start := time.Now()
	result := quiesceTarget(sess, target.UDPEcho, Options{Logf: func(string, ...any) {}})
	elapsed := time.Since(start)
	if result.Status != "timeout" || !result.TimedOut || result.OK {
		t.Fatalf("status=%q ok=%v timedout=%v, want timeout", result.Status, result.OK, result.TimedOut)
	}
	if result.ElapsedMS < 1900 {
		t.Fatalf("timeout returned too early: %v ms", result.ElapsedMS)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("wall time %v shorter than the 2 s bound", elapsed)
	}
	if result.Datagrams == 0 {
		t.Fatal("timeout result did not record the last observed count")
	}
}

func TestQuiesceTargetConfirmedAfterStableWindow(t *testing.T) {
	sess := newQuiesceSession(&fakeTargetHandle{datagrams: 7})
	result := quiesceTarget(sess, target.UDPEcho, Options{Logf: func(string, ...any) {}})
	if result.Status != "confirmed" || !result.OK || result.TimedOut {
		t.Fatalf("status=%q ok=%v timedout=%v, want confirmed", result.Status, result.OK, result.TimedOut)
	}
	if result.Datagrams != 7 {
		t.Fatalf("datagrams = %d, want 7", result.Datagrams)
	}
}

func TestQuiesceTargetMissingHandleIsUnavailable(t *testing.T) {
	result := quiesceTarget(&session{targets: &targetSet{byKind: map[target.Kind]targetHandle{}}}, target.UDPEcho, Options{Logf: func(string, ...any) {}})
	if result.Status != "unavailable" || result.OK {
		t.Fatalf("status=%q ok=%v, want unavailable", result.Status, result.OK)
	}
}
