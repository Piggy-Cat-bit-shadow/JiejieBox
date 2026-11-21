// Command bprobe measures what a non-Go thread costs the Go runtime after it has called into Go.
//
// # What it models
//
// A dispatch thread that calls one exported function and then goes back to waiting is the shape
// an Apple UI's control-plane traffic has: the call is rare, the thread is long-lived. Upstream,
// the first such call binds an m to the pthread and keeps it there until the thread exits
// (runtime/asm_arm64.s skips dropm when a pthread key exists, and runtime/proc.go's needAndBindM
// hands the thread its g0 through cgoBindM). That m brings a g0 stack and a signal stack with it,
// so N threads that have ever called Go are N x (m + 8 KiB + 32 KiB) of resident memory that no
// traffic is using.
//
// Group B makes the callback hand the m back every time, so the ms in flight are bounded by the
// number of calls running AT ONCE rather than by the number of threads that ever called.
//
// # Why it is a separate program and not a unit test
//
// The difference only exists while the calling threads are alive, and it only exists on a pthread
// platform. A Go test can call into Go all day without ever becoming a foreign thread.
//
// It lives under testdata/ and is compiled as its own module by probe_test.go.
package main

/*
#include <pthread.h>
#include <stdlib.h>
#include <unistd.h>

extern int goCallback(int id);

#define MAX_THREADS 512

static pthread_t threads[MAX_THREADS];
static volatile int thread_count = 0;
static volatile int release_flag = 0;
static volatile int errors = 0;

// parked_after_callback is the shape under test: call Go once, then stay alive doing nothing.
static void *parked_after_callback(void *arg) {
	int id = (int)(long)arg;
	int want = id * 7 + 1;
	int got = goCallback(id);
	if (got != want) {
		__sync_fetch_and_add(&errors, 1);
	}
	while (!release_flag) {
		usleep(5000);
	}
	return 0;
}

// churning_thread calls into Go repeatedly, so a released m has to be re-acquired many times
// from a foreign thread while other threads are doing the same.
static void *churning_thread(void *arg) {
	int id = (int)(long)arg;
	int want = id * 7 + 1;
	for (int i = 0; i < 1500; i++) {
		if (goCallback(id) != want) {
			__sync_fetch_and_add(&errors, 1);
		}
	}
	return 0;
}

static int spawn_parked(int count) {
	thread_count = 0;
	release_flag = 0;
	for (int i = 0; i < count; i++) {
		if (pthread_create(&threads[i], 0, parked_after_callback, (void *)(long)(i + 1)) != 0) {
			return -1;
		}
		thread_count++;
	}
	return 0;
}

static int spawn_churning(int count) {
	thread_count = 0;
	for (int i = 0; i < count; i++) {
		if (pthread_create(&threads[i], 0, churning_thread, (void *)(long)(i + 1)) != 0) {
			return -1;
		}
		thread_count++;
	}
	return 0;
}

static void release_parked(void) { release_flag = 1; }

static void join_all(void) {
	for (int i = 0; i < thread_count; i++) {
		pthread_join(threads[i], 0);
	}
	thread_count = 0;
}

static int error_count(void) { return errors; }
static void reset_errors(void) { errors = 0; }
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"sync"
	"time"
)

// keepAlive defeats the dead-store elimination of the callback's small allocation.
var keepAlive [][]byte

//export goCallback
func goCallback(id C.int) C.int {
	// A small allocation per call, so the callback is a real allocation site and not something the
	// compiler can reduce to an arithmetic expression.
	slice := make([]byte, 64)
	slice[0] = byte(id)
	keepAlive = append(keepAlive, slice)
	if len(keepAlive) > 4096 {
		keepAlive = keepAlive[:0]
	}
	return id*7 + 1
}

func main() {
	runtime.GOMAXPROCS(setting("BPROBE_GOMAXPROCS", 2))
	threads := setting("BPROBE_THREADS", 64)

	// One call from the main (Go) thread first: it initialises the callback path and creates the
	// first extra m, so the baseline below is measured with the machinery already up.
	if C.goCallback(0) != 1 {
		fmt.Println("callbackDisagreed=1")
	}

	baseline := readMemory()
	fmt.Printf("baseline.stacks=%d\n", baseline["/memory/classes/heap/stacks:bytes"])
	fmt.Printf("baseline.heap=%d\n", baseline["/memory/classes/heap/inuse:bytes"])

	if C.spawn_parked(C.int(threads)) != 0 {
		fmt.Println("spawnFailed=1")
		os.Exit(1)
	}
	// Long enough for every thread to have called in and parked, and for the runtime to have
	// grown whatever it is going to grow.
	time.Sleep(1500 * time.Millisecond)

	alive := readMemory()
	fmt.Printf("parked.stacks=%d\n", alive["/memory/classes/heap/stacks:bytes"])
	fmt.Printf("parked.heap=%d\n", alive["/memory/classes/heap/inuse:bytes"])
	fmt.Printf("parked.threads=%d\n", threads)
	fmt.Printf("parked.errors=%d\n", int(C.error_count()))

	C.release_parked()
	C.join_all()

	after := readMemory()
	fmt.Printf("after.stacks=%d\n", after["/memory/classes/heap/stacks:bytes"])
	fmt.Printf("after.heap=%d\n", after["/memory/classes/heap/inuse:bytes"])
	fmt.Printf("after.errors=%d\n", int(C.error_count()))

	// The concurrency half: many threads calling at once, each expecting its own answer. A wrongly
	// released m, a stale TLS slot, or a destructor freeing an m twice shows up here as a
	// disagreement or a crash rather than as a number that looks fine.
	//
	// It is also the cost measurement. Group B pays a needm and a dropm per call, and that price
	// is what decides whether it is worth the residency above: a control-plane call that costs
	// microseconds more is fine, one that costs hundreds is not.
	C.reset_errors()
	const churnCalls = 1500
	churnStart := time.Now()
	if C.spawn_churning(8) != 0 {
		fmt.Println("spawnFailed=1")
		os.Exit(1)
	}
	C.join_all()
	churnElapsed := time.Since(churnStart)
	fmt.Printf("churn.errors=%d\n", int(C.error_count()))
	fmt.Printf("churn.calls=%d\n", 8*churnCalls)
	fmt.Printf("churn.nsPerCall=%d\n", churnElapsed.Nanoseconds()/(8*churnCalls))
	afterChurn := readMemory()
	fmt.Printf("churn.stacks=%d\n", afterChurn["/memory/classes/heap/stacks:bytes"])

	// The combination phase: group B's foreign threads calling in WHILE group A3 retires
	// goroutines, with a traceback and a GC in the middle. This is the pairing that neither group's
	// own test covers - a callback returning as its m is dropped, concurrent with a P freeing stack
	// spans, concurrent with a GC walking both.
	if os.Getenv("BPROBE_MIXED") != "" {
		mixedPhase()
	}
	fmt.Println("done=1")
}

func mixedPhase() {
	C.reset_errors()
	if C.spawn_churning(8) != 0 {
		fmt.Println("spawnFailed=1")
		os.Exit(1)
	}
	for round := 0; round < 40; round++ {
		var group sync.WaitGroup
		for index := 0; index < 32; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				deep(index % 7 * 40)
			}(index)
		}
		group.Wait()
		if round%8 == 0 {
			// A traceback walks every live goroutine while foreign threads are inside Go, and a GC
			// runs across the callbacks: if an m or a stack were being released at the wrong moment,
			// this is where it would be read.
			traceEveryGoroutine()
			runtime.GC()
		}
	}
	C.join_all()
	fmt.Printf("mixed.errors=%d\n", int(C.error_count()))
	fmt.Printf("mixed.stacks=%d\n", readMemory()["/memory/classes/heap/stacks:bytes"])
}

//go:noinline
func deep(depth int) {
	var frame [256]byte
	frame[0] = byte(depth)
	frame[len(frame)-1] = byte(depth >> 3)
	if depth == 0 {
		return
	}
	deep(depth - 1)
}

func traceEveryGoroutine() {
	buffer := make([]byte, 1<<20)
	for {
		if runtime.Stack(buffer, true) < len(buffer) {
			return
		}
		buffer = make([]byte, len(buffer)*2)
	}
}

func readMemory() map[string]uint64 {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/heap/inuse:bytes"},
		{Name: "/memory/classes/heap/objects:bytes"},
	}
	metrics.Read(samples)
	values := make(map[string]uint64, len(samples))
	for _, sample := range samples {
		if sample.Value.Kind() == metrics.KindUint64 {
			values[sample.Name] = sample.Value.Uint64()
		}
	}
	return values
}

func setting(name string, fallback int) int {
	if text := os.Getenv(name); text != "" {
		if parsed, err := strconv.Atoi(text); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
