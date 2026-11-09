package rule

// D — the build-pressure sampler for an exclusive timing window.
//
// This test is SKIPPED unless a real rule-set corpus is supplied. It performs the comparison the
// acceptance asks for (keyword loop vs compiled path, concurrent builds under one gate, equal
// success counts) against a corpus the operator owns; nothing about the corpus is invented here.
//
// Exclusive window, one run:
//
//	SING_BOX_PERF_CORPUS=C:\corpus\geosite-current.srs \
//	SING_BOX_PERF_WORKERS=8 \
//	SING_BOX_PERF_GATE=2 \
//	SING_BOX_PERF_TARGET=500 \
//	SING_BOX_PERF_OUT=C:\evidence\sample.csv \
//	go test ./route/rule -run TestBuildPressureSample -count=1 -v -timeout 30m
//
// The run stops at SING_BOX_PERF_TARGET successful builds (or the overall deadline). Each attempt
// records its queue latency and build time; the sampler records peak heap and heap system bytes.
// Run the baseline and the candidate with the SAME corpus, workers, gate and target; compare only
// runs that reached the target, and report cancelled/deferred/timeout attempts separately.

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func sampleEnvironmentInt(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

type buildSample struct {
	Worker    int
	Attempt   int
	StartedAt time.Time
	Elapsed   time.Duration
	Status    string
	Error     string
}

func TestBuildPressureSample(t *testing.T) {
	corpusPath := os.Getenv("SING_BOX_PERF_CORPUS")
	if corpusPath == "" {
		t.Skip("set SING_BOX_PERF_CORPUS to a binary rule-set file to run the exclusive-window sample")
	}
	corpus, err := os.ReadFile(corpusPath)
	require.NoError(t, err)

	workers := sampleEnvironmentInt("SING_BOX_PERF_WORKERS", 8)
	gateLimit := sampleEnvironmentInt("SING_BOX_PERF_GATE", 2)
	target := sampleEnvironmentInt("SING_BOX_PERF_TARGET", 200)
	deadline := 10 * time.Minute
	if value := os.Getenv("SING_BOX_PERF_DEADLINE"); value != "" {
		parsed, parseErr := time.ParseDuration(value)
		require.NoError(t, parseErr)
		deadline = parsed
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(corpus)
	}))
	t.Cleanup(server.Close)

	gate := buildgate.New(gateLimit, workers*2, 2*time.Second)
	ctx, cancel := context.WithTimeout(buildgate.ContextWithGate(context.Background(), gate), deadline)
	defer cancel()

	var completed atomic.Int32
	var reserved atomic.Int32
	var samplesAccess sync.Mutex
	var samples []buildSample
	var peakHeap, peakSys atomic.Uint64
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapAlloc > peakHeap.Load() {
					peakHeap.Store(stats.HeapAlloc)
				}
				if stats.HeapSys > peakSys.Load() {
					peakSys.Store(stats.HeapSys)
				}
			}
		}
	}()

	started := time.Now()
	var runners sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		runners.Add(1)
		go func(worker int) {
			defer runners.Done()
			ruleSet, createErr := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), "sample", option.RuleSet{
				Type:   C.RuleSetTypeRemote,
				Format: C.RuleSetFormatBinary,
				RemoteOptions: option.RemoteRuleSet{
					URL: server.URL,
				},
			})
			if createErr != nil {
				return
			}
			ruleSet.httpClient = server.Client()
			defer ruleSet.Close()
			for attempt := 0; ; attempt++ {
				if ctx.Err() != nil {
					return
				}
				// Reserve one of the target SUCCESS slots. A failed/deferred attempt returns its
				// reservation, so the run ends with exactly target successes (or fewer if the
				// deadline cancels attempts that were in flight).
				if completed.Load() >= int32(target) || reserved.Load() >= int32(target) {
					return
				}
				if reserved.Add(1) > int32(target) {
					reserved.Add(-1)
					return
				}
				attemptStarted := time.Now()
				fetchErr := ruleSet.fetch(ctx, false)
				elapsed := time.Since(attemptStarted)
				sample := buildSample{
					Worker:    worker,
					Attempt:   attempt,
					StartedAt: attemptStarted,
					Elapsed:   elapsed,
				}
				switch {
				case fetchErr == nil:
					sample.Status = "success"
					completed.Add(1)
				case errors.Is(fetchErr, buildgate.ErrDeferred):
					sample.Status = "deferred"
					reserved.Add(-1)
				case errors.Is(fetchErr, context.DeadlineExceeded):
					sample.Status = "timeout"
					reserved.Add(-1)
				case errors.Is(fetchErr, context.Canceled):
					sample.Status = "cancelled"
					reserved.Add(-1)
				default:
					sample.Status = "failed"
					sample.Error = fetchErr.Error()
					reserved.Add(-1)
				}
				samplesAccess.Lock()
				samples = append(samples, sample)
				samplesAccess.Unlock()
			}
		}(worker)
	}
	runners.Wait()
	close(stopSampling)
	<-samplingDone

	var successes, deferred, failed, cancelled, timedOut int
	var totalBuild time.Duration
	samplesAccess.Lock()
	for _, sample := range samples {
		switch sample.Status {
		case "success":
			successes++
			totalBuild += sample.Elapsed
		case "deferred":
			deferred++
		case "cancelled":
			cancelled++
		case "timeout":
			timedOut++
		default:
			failed++
		}
	}
	outPath := os.Getenv("SING_BOX_PERF_OUT")
	if outPath != "" {
		file, createErr := os.Create(outPath)
		require.NoError(t, createErr)
		writer := csv.NewWriter(file)
		require.NoError(t, writer.Write([]string{"worker", "attempt", "startedAt", "elapsedMs", "status", "error"}))
		for _, sample := range samples {
			require.NoError(t, writer.Write([]string{
				strconv.Itoa(sample.Worker),
				strconv.Itoa(sample.Attempt),
				sample.StartedAt.UTC().Format(time.RFC3339Nano),
				strconv.FormatFloat(float64(sample.Elapsed)/float64(time.Millisecond), 'f', 3, 64),
				sample.Status,
				sample.Error,
			}))
		}
		writer.Flush()
		require.NoError(t, writer.Error())
		require.NoError(t, file.Close())
	}
	samplesAccess.Unlock()

	wall := time.Since(started)
	average := time.Duration(0)
	if successes > 0 {
		average = totalBuild / time.Duration(successes)
	}
	t.Logf("corpus=%s bytes=%d workers=%d gate=%d target=%d", corpusPath, len(corpus), workers, gateLimit, target)
	t.Logf("wall=%s successes=%d deferred=%d failed=%d cancelled=%d timeout=%d",
		wall.Round(time.Millisecond), successes, deferred, failed, cancelled, timedOut)
	t.Logf("average successful build=%s peakHeap=%d peakHeapSys=%d gate=%+v",
		average.Round(time.Microsecond), peakHeap.Load(), peakSys.Load(), gate.Stats())
	require.NotZero(t, successes, "the corpus must build at least once")
}
