package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	goprofile "runtime/pprof"
	"strconv"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

// earliestObservable is the earliest instant this program can observe.
//
// Package-level variable initialisation runs after the Go runtime is up and after every imported
// package's init, so this is NOT process start: it is the first moment a measurement binary can
// stamp. Everything before it - runtime arena setup, the scheduler, every dependency's init - is
// outside this ledger, and the earliest_observable record says so in its unobservable list instead
// of pretending the number is the whole process.
var earliestObservable = time.Now()

// forcedGCRounds is how many collections a forced reading takes before it is read.
//
// One is not enough: the first collection promotes surviving objects and runs finalisers, and the
// second reclaims what the first made unreachable. Two is the convention the tests already in this
// tree use for a live-heap reading, and keeping the same number is what makes the two comparable.
// A measurement taken with five collections was tried and produced the same reading to within the
// run-to-run spread, so the extra collections cost time and buy nothing.
const forcedGCRounds = 2

// childRun is one measured process: one configuration, one GC discipline, one activity history.
//
// One run per process is the point. GOMAXPROCS, MemProfileRate, the adaptive stack size and the
// GODEBUG defaults are process-global and partly sticky - a second case in the same process would
// inherit the first one's warmed pools, adapted stacks and resident heap, and every "independent
// startup" comparison in the round would be measuring the previous case.
type childRun struct {
	spec   runSpec
	runID  string
	pid    int
	writer *bufio.Writer

	sampler  *sampler
	identity identity

	seq           int
	stageIndex    int
	previousAt    time.Time
	previous      readings
	previousStage string
	havePrev      bool
	entryRead     readings

	notes []string

	// fixture state (the harness's own cost, on its own boundary)
	probeServer *probeServer
	destination *destinationServer
	inboundPort uint16

	// provider state: the node document a local provider reads, and the directory that owns it.
	// The child, not the fixture, owns the file's lifetime so a run cannot leave a subscription
	// document behind in the working directory.
	providerDirectory string
	providerPath      string
	providerBytes     int

	// product state
	ctx          context.Context
	options      option.Options
	configBytes  []byte
	fixture      fixtureConfig
	instance     *box.Box
	connections  adapter.ConnectionManager
	socksAddress string

	harness *trafficHarness

	entryGoroutines int
}

