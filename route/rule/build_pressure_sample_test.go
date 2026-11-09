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
// The run stops at SING_BOX_PERF_TARGET successful builds (or the overall deadline). The gate is
// taken BEFORE the download, so the whole-fetch number folds the queue into the build: a change
// that only moves the queue would look like a build regression. The gate's observer therefore
// reports the wait, and every attempt records three separate figures:
//
//	queueMs  time spent waiting for the reservation (the gate's own admission)
//	buildMs  the rest of the fetch: request, download, parse, compile and swap
//	fetchMs  queueMs + buildMs, which is what "average successful build" used to report alone
//
// Run the baseline and the candidate with the SAME corpus, workers, gate and target, and with the
// SAME instrument: compare only runs that reached the target, and report cancelled/deferred/
// timeout attempts separately. The sampler records peak heap and heap system bytes; RSS, CPU and
// the request count come from the process observer outside the test.

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
	Fetch     time.Duration
	Queue     time.Duration
	Status    string
	Error     string
}

// sampleWorkerKey tags an attempt's context with its worker, so the gate observer - which runs on
// the acquiring goroutine but knows nothing about this test - can attribute an admission to the
// attempt that caused it.
type sampleWorkerKey struct{}

type buildPressureSampler struct {
	access  sync.Mutex
	samples []*buildSample
}

func (s *buildPressureSampler) appendSample(sample *buildSample) int {
	s.access.Lock()
	defer s.access.Unlock()
	s.samples = append(s.samples, sample)
	return len(s.samples) - 1
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
	sampler := &buildPressureSampler{}
	// One index per worker: a worker runs one attempt at a time, so the index that is current when
	// the observer fires is the attempt that is waiting. The observer runs inside Acquire, on the
	// worker's own goroutine, which is what makes that true without a per-goroutine lookup.
	currentAttempt := make([]atomic.Int64, workers)
	for worker := range currentAttempt {
		currentAttempt[worker].Store(-1)
	}
	gate.Observe(func(attemptContext context.Context, admission buildgate.Admission) {
		worker, ok := attemptContext.Value(sampleWorkerKey{}).(int)
		if !ok {
			return
		}
		index := currentAttempt[worker].Load()
		if index < 0 {
			return
		}
		sampler.access.Lock()
		sampler.samples[index].Queue = admission.Wait
		sampler.access.Unlock()
	})
	ctx, cancel := context.WithTimeout(buildgate.ContextWithGate(context.Background(), gate), deadline)
	defer cancel()

	var completed atomic.Int32
	var reserved atomic.Int32
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
			workerContext := context.WithValue(ctx, sampleWorkerKey{}, worker)
			ruleSet, createErr := NewRemoteRuleSet(workerContext, log.NewNOPFactory().Logger(), "sample", option.RuleSet{
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
				if workerContext.Err() != nil {
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
				sample := &buildSample{
					Worker:    worker,
					Attempt:   attempt,
					StartedAt: attemptStarted,
				}
				index := sampler.appendSample(sample)
				currentAttempt[worker].Store(int64(index))
				fetchErr := ruleSet.fetch(workerContext, false)
				currentAttempt[worker].Store(-1)
				sample.Fetch = time.Since(attemptStarted)
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
			}
		}(worker)
	}
	runners.Wait()
	close(stopSampling)
	<-samplingDone

	var successes, deferred, failed, cancelled, timedOut int
	var totalQueue, totalBuild, totalFetch time.Duration
	sampler.access.Lock()
	for _, sample := range sampler.samples {
		switch sample.Status {
		case "success":
			successes++
			totalQueue += sample.Queue
			totalBuild += sample.Fetch - sample.Queue
			totalFetch += sample.Fetch
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
		require.NoError(t, writer.Write([]string{
			"worker", "attempt", "startedAt", "queueMs", "buildMs", "fetchMs", "status", "error",
		}))
		for _, sample := range sampler.samples {
			require.NoError(t, writer.Write([]string{
				strconv.Itoa(sample.Worker),
				strconv.Itoa(sample.Attempt),
				sample.StartedAt.UTC().Format(time.RFC3339Nano),
				strconv.FormatFloat(float64(sample.Queue)/float64(time.Millisecond), 'f', 3, 64),
				strconv.FormatFloat(float64(sample.Fetch-sample.Queue)/float64(time.Millisecond), 'f', 3, 64),
				strconv.FormatFloat(float64(sample.Fetch)/float64(time.Millisecond), 'f', 3, 64),
				sample.Status,
				sample.Error,
			}))
		}
		writer.Flush()
		require.NoError(t, writer.Error())
		require.NoError(t, file.Close())
	}
	sampler.access.Unlock()

	wall := time.Since(started)
	average := func(total time.Duration) time.Duration {
		if successes == 0 {
			return 0
		}
		return total / time.Duration(successes)
	}
	t.Logf("corpus=%s bytes=%d workers=%d gate=%d target=%d", corpusPath, len(corpus), workers, gateLimit, target)
	t.Logf("wall=%s successes=%d deferred=%d failed=%d cancelled=%d timeout=%d",
		wall.Round(time.Millisecond), successes, deferred, failed, cancelled, timedOut)
	t.Logf("average queue=%s build=%s fetch=%s peakHeap=%d peakHeapSys=%d gate=%+v",
		average(totalQueue).Round(time.Microsecond),
		average(totalBuild).Round(time.Microsecond),
		average(totalFetch).Round(time.Microsecond),
		peakHeap.Load(), peakSys.Load(), gate.Stats())
	require.NotZero(t, successes, "the corpus must build at least once")
}