func runChild(spec runSpec, runID string, output io.Writer) (returnErr error) {
	instance := &childRun{
		spec:   spec,
		runID:  runID,
		pid:    os.Getpid(),
		writer: bufio.NewWriterSize(output, 64<<10),
	}
	defer func() { _ = instance.writer.Flush() }()
	instance.sampler = newSampler()
	instance.identity = captureIdentity(instance.sampler)
	instance.entryGoroutines = runtime.NumGoroutine()

	defer func() {
		if recovered := recover(); recovered != nil {
			// A panic still has to leave a record: the parent's rule is "no run_end means not
			// evidence", and this makes the reason visible in the ledger as well as in stderr.
			instance.writeFailure(fmt.Errorf("panic: %v", recovered))
			panic(recovered)
		}
		if returnErr != nil {
			instance.writeFailure(returnErr)
		}
	}()

	// 1. The earliest point this process can speak from.
	instance.note("Go runtime startup and every imported package's init are complete before this " +
		"record; the runtime publishes no accounting for memory held before the first statement of " +
		"main, so that prefix is not in this ledger. -inittrace reports the per-package init cost " +
		"that IS attributable, on the child's stderr.")
	instance.record(stageEarliestObservable)

	// 2. Setup: the registry construction a libbox caller does before it can parse anything.
	instance.ctx = include.Context(context.Background())
	instance.record(stageSetup)

	// 3. Fixture servers and the inbound port. These are the harness's listeners; charging them to
	//    their own stage keeps them out of the product's construction delta.
	var err error
	if hasActivity(spec, "probe") {
		instance.probeServer, err = startProbeServer()
		if err != nil {
			return err
		}
	}
	if hasActivity(spec, "traffic") {
		instance.destination, err = startDestinationServer()
		if err != nil {
			return err
		}
	}
	instance.inboundPort, err = reservePort()
	if err != nil {
		return fmt.Errorf("reserve inbound port: %w", err)
	}
	instance.record(stageFixtureServers)

	// 4. The document itself.
	probeURL := "https://probe.memory-study.invalid/generate_204"
	if instance.probeServer != nil {
		probeURL = instance.probeServer.URL()
	}
	if spec.ConfigSource == "provider" {
		if err = instance.writeProviderDocument(); err != nil {
			return err
		}
	}
	instance.fixture = generateFixtureWithProvider(spec, instance.inboundPort, probeURL, instance.providerPath)
	instance.configBytes = instance.fixture.Document
	// The document's own identity is recorded with the run, before the first reading that charges
	// it. The digest is what turns "the fixture did not change" into something the gate can check:
	// two ledgers with different documents are not two measurements of the same thing, whatever
	// the version string says. The reserved inbound port, the fixture server's port and the
	// provider's temporary directory are ephemeral, so they are redacted from the hash.
	instance.identity.Fixture = fixtureIdentity{
		Version: fixtureVersion,
		Digest: fixtureFingerprint(instance.fixture.Document, probeURL, instance.providerPath,
			strconv.Itoa(int(instance.inboundPort))),
	}
	instance.note(fmt.Sprintf("fixture document: %d bytes, %d nodes, %d groups, node_proto=%s, config_source=%s",
		len(instance.fixture.Document), instance.fixture.Nodes, instance.fixture.Groups, spec.NodeProto,
		spec.ConfigSource))
	if instance.providerBytes > 0 {
		instance.note(fmt.Sprintf("provider node document: %d bytes at %s, %d member edges (%d provider edges + %d node declarations)",
			instance.providerBytes, instance.providerDirectory, instance.fixture.MemberEdges,
			instance.fixture.Groups, instance.fixture.Nodes))
	}
	instance.record(stageConfigBuild)

	// 5. Decode. Both the document and the option tree are alive at this reading on purpose: that
	//    simultaneous existence is the configuration-processing peak, and it is a different number
	//    from the tree's own size.
	instance.options, err = singjson.UnmarshalExtendedContext[option.Options](instance.ctx, instance.configBytes)
	if err != nil {
		return fmt.Errorf("parse the fixture document: %w", err)
	}
	instance.record(stageConfigParse, &instance.options, instance.configBytes)

	// The decoder's input dies here. Below this line the ledger measures what the RUNTIME holds,
	// not what the caller happened to keep in a local. Both references have to go: the fixture still
	// holds the same backing array, and clearing only one would leave the document live and charge
	// it to the runtime.
	instance.configBytes = nil
	instance.fixture.Document = nil

	// 6. Construction. The option tree is still alive at this reading, and that is deliberate: it is
	//    the state a caller is in when box.New returns, and the simultaneous existence of the tree
	//    and the runtime is a real peak.
	instance.instance, err = box.New(box.Options{Context: instance.ctx, Options: instance.options})
	if err != nil {
		return fmt.Errorf("box.New: %w", err)
	}
	// The connection manager is registered by Box under the INTERFACE type, so it is read back with
	// FromContext; PtrFromContext is for services registered as a concrete pointer and would panic
	// on the interface assertion.
	if manager := service.FromContext[adapter.ConnectionManager](instance.ctx); manager != nil {
		instance.connections = manager
	}
	instance.record(stageConstruct, instance.instance, &instance.options)

	// The caller's option tree is released here. Whether that actually frees anything is the
	// measurement: a runtime that keeps the applied options (for change detection) shares those
	// objects, and the drop will be small. Either answer is the truth about ownership.
	instance.options = option.Options{}
	instance.note("the caller's option tree was released after construct; later stages measure what " +
		"the runtime itself retains")

	// 7. Start, then prove the inbound is accepting. Start returning without an error is the
	//    product's own readiness contract for listeners; the dial below is what turns that contract
	//    into an observation.
	if err = instance.instance.Start(); err != nil {
		return fmt.Errorf("box.Start: %w", err)
	}
	instance.record(stageServiceStart, instance.instance)

	instance.socksAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(instance.inboundPort)))
	if err = instance.waitInboundReady(); err != nil {
		return err
	}
	instance.record(stageReady, instance.instance)

	// 8. Idle checkpoints. Idle means the process was left alone: no forced collection happens in a
	//    natural run, and the sleep is the definition of the interval rather than a substitute for a
	//    readiness signal.
	idleStart := time.Now()
	for _, raw := range spec.Idle {
		duration, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return fmt.Errorf("parse -idle entry %q: %w", raw, parseErr)
		}
		if duration > 0 {
			if remaining := time.Until(idleStart.Add(duration)); remaining > 0 {
				time.Sleep(remaining)
			}
		}
		instance.record(idleStage(raw))
	}

	// 9. Probe (URL test) round, if this case asks for one.
	instance.runProbeStage()

	// 10. Real traffic through the real relay, with integrity and half-close verification.
	instance.runTrafficStage()

	// 11. Idle again, after the activity. This is the reading that answers "did the activity leave
	//     something behind", and it is taken with the Box still running.
	if len(spec.Activity) > 0 && !allActivityNone(spec.Activity) {
		if duration, parseErr := time.ParseDuration(spec.PostIdle); parseErr != nil {
			return fmt.Errorf("parse -post-idle %q: %w", spec.PostIdle, parseErr)
		} else if duration > 0 {
			time.Sleep(duration)
		}
		instance.record(idleAgainStage(spec.PostIdle))
	}

	// 11. Publication refreshes. These come after the activity readings on purpose: a refresh
	//     allocates a generation of objects and retires the previous one, so taking it before the
	//     idle checkpoint would charge its garbage to "idle".
	instance.runProviderRefreshStages()

	// 12. Teardown. The references are dropped BEFORE the reading so that a forced collection can
	//     reclaim them: a close stage that still holds the Box would report the cost of the object
	//     it is supposed to prove is gone.
	returnErr = instance.closeAndRecord()
	if returnErr != nil {
		return returnErr
	}

	// 13. Last word.
	instance.identity.Policy.GODebugNonDefault = readGODebugEvents()
	instance.note(fmt.Sprintf("goroutines: %d before the first reading, %d at run end",
		instance.entryGoroutines, runtime.NumGoroutine()))
	if instance.probeServer != nil {
		instance.note(fmt.Sprintf("the fixture probe server answered %d requests", instance.probeServer.Requests()))
	}
	instance.record(stageRunEnd)

	// The profile is written AFTER the last reading on purpose: profiling machinery and the profile
	// file itself must not appear in any number the ledger reports. It exists so a retained or
	// allocated byte can be attributed to a call site instead of argued from struct sizes.
	if heapProfilePath != "" {
		if profileErr := writeHeapProfiles(heapProfilePath); profileErr != nil {
			fmt.Fprintln(os.Stderr, "memorystudy: heap profile:", profileErr)
		}
	}
	return nil
}

// heapProfilePath is set by -heapprofile in child mode. It is a package variable rather than a
// childRun field so the command line can set it before the measured object exists.
var heapProfilePath string

// writeHeapProfiles writes the live heap and the cumulative allocation sites.
//
// Two files, because they answer different questions: inuse_space is what the Box still holds at the
// end of a run, alloc_space is what the run spent to get there. A changed provider refresh is
// allocated-but-not-retained by construction, so the second file is the only one that can price it.
func writeHeapProfiles(path string) error {
	if err := writeProfile(path, "heap"); err != nil {
		return err
	}
	return writeProfile(path+".allocs", "allocs")
}

func writeProfile(path string, name string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	profile := goprofile.Lookup(name)
	if profile == nil {
		_ = file.Close()
		return fmt.Errorf("profile %q is not available", name)
	}
	if err = profile.WriteTo(file, 0); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// closeAndRecord tears the Box down and records what is left.
func (c *childRun) closeAndRecord() error {
	if c.harness != nil {
		c.harness.Close()
		_ = c.harness.WaitDrained(c.spec.HeldFlows, 15*time.Second)
	}
	// The fixture's own listeners are closed BEFORE the teardown reading. They are the harness, and
	// an accept goroutine that only exists to serve the measurement must not appear in the number
	// that is supposed to describe the product's release.
	c.stopFixtureServers()

	var closeErr error
	if c.instance != nil {
		closeErr = c.instance.Close()
	}
	// The provider document is harness input, not product state: close it before the teardown
	// reading so a temporary directory cannot appear as retained heap or as a leaked handle.
	if c.providerDirectory != "" {
		_ = os.RemoveAll(c.providerDirectory)
		c.providerDirectory = ""
		c.providerPath = ""
	}
	// Drop every reference the ledger could still be holding through this struct. Setting the
	// fields is what makes the close reading a measurement of the release rather than of the
	// instrumentation.
	c.instance = nil
	c.connections = nil
	c.options = option.Options{}
	c.configBytes = nil
	c.fixture.Document = nil
	c.fixture.NodeTags = nil
	c.fixture.GroupTags = nil
	// The context is the caller's. Box registers its services INTO the caller's registry, so a
	// caller that keeps the context keeps the Box's connection manager, URL-test history and rule
	// index alive after Close - the objects are reachable, so they are live heap. Releasing the
	// context here is what "the caller dropped everything" looks like; a product that keeps its
	// context across a restart pays the difference.
	c.ctx = nil

	settled, count := c.settleGoroutines(10 * time.Second)
	c.record(stageClose)
	if !settled {
		c.note(fmt.Sprintf("the goroutine count was still changing when the close reading was "+
			"taken (%d now, %d at the first reading)", count, c.entryGoroutines))
	}
	if closeErr != nil {
		return fmt.Errorf("box.Close: %w", closeErr)
	}
	return nil
}

// writeProviderDocument materialises the node document a local provider reads.
//
// The bytes are written to a directory the child owns and then dropped: the product re-reads the
// file from disk at Start, so keeping a second copy in the harness would charge the measurement for
// an input the product never holds. The file is created before the configuration document because
// the configuration names its path.
func (c *childRun) writeProviderDocument() error {
	directory, err := os.MkdirTemp("", "memorystudy-provider-")
	if err != nil {
		return fmt.Errorf("create provider directory: %w", err)
	}
	document := generateProviderDocument(c.spec, 0)
	path := filepath.Join(directory, "nodes.json")
	if err = os.WriteFile(path, document, 0o644); err != nil {
		_ = os.RemoveAll(directory)
		return fmt.Errorf("write provider document: %w", err)
	}
	c.providerDirectory = directory
	// Forward slashes are what a JSON configuration in this tree uses: a raw backslash would have to
	// be escaped, and the watcher resolves the configured string rather than a cleaned path.
	c.providerPath = filepath.ToSlash(path)
	c.providerBytes = len(document)
	return nil
}

// runProviderRefreshStages drives the product's own publication transaction twice: once with the
// node document it already published, and once with a document whose every node changed.
//
// # What this measures, and what it does not
//
// Both refreshes go through adapter.Provider.UpdateNodes + UpdateGroups, which is the transaction the
// local provider's reloadFile itself calls. They are driven with a freshly parsed document rather
// than by rewriting the file and waiting for fsnotify: the watcher's callback is asynchronous and
// the ledger has no synchronous boundary to read at, so a file-rewrite variant would measure the
// sleep in this harness as much as the refresh.
func (c *childRun) runProviderRefreshStages() {
	if c.spec.ConfigSource != "provider" {
		return
	}
	manager := service.FromContext[adapter.ProviderManager](c.ctx)
	if manager == nil {
		reason := "the provider manager is not in the context"
		c.recordStatus(stageProviderRefreshSame, statusFailed, reason)
		c.recordStatus(stageProviderRefreshChanged, statusFailed, reason)
		return
	}
	provider, ok := manager.Get(fixtureProviderTag)
	if !ok {
		reason := "provider " + fixtureProviderTag + " is not registered; the publication transaction is not observable"
		c.recordStatus(stageProviderRefreshSame, statusFailed, reason)
		c.recordStatus(stageProviderRefreshChanged, statusFailed, reason)
		return
	}
	updater, ok := provider.(interface {
		UpdateNodes(outOpts []option.Outbound) error
	})
	if !ok {
		reason := fmt.Sprintf("%T does not expose UpdateNodes", provider)
		c.recordStatus(stageProviderRefreshSame, statusUnsupported, reason)
		c.recordStatus(stageProviderRefreshChanged, statusUnsupported, reason)
		return
	}

	// The unchanged refresh: the same bytes the provider already published. The product's
	// reflect.DeepEqual check should decide every node is unchanged and touch no live object, so the
	// reading after it is the transaction's own cost.
	same, err := parser.ParseSubscription(c.ctx, string(c.readProviderDocument()), nil, fixtureProviderTag)
	if err != nil {
		c.recordStatus(stageProviderRefreshSame, statusFailed, "parse the unchanged provider document: "+err.Error())
	} else if same == nil {
		c.recordStatus(stageProviderRefreshSame, statusUnsupported, "the provider document parsed to no nodes")
	} else if err = updater.UpdateNodes(same); err != nil {
		c.recordStatus(stageProviderRefreshSame, statusFailed, "publish the unchanged document: "+err.Error())
	} else if groupErr := updateProviderGroups(provider); groupErr != nil {
		c.recordStatus(stageProviderRefreshSame, statusFailed, "rebuild the groups: "+groupErr.Error())
	} else {
		// No keep: UpdateNodes does not retain the parsed slice, it retains a copy of each node's
		// option struct in appliedOutbounds. Pinning the slice here would charge the reading for a
		// document the product drops, which is exactly the mistake the keeps parameter exists to avoid.
		c.record(stageProviderRefreshSame)
	}

	// The changed refresh: every node's port moved, so every node is rebuilt and the previous
	// generation is retired through the same hand-off a real subscription update uses. The document
	// bytes are generated here rather than written to the provider's path because this measures the
	// transaction, not the file watcher.
	changed := generateProviderDocument(c.spec, 1)
	changedOptions, err := parser.ParseSubscription(c.ctx, string(changed), nil, fixtureProviderTag)
	if err != nil {
		c.recordStatus(stageProviderRefreshChanged, statusFailed, "parse the changed provider document: "+err.Error())
		return
	}
	if err = updater.UpdateNodes(changedOptions); err != nil {
		c.recordStatus(stageProviderRefreshChanged, statusFailed, "publish the changed document: "+err.Error())
		return
	}
	if groupErr := updateProviderGroups(provider); groupErr != nil {
		c.recordStatus(stageProviderRefreshChanged, statusFailed, "rebuild the groups: "+groupErr.Error())
		return
	}
	// No keeps: the changed document's per-node option objects survive through appliedOutbounds,
	// which the provider itself holds. The slice and the bytes are dropped by the product, so pinning
	// them would report the harness's copy as retained heap.
	c.record(stageProviderRefreshChanged)

	// A second changed publication, with a different address again. The pair answers the question a
	// single measurement cannot: is the retained cost of a replacement a one-time high-water mark,
	// or does it grow with every refresh?
	again := generateProviderDocument(c.spec, 2)
	againOptions, err := parser.ParseSubscription(c.ctx, string(again), nil, fixtureProviderTag)
	if err != nil {
		c.recordStatus(stageProviderRefreshChangedAgain, statusFailed, "parse the second changed provider document: "+err.Error())
		return
	}
	if err = updater.UpdateNodes(againOptions); err != nil {
		c.recordStatus(stageProviderRefreshChangedAgain, statusFailed, "publish the second changed document: "+err.Error())
		return
	}
	if groupErr := updateProviderGroups(provider); groupErr != nil {
		c.recordStatus(stageProviderRefreshChangedAgain, statusFailed, "rebuild the groups: "+groupErr.Error())
		return
	}
	c.record(stageProviderRefreshChangedAgain)
}

// readProviderDocument re-reads the provider file the product reads, rather than a harness copy.
func (c *childRun) readProviderDocument() []byte {
	if c.providerDirectory == "" {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(c.providerDirectory, "nodes.json"))
	if err != nil {
		return nil
	}
	return content
}

// updateProviderGroups performs the second half of the product's publication transaction. A
// provider whose adapter cannot rebuild memberships is not a supported shape here, and its absence
// has to be visible rather than silently skipping the step the measurement depends on.
func updateProviderGroups(provider adapter.Provider) error {
	updater, ok := provider.(interface{ UpdateGroups() error })
	if !ok {
		return fmt.Errorf("%T does not expose UpdateGroups", provider)
	}
	return updater.UpdateGroups()
}

// stopFixtureServers closes the harness's listeners.
func (c *childRun) stopFixtureServers() {
	if c.probeServer != nil {
		c.probeServer.Close()
		c.probeServer = nil
	}
	if c.destination != nil {
		c.destination.Close()
		c.destination = nil
	}
}

// settleGoroutines waits for the goroutine count to stop falling.
//
// # Why this does not compare against the count at process start
//
// A Go process starts with one goroutine and gains the runtime's own background goroutines - the
// forced-GC goroutine, the scavenger, the finaliser and, at the first collection, one mark worker
// per P - as it does work. Those never go away, so "back to the count at main" is a test that can
// only fail: it reported a residual of twenty-four goroutines for a Box that had released
// everything, and the twenty-four were the runtime's.
//
// What is actually being asked is "has teardown finished". So this waits until the count has been
// unchanged across consecutive checks, and reports both the convergence and the number, leaving the
// absolute residual for the gate to compare against its baseline-derived threshold.
func (c *childRun) settleGoroutines(timeout time.Duration) (bool, int) {
	const stableChecks = 3
	deadline := time.Now().Add(timeout)
	previous := runtime.NumGoroutine()
	stable := 0
	for {
		time.Sleep(50 * time.Millisecond)
		current := runtime.NumGoroutine()
		if current == previous {
			stable++
			if stable >= stableChecks {
				return true, current
			}
		} else {
			stable = 0
			previous = current
		}
		if time.Now().After(deadline) {
			return false, current
		}
	}
}

// waitInboundReady dials the inbound until it accepts, so that `ready` means the listener answers
// rather than that Start returned.
func (c *childRun) waitInboundReady() error {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for {
		conn, err := net.DialTimeout("tcp4", c.socksAddress, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("the inbound at %s never accepted a connection: %w", c.socksAddress, lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runProbeStage drives one URL-test round and records what it cost.
func (c *childRun) runProbeStage() {
	if !hasActivity(c.spec, "probe") {
		return
	}
	// A probe is a real HTTP request through each member. The trojan and heavy fixtures point at
	// TEST-NET-2, so driving a round against them would measure a TCP connect timeout and then
	// report it as "probing is expensive". The stage says it is unsupported instead.
	if c.spec.NodeProto != "direct" {
		c.recordStatus(stageProbe, statusUnsupported,
			"probe members are not dialable in this fixture: node_proto="+c.spec.NodeProto+
				" points at TEST-NET-2, so a round would measure a connect timeout; use -node-proto=direct")
		return
	}
	if c.fixture.ProbeTag == "" {
		c.recordStatus(stageProbe, statusUnsupported, "the fixture has no URL-test group to drive")
		return
	}
	outbound, ok := c.instance.Outbound().Outbound(c.fixture.ProbeTag)
	if !ok {
		c.recordStatus(stageProbe, statusFailed, "the URL-test group "+c.fixture.ProbeTag+" is not registered")
		return
	}
	tester, ok := outbound.(interface {
		URLTest(ctx context.Context) (map[string]uint16, error)
	})
	if !ok {
		c.recordStatus(stageProbe, statusUnsupported,
			fmt.Sprintf("%T does not expose a synchronous URL test", outbound))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	delays, probeErr := tester.URLTest(ctx)
	report := &probeReport{
		Group:   c.fixture.ProbeTag,
		URL:     c.fixture.ProbeURL,
		Members: len(c.fixture.NodeTags),
	}
	for _, delay := range delays {
		if delay > 0 {
			report.Succeeded++
		} else {
			report.Failed++
		}
	}
	if probeErr != nil {
		report.Error = probeErr.Error()
		c.recordProbe(stageProbe, report, statusFailed, c.instance)
		return
	}
	c.recordProbe(stageProbe, report, statusOK, c.instance)
}

// runTrafficStage pushes real bytes through the real relay and records both the held state and the
// verified state.
func (c *childRun) runTrafficStage() {
	if !hasActivity(c.spec, "traffic") {
		return
	}
	c.harness = newTrafficHarness(c.socksAddress, c.destination, c.spec.HeldFlows)
	if err := c.harness.Open(c.spec.HeldFlows); err != nil {
		c.recordStatus(stageTrafficHeld, statusFailed, "open flows: "+err.Error())
		return
	}
	// Readiness for "the flows are held" is the product's own connection counter reaching the flow
	// count, not a sleep: a relay that accepted the SOCKS request but has not registered the
	// connection is a different process state, and the held reading must not be taken in it.
	if err := c.waitConnections(c.spec.HeldFlows, 15*time.Second); err != nil {
		c.note(err.Error())
	}
	c.record(stageTrafficHeld, c.harness)

	if err := c.harness.Verify(c.spec.TrafficBytes); err != nil {
		c.recordStatus(stageTrafficVerified, statusFailed, "verify flows: "+err.Error())
		return
	}
	report := c.harness.Report()
	status := statusOK
	if !report.Verified {
		status = statusFailed
	}
	c.recordTraffic(stageTrafficVerified, &report, status, c.harness)
}

// waitConnections waits for the product's connection manager to report the flows.
func (c *childRun) waitConnections(want int, timeout time.Duration) error {
	if c.connections == nil {
		return fmt.Errorf("the connection manager is not in the context, so flow readiness was not " +
			"confirmed against the product's own counter")
	}
	return waitUntil(timeout, func() bool { return c.connections.Count() >= want }, func() string {
		return fmt.Sprintf("the connection manager holds %d of %d flows", c.connections.Count(), want)
	})
}

// takeReading collects one full reading.
func (c *childRun) takeReading() readings {
	reading := readings{Go: c.sampler.readGo()}
	reading.Goroutines = c.sampler.readGoroutines()
	unsupported := make(map[string]string, 8)
	readHostProcess(&reading.Process, unsupported)
	reading.Process.NativeResidualBytesProxy = nativeResidual(
		reading.Process.PrivateCommitBytes,
		reading.Go.TotalBytes,
	)
	reading.Objects = c.countObjects()
	reading.Device = deviceReadings{
		Supported:          c.identity.Device.Supported,
		PhysFootprintBytes: nil,
		PeakFootprintBytes: nil,
		Reason:             c.identity.Device.Reason,
	}
	unsupported["device.phys_footprint_bytes"] = c.identity.Device.Reason
	unsupported["device.peak_footprint_bytes"] = c.identity.Device.Reason
	reading.Unsupported = mergeUnsupported(unsupported, c.sampler.unsupportedNow())
	return reading
}

// countObjects reports what is running. These are exact counts, so zero here is a real zero: the
// fixture configures no providers and no endpoints, and the ledger says so by counting them.
func (c *childRun) countObjects() objectReadings {
	objects := objectReadings{}
	if c.instance != nil {
		if manager := c.instance.Outbound(); manager != nil {
			objects.Outbounds = len(manager.Outbounds())
		}
		if manager := c.instance.Inbound(); manager != nil {
			objects.Inbounds = len(manager.Inbounds())
		}
		if manager := c.instance.Endpoint(); manager != nil {
			objects.Endpoints = len(manager.Endpoints())
		}
	}
	if c.connections != nil {
		objects.Connections = c.connections.Count()
	}
	if c.harness != nil {
		objects.SessionsHeld = len(c.harness.Held())
	}
	return objects
}

// nativeResidual is the part of the host's memory that Go's own accounting does not explain.
//
// It is a PROXY and it can be negative: Go's total includes memory the operating system has not
// committed, and a mapped file counts differently on every platform. It exists to answer "the Go
// ledger went down and the footprint did not - where else should I look", not to be a footprint.
func nativeResidual(privateCommit *uint64, goTotal *uint64) *int64 {
	if privateCommit == nil || goTotal == nil {
		return nil
	}
	delta, ok := signedDelta(*privateCommit, *goTotal)
	if !ok {
		return nil
	}
	return delta
}

type stagePayload struct {
	status  string
	errText string
	traffic *trafficReport
	probe   *probeReport
}

func (c *childRun) record(stage string, keeps ...any) {
	c.emit(stage, stagePayload{status: statusOK}, keeps...)
}

func (c *childRun) recordStatus(stage string, status string, errText string, keeps ...any) {
	c.emit(stage, stagePayload{status: status, errText: errText}, keeps...)
}

func (c *childRun) recordTraffic(stage string, report *trafficReport, status string, keeps ...any) {
	c.emit(stage, stagePayload{status: status, traffic: report}, keeps...)
}

func (c *childRun) recordProbe(stage string, report *probeReport, status string, keeps ...any) {
	c.emit(stage, stagePayload{status: status, probe: report}, keeps...)
}

// emit takes one reading and writes it.
//
// # Why the keeps are parameters
//
// `runtime.KeepAlive` is what makes a heap reading mean anything. The compiler is free to treat a
// local as dead after its last syntactic use, and a forced collection inside the reading then
// reclaims the very object the reading is supposed to include: a census of a thousand nodes once
// reported ten bytes per node for that exact reason. Passing the objects in and keeping them alive
// AFTER the reads - not before - is what pins them across the collection.
func (c *childRun) emit(stage string, payload stagePayload, keeps ...any) {
	if c.spec.GCMode == "forced" {
		// forcedGCRounds collections, not one: the first promotes and finalises, the following ones
		// reclaim what the first made unreachable and let the sweep finish. Two is the repo's
		// existing convention for a live-heap reading and is kept so numbers stay comparable with
		// the tests already in the tree; -gc natural never comes through here at all.
		for round := 0; round < forcedGCRounds; round++ {
			runtime.GC()
		}
	}
	observedAt := time.Now()
	reading := c.takeReading()

	delta := deltas{}
	if c.entryRead.Go.LiveHeapBytes != nil || c.entryRead.Go.TotalBytes != nil {
		delta.FromEntry = readingDeltaOf("entry:"+stageEarliestObservable, c.entryRead, reading, c.spec.Nodes)
	}
	if c.havePrev {
		delta.FromPrevious = readingDeltaOf("previous:"+c.previousStage, c.previous, reading, c.spec.Nodes)
	}

	record := stageRecord{
		SchemaVersion:   schemaVersion,
		Record:          recordStage,
		Status:          payload.status,
		RunID:           c.runID,
		PID:             c.pid,
		Seq:             c.seq,
		Stage:           stage,
		StageIndex:      c.stageIndex,
		ObservedAt:      observedAt.UTC().Format(time.RFC3339Nano),
		OffsetNS:        observedAt.Sub(earliestObservable).Nanoseconds(),
		StageDurationNS: observedAt.Sub(c.previousAt).Nanoseconds(),
		Spec:            c.spec,
		Identity:        c.identity,
		Readings:        reading,
		Deltas:          delta,
		Traffic:         payload.traffic,
		Probe:           payload.probe,
		Notes:           c.notes,
		Error:           payload.errText,
	}
	if stage == stageEarliestObservable {
		record.Unobservable = []string{
			"Go runtime startup, and the init of every imported package, ran before this record; the " +
				"runtime publishes no accounting for memory held before main, so the pre-main prefix is " +
				"NOT captured by this ledger",
		}
	}
	c.writeRecord(record)

	// KeepAlive goes after the reads on purpose: the forced collection above happens first, and an
	// object kept alive only before it would already be gone when the reading was taken.
	for _, keep := range keeps {
		runtime.KeepAlive(keep)
	}

	c.notes = nil
	if stage == stageEarliestObservable {
		c.entryRead = reading
	}
	c.previous = reading
	c.previousStage = stage
	c.havePrev = true
	c.seq++
	c.stageIndex++
	c.previousAt = observedAt
}

func (c *childRun) note(text string) {
	c.notes = append(c.notes, text)
}

func (c *childRun) writeRecord(record stageRecord) {
	encoded, err := json.Marshal(record)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "memorystudy: cannot encode the %s record: %v\n", record.Stage, err)
		return
	}
	if _, err = c.writer.Write(encoded); err != nil {
		return
	}
	_ = c.writer.WriteByte('\n')
	// Flushed per record, not at exit: a child that dies mid-run has to leave the records that
	// explain where it died.
	_ = c.writer.Flush()
}

// writeFailure is the last thing a dying child writes, so the parent can tell "the run failed" from
// "the child was killed before it could say anything".
func (c *childRun) writeFailure(cause error) {
	record := stageRecord{
		SchemaVersion: schemaVersion,
		Record:        recordStage,
		Status:        statusFailed,
		RunID:         c.runID,
		PID:           c.pid,
		Seq:           c.seq,
		Stage:         "error",
		StageIndex:    c.stageIndex,
		ObservedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		OffsetNS:      time.Since(earliestObservable).Nanoseconds(),
		Spec:          c.spec,
		Identity:      c.identity,
		Error:         cause.Error(),
	}
	c.writeRecord(record)
}

// readingDeltaOf builds one signed difference.
func readingDeltaOf(against string, before readings, after readings, nodes int) *readingDelta {
	delta := &readingDelta{
		Against:               against,
		LiveHeapBytes:         deltaOf(after.Go.LiveHeapBytes, before.Go.LiveHeapBytes),
		LiveObjects:           deltaOf(after.Go.LiveObjects, before.Go.LiveObjects),
		HeapStacksBytes:       deltaOf(after.Go.HeapStacksBytes, before.Go.HeapStacksBytes),
		HeapObjectsBytes:      deltaOf(after.Go.HeapObjectsBytes, before.Go.HeapObjectsBytes),
		TotalBytes:            deltaOf(after.Go.TotalBytes, before.Go.TotalBytes),
		MetadataBytes:         deltaOf(metadataTotal(after.Go.Metadata), metadataTotal(before.Go.Metadata)),
		ProfilingBucketsBytes: deltaOf(after.Go.ProfilingBucketsBytes, before.Go.ProfilingBucketsBytes),
		ScanTotalBytes:        deltaOf(after.Go.Scan.TotalBytes, before.Go.Scan.TotalBytes),
		CumulativeAllocBytes:  deltaOf(after.Go.CumulativeAllocBytes, before.Go.CumulativeAllocBytes),
		Goroutines:            deltaOf(after.Goroutines.Current, before.Goroutines.Current),
		Threads:               deltaOf(after.Process.Threads, before.Process.Threads),
		FileDescriptors:       deltaOf(after.Process.FileDescriptors, before.Process.FileDescriptors),
		Connections:           deltaInt(after.Objects.Connections, before.Objects.Connections),
		GCCyclesBetween:       deltaOf(after.Go.GC.CyclesTotal, before.Go.GC.CyclesTotal),
	}
	if nodes > 0 && delta.LiveHeapBytes != nil {
		perNode := *delta.LiveHeapBytes / int64(nodes)
		delta.LiveHeapPerNodeBytes = &perNode
	}
	return delta
}

func allActivityNone(activity []string) bool {
	for _, entry := range activity {
		if entry != "none" {
			return false
		}
	}
	return true
}
