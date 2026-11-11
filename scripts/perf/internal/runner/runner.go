package runner

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/certs"
	"github.com/sagernet/sing-box/scripts/perf/internal/host"
	"github.com/sagernet/sing-box/scripts/perf/internal/loadgen"
	"github.com/sagernet/sing-box/scripts/perf/internal/netmeter"
	"github.com/sagernet/sing-box/scripts/perf/internal/sampler"
	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
	"github.com/sagernet/sing-box/scripts/perf/internal/source"
	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

// Options configures one scenario run.
type Options struct {
	ScenarioDir string
	OutDir      string
	RunID       string
	Binaries    map[string]string
	Patterns    []string
	Streams     []int
	Duration    time.Duration
	Warmup      time.Duration
	Reps        int
	Verify      bool
	Quiet       bool
	Logf        func(format string, args ...any)
}

// Outcome is what a run produced.
type Outcome struct {
	Run      *schema.Run
	RunDir   string
	RunJSON  string
	Failures []schema.Failure
}

type session struct {
	scenario      *Scenario
	dir           string
	vars          templateVars
	server        *process
	client        *process
	targets       *targetSet
	targetProcs   []*process
	cover         *target.TLSCover
	serverNS      string
	serverStartup time.Duration
	clientStartup time.Duration
	firstRequest  time.Duration
	readyDelay    time.Duration
	serverAddr    string
	clientAddr    string
}

// targetSet is one target per traffic shape, each on its own port. A single
// scenario can then measure upload, download, ping-pong, duplex and UDP without
// being restarted between them.
type targetSet struct {
	byKind map[target.Kind]targetHandle
	addr   map[target.Kind]string
}

// targetHandle is a target that may live in this process or in another network
// namespace.
type targetHandle interface {
	Snapshot() (target.Stats, error)
	Reset() error
	Close() error
}

type localTarget struct{ server *target.Server }

func (l localTarget) Snapshot() (target.Stats, error) { return l.server.Snapshot(), nil }
func (l localTarget) Reset() error                    { l.server.Reset(); return nil }
func (l localTarget) Close() error                    { return l.server.Close() }

func (t *targetSet) close() {
	for _, handle := range t.byKind {
		_ = handle.Close()
	}
}

func (t *targetSet) reset() {
	for _, handle := range t.byKind {
		_ = handle.Reset()
	}
}

// kindsForScenario is the set a scenario can address. A scenario that names one
// target kind only starts that one, so a protocol that cannot carry UDP is not
// asked to.
func kindsForScenario(scenario *Scenario) []target.Kind {
	if scenario.Target.Kind != "" {
		return []target.Kind{target.Kind(scenario.Target.Kind)}
	}
	return []target.Kind{target.Sink, target.Source, target.Echo, target.Duplex, target.UDPEcho}
}

func kindForPattern(pattern loadgen.Pattern) target.Kind {
	switch pattern {
	case loadgen.Download:
		return target.Source
	case loadgen.PingPong, loadgen.Connect:
		return target.Echo
	case loadgen.Duplex:
		return target.Duplex
	case loadgen.UDP:
		return target.UDPEcho
	default:
		return target.Sink
	}
}

// Run executes the scenario and writes run.json plus summary.md.
func Run(opts Options) (*Outcome, error) {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	scenario, err := LoadScenario(opts.ScenarioDir)
	if err != nil {
		return nil, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("%s-%s", scenario.ID, time.Now().Format("20060102T150405"))
	}
	runDir := filepath.Join(opts.OutDir, runID)
	if err = os.MkdirAll(filepath.Join(runDir, "configs"), 0o755); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(runDir, "logs"), 0o755); err != nil {
		return nil, err
	}

	startedAt := time.Now()
	outcome := &Outcome{RunDir: runDir}
	run := &schema.Run{
		SchemaVersion: schema.Version,
		RunID:         runID,
		Kind:          schema.KindE2E,
		StartedAt:     startedAt,
		Source:        source.Collect(repoRoot(), opts.Binaries["lab"]),
		Host:          host.Info(),
		Network:       networkFromEnv(),
		Scenario: schema.Scenario{
			ID: scenario.ID, Protocol: scenario.Protocol, Mode: scenario.Mode,
			Transport: scenario.Transport, Security: scenario.Security,
			Extra: scenario.Extra,
		},
		Environment: environment(),
		Reps:        []schema.Rep{},
		Summary:     []schema.Summary{},
		Method:      methodText(scenario),
	}
	outcome.Run = run
	run.Host.SamplingIntervalMS = int(sampleInterval / time.Millisecond)

	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = scenario.Defaults.Patterns
	}
	if len(patterns) == 0 {
		patterns = []string{string(loadgen.Upload)}
	}
	streamsList := opts.Streams
	if len(streamsList) == 0 {
		streamsList = scenario.Defaults.Streams
	}
	if len(streamsList) == 0 {
		streamsList = []int{1}
	}
	duration := opts.Duration
	if duration <= 0 {
		duration = parseDuration(scenario.Defaults.Duration, 5*time.Second)
	}
	warmup := opts.Warmup
	if warmup <= 0 {
		warmup = parseDuration(scenario.Defaults.Warmup, time.Second)
	}
	reps := opts.Reps
	if reps <= 0 {
		reps = scenario.Defaults.Reps
	}
	if reps <= 0 {
		reps = 1
	}

	sess, err := prepare(scenario, runDir, opts, duration)
	if err != nil {
		run.Failures = append(run.Failures, schema.Failure{Stage: "prepare", Message: err.Error(), Class: "environment"})
		outcome.Failures = run.Failures
		writeRun(runDir, run)
		return outcome, err
	}
	defer sess.close()

	run.Scenario.ServerConfig = relative(runDir, filepath.Join(runDir, "configs", "server.json"))
	run.Scenario.ClientConfig = relative(runDir, filepath.Join(runDir, "configs", "client.json"))
	run.Artifacts = schema.Artifacts{
		RunJSON:      "run.json",
		SummaryMD:    "summary.md",
		ClientConfig: run.Scenario.ClientConfig,
		ServerConfig: run.Scenario.ServerConfig,
		ClientLog:    "logs/client.log",
		ServerLog:    "logs/server.log",
	}
	// Process startup is part of what a user pays, and for a native engine it is
	// a large part; it is recorded next to the throughput rather than in a
	// separate experiment.
	if run.Scenario.Extra == nil {
		run.Scenario.Extra = map[string]any{}
	}
	run.Scenario.Extra["startup_server_ms"] = float64(sess.serverStartup.Microseconds()) / 1000
	run.Scenario.Extra["startup_client_ms"] = float64(sess.clientStartup.Microseconds()) / 1000
	// startup_*_ms is when the configured readiness check passed. For a native
	// engine that starts lazily, the interesting number is first_request_ms:
	// the time until a real proxied request completed.
	run.Scenario.Extra["first_request_ms"] = float64(sess.firstRequest.Microseconds()) / 1000

	for _, pattern := range patterns {
		for _, streams := range streamsList {
			for rep := 0; rep < reps; rep++ {
				repStarted := time.Now()
				repRecord, failure := measureRep(sess, runID, pattern, streams, rep, duration, warmup, opts)
				if failure != nil {
					run.Failures = append(run.Failures, *failure)
					opts.Logf("FAIL %s/%s/%s rep %d: %s", scenario.ID, pattern, streams, rep, failure.Message)
					// A failed readiness or protocol setup will not fix itself on
					// the next repetition; stop instead of producing N copies of
					// the same failure.
					if failure.Stage != "measure" {
						run.Summary = summarise(run.Reps)
						run.FinishedAt = time.Now()
						outcome.Failures = run.Failures
						writeRun(runDir, run)
						writeSummaryMD(runDir, run)
						return outcome, fmt.Errorf("%s", failure.Message)
					}
					continue
				}
				run.Reps = append(run.Reps, *repRecord)
				opts.Logf("rep %d %s streams=%d: payload=%d MiB goodput=%.1f MiB/s (took %s)", rep, pattern, streams,
					repRecord.PayloadBytes/(1<<20), valueOrZero(repRecord.GoodputMiBPerS), time.Since(repStarted).Round(time.Millisecond))
			}
		}
	}

	run.Summary = summarise(run.Reps)
	run.FinishedAt = time.Now()
	opts.Logf("measurement loop finished after %s", time.Since(startedAt).Round(time.Millisecond))
	outcome.Failures = run.Failures
	if err = writeRun(runDir, run); err != nil {
		return outcome, err
	}
	if err = writeSummaryMD(runDir, run); err != nil {
		return outcome, err
	}
	outcome.RunJSON = filepath.Join(runDir, "run.json")
	return outcome, nil
}

const sampleInterval = 100 * time.Millisecond

func prepare(scenario *Scenario, runDir string, opts Options, duration time.Duration) (sess *session, err error) {
	sess = &session{
		scenario:   scenario,
		dir:        runDir,
		serverAddr: envOr("PERF_SERVER_ADDR", "127.0.0.1"),
		clientAddr: envOr("PERF_CLIENT_ADDR", "127.0.0.1"),
		serverNS:   os.Getenv("PERF_SERVER_NS"),
	}
	// A partially prepared session still owns whatever it already started. The
	// deferred close makes every early return below leak-free.
	defer func() {
		if err != nil && sess != nil {
			sess.close()
		}
	}()
	serverPort, err := reserveServerPort(sess, net.JoinHostPort(sess.serverAddr, "0"))
	if err != nil {
		return nil, err
	}
	clientPort, err := ReservePort(net.JoinHostPort(sess.clientAddr, "0"))
	if err != nil {
		return nil, err
	}

	serverName := "perf-lab.test"
	certFiles, err := certs.Generate(filepath.Join(runDir, "certs"), serverName, []net.IP{net.ParseIP(sess.serverAddr), net.ParseIP("127.0.0.1")})
	if err != nil {
		return nil, fmt.Errorf("generate certificates: %w", err)
	}
	// The target is addressed by an address the proxy can route to. Loopback is
	// not usable for tunnel protocols that carry IP packets (a userspace stack
	// treats 127.0.0.0/8 as itself), so the lab uses the run's site address,
	// which is the server side's own address in a namespace run and the host's
	// private IPv4 on a loopback run.
	siteAddress := siteAddr()

	vars := templateVars{
		"SERVER_ADDR": sess.serverAddr,
		"SERVER_PORT": strconv.Itoa(serverPort),
		"CLIENT_ADDR": sess.clientAddr,
		"CLIENT_PORT": strconv.Itoa(clientPort),
		"SITE_ADDR":   siteAddress,
		"TARGET_ADDR": "127.0.0.1",
		"TARGET_PORT": "",
		"CERT":        certFiles.Cert,
		"KEY":         certFiles.Key,
		"CA":          certFiles.CA,
		"SERVER_NAME": serverName,
		"UUID":        randomUUID(),
		"PASSWORD":    randomHex(16),
		"SHORT_ID":    randomHex(4),
		"RUN_DIR":     runDir,
	}
	for key, value := range scenario.Vars {
		vars[key] = value
	}

	// A REALITY cell forwards unauthenticated handshakes to a TLS endpoint. It
	// is started from this process so the cell needs no public site.
	if templateContains(opts.ScenarioDir, scenario, "__COVER_PORT__") {
		coverPort, portErr := ReservePort("127.0.0.1:0")
		if portErr != nil {
			return nil, portErr
		}
		cover, coverErr := target.StartTLSCover(net.JoinHostPort("127.0.0.1", strconv.Itoa(coverPort)), certFiles.Cert, certFiles.Key)
		if coverErr != nil {
			return nil, fmt.Errorf("start REALITY cover: %w", coverErr)
		}
		sess.cover = cover
		vars["COVER_PORT"] = strconv.Itoa(coverPort)
	}

	// REALITY and WireGuard need matching key pairs, and only the binary knows
	// how to make one. Generating them here keeps the pairs consistent with the
	// build under test instead of with a hard-coded fixture.
	labBinary := opts.Binaries["lab"]
	if labBinary != "" && templateContains(opts.ScenarioDir, scenario, "__REALITY_PRIVATE__") {
		privateKey, publicKey, keyErr := realityKeyPair(labBinary)
		if keyErr != nil {
			return nil, keyErr
		}
		vars["REALITY_PRIVATE"] = privateKey
		vars["REALITY_PUBLIC"] = publicKey
	}
	if labBinary != "" && templateContains(opts.ScenarioDir, scenario, "__WG_SERVER_PRIVATE__") {
		serverPrivate, serverPublic, keyErr := wgKeyPair(labBinary)
		if keyErr != nil {
			return nil, keyErr
		}
		clientPrivate, clientPublic, keyErr := wgKeyPair(labBinary)
		if keyErr != nil {
			return nil, keyErr
		}
		vars["WG_SERVER_PRIVATE"] = serverPrivate
		vars["WG_SERVER_PUBLIC"] = serverPublic
		vars["WG_CLIENT_PRIVATE"] = clientPrivate
		vars["WG_CLIENT_PUBLIC"] = clientPublic
	}

	sess.vars = vars
	sess.targets = &targetSet{byKind: map[target.Kind]targetHandle{}, addr: map[target.Kind]string{}}
	for _, kind := range kindsForScenario(scenario) {
		port, portErr := reserveServerPort(sess, net.JoinHostPort(siteAddress, "0"))
		if portErr != nil {
			return nil, portErr
		}
		address := net.JoinHostPort(siteAddress, strconv.Itoa(port))
		if sess.serverNS != "" {
			// The target must sit behind the server, in the server's namespace.
			// Started from the server side, "server to target" stays local and
			// only the tunnel crosses the shaped link.
			controlPort, controlErr := reserveServerPort(sess, net.JoinHostPort(siteAddress, "0"))
			if controlErr != nil {
				return nil, controlErr
			}
			controlAddr := net.JoinHostPort(siteAddress, strconv.Itoa(controlPort))
			self, selfErr := os.Executable()
			if selfErr != nil {
				return nil, selfErr
			}
			verifyFlag := "-verify=false"
			if opts.Verify {
				verifyFlag = "-verify=true"
			}
			targetProcess, startErr := startProcessPrefix("target-"+string(kind), nsPrefix(sess.serverNS), self,
				[]string{"target", "-kind", string(kind), "-addr", address, "-control", controlAddr,
					"-duration", duration.String(), verifyFlag},
				runDir, filepath.Join(runDir, "logs", "target-"+string(kind)+".log"))
			if startErr != nil {
				return nil, startErr
			}
			sess.targetProcs = append(sess.targetProcs, targetProcess)
			remote, dialErr := target.DialRemote(controlAddr, 30*time.Second)
			if dialErr != nil {
				return nil, fmt.Errorf("target %s in server namespace: %w", kind, dialErr)
			}
			sess.targets.byKind[kind] = remote
			sess.targets.addr[kind] = address
			continue
		}
		server, startErr := target.Start(kind, address, duration, opts.Verify)
		if startErr != nil {
			return nil, fmt.Errorf("start %s target: %w", kind, startErr)
		}
		sess.targets.byKind[kind] = localTarget{server: server}
		sess.targets.addr[kind] = address
	}
	if sinkAddr, ok := sess.targets.addr[target.Sink]; ok {
		host, port, _ := net.SplitHostPort(sinkAddr)
		vars["TARGET_ADDR"] = host
		vars["TARGET_PORT"] = port
	}

	if err = renderFile(filepath.Join(opts.ScenarioDir, scenario.Server.Config), filepath.Join(runDir, "configs", "server.json"), vars); err != nil {
		return nil, fmt.Errorf("render server config: %w", err)
	}
	if err = renderFile(filepath.Join(opts.ScenarioDir, scenario.Client.Config), filepath.Join(runDir, "configs", "client.json"), vars); err != nil {
		return nil, fmt.Errorf("render client config: %w", err)
	}

	serverBinary, err := resolvedBinary(binaryLabel(scenario.Server.Binary, "lab"), opts.Binaries)
	if err != nil {
		return nil, err
	}
	clientBinary, err := resolvedBinary(binaryLabel(scenario.Client.Binary, "lab"), opts.Binaries)
	if err != nil {
		return nil, err
	}
	serverStarted := time.Now()
	sess.server, err = startProcessPrefix("server", nsPrefix(sess.serverNS), serverBinary,
		append([]string{"run", "-c", filepath.Join(runDir, "configs", "server.json")}, scenario.Server.Args...),
		runDir, filepath.Join(runDir, "logs", "server.log"))
	if err != nil {
		return nil, err
	}
	opts.Logf("server started in %s", time.Since(serverStarted).Round(time.Millisecond))
	if err = waitReady(sess.server, scenario.Server.Ready, vars); err != nil {
		return nil, err
	}
	sess.serverStartup = time.Since(serverStarted)
	opts.Logf("server ready in %s", sess.serverStartup.Round(time.Millisecond))
	clientStarted := time.Now()
	sess.client, err = startProcess("client", clientBinary,
		append([]string{"run", "-c", filepath.Join(runDir, "configs", "client.json")}, scenario.Client.Args...),
		runDir, filepath.Join(runDir, "logs", "client.log"))
	if err != nil {
		return nil, err
	}
	if err = waitReady(sess.client, scenario.Client.Ready, vars); err != nil {
		return nil, err
	}
	sess.clientStartup = time.Since(clientStarted)
	opts.Logf("client ready in %s", sess.clientStartup.Round(time.Millisecond))
	deadline := time.Now().Add(45 * time.Second)
	var probeErr error
	probeKind := target.Sink
	if _, ok := sess.targets.addr[probeKind]; !ok {
		for kind := range sess.targets.addr {
			probeKind = kind
			break
		}
	}
	probeStarted := time.Now()
	for time.Now().Before(deadline) {
		probeErr = loadgen.Probe(net.JoinHostPort(sess.clientAddr, strconv.Itoa(clientPort)),
			sess.targets.addr[probeKind], probeKind)
		if probeErr == nil {
			sess.firstRequest = time.Since(probeStarted)
			opts.Logf("readiness probe succeeded in %s", sess.firstRequest.Round(time.Millisecond))
			return sess, nil
		}
		if !sess.client.alive() || !sess.server.alive() {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if probeErr == nil {
		probeErr = fmt.Errorf("readiness probe timed out")
	}
	return nil, fmt.Errorf("readiness probe failed: %w\nserver log:\n%s\nclient log:\n%s",
		probeErr, sess.server.tail(20), sess.client.tail(20))
}

func (s *session) close() {
	if s.client != nil {
		s.client.stop(2 * time.Second)
	}
	if s.server != nil {
		s.server.stop(2 * time.Second)
	}
	if s.targets != nil {
		s.targets.close()
	}
	for _, targetProcess := range s.targetProcs {
		targetProcess.stop(2 * time.Second)
	}
	if s.cover != nil {
		_ = s.cover.Close()
	}
}

func measureRep(sess *session, runID, pattern string, streams, rep int, duration, warmup time.Duration, opts Options) (*schema.Rep, *schema.Failure) {
	proxyAddr := net.JoinHostPort(sess.clientAddr, sess.vars["CLIENT_PORT"])
	kind := kindForPattern(loadgen.Pattern(pattern))
	targetAddr, ok := sess.targets.addr[kind]
	if !ok {
		return nil, &schema.Failure{
			Stage:   "measure",
			Class:   "scenario",
			Message: fmt.Sprintf("pattern %q needs a %s target, which this scenario does not start", pattern, kind),
		}
	}
	cfg := loadgen.Config{
		ProxyAddr:     proxyAddr,
		TargetAddr:    targetAddr,
		Pattern:       loadgen.Pattern(pattern),
		Streams:       streams,
		Duration:      duration,
		ChunkSize:     256 * 1024,
		PingPongBytes: 1024,
		PacketSize:    1200,
		Verify:        opts.Verify,
	}

	if warmup > 0 {
		warmCfg := cfg
		warmCfg.Duration = warmup
		if _, err := loadgen.Run(context.Background(), warmCfg); err != nil {
			opts.Logf("warmup for %s/%d reported: %v", pattern, streams, err)
		}
	}
	sess.targets.reset()

	wireBefore, wireBeforeOK := wireSnapshot()

	clientMeter := newCPUMeter(sess.client.pid())
	serverMeter := newCPUMeter(sess.server.pid())
	selfMeter := newCPUMeter(os.Getpid())
	clientSampler := sampler.Start(sess.client.pid(), sampleInterval)
	serverSampler := sampler.Start(sess.server.pid(), sampleInterval)

	result, runErr := loadgen.Run(context.Background(), cfg)

	clientSummary := clientSampler.Stop()
	serverSummary := serverSampler.Stop()
	wireAfter, wireAfterOK := wireSnapshot()

	stats, statsErr := sess.targets.byKind[kind].Snapshot()
	if statsErr != nil {
		stats = target.Stats{}
		opts.Logf("target stats unavailable: %v", statsErr)
	}

	// The payload is what arrived, not what was handed to the socket. For an
	// upload that is the target's byte count; for a download it is the client's
	// verified bytes. Using the sender's count would let a run that drops data
	// report the bytes it queued.
	payloadBytes := result.PayloadBytes
	switch loadgen.Pattern(pattern) {
	case loadgen.Upload:
		payloadBytes = stats.Bytes
	case loadgen.Duplex:
		payloadBytes = stats.Bytes + result.ReceivedBytes
	}
	wire := wireDelta(wireBefore, wireBeforeOK, wireAfter, wireAfterOK, result.Elapsed, payloadBytes)

	record := &schema.Rep{
		Index:           rep,
		Streams:         streams,
		Pattern:         pattern,
		PayloadBytes:    payloadBytes,
		DurationSeconds: result.Elapsed.Seconds(),
		Wire:            wire,
		Latency:         latencyFrom(result),
		Client:          procRecord(sess.client.pid(), clientMeter, clientSummary, payloadBytes),
		Server:          procRecord(sess.server.pid(), serverMeter, serverSummary, payloadBytes),
		Loadgen:         procRecord(os.Getpid(), selfMeter, sampler.Summary{}, payloadBytes),
		Integrity:       integrityFrom(result, stats, string(kind)),
		Extra: map[string]any{
			"sent_bytes":     result.SentBytes,
			"received_bytes": result.ReceivedBytes,
			"target_bytes":   stats.Bytes,
		},
	}
	if payloadBytes > 0 {
		goodput := float64(payloadBytes) / result.Elapsed.Seconds() / (1 << 20)
		record.GoodputMiBPerS = &goodput
	}
	if loadgen.Pattern(pattern) == loadgen.UDP {
		record.UDP = &schema.UDP{
			Sent:       result.DatagramsSent,
			Received:   result.DatagramsReceived,
			PacketSize: intPtr(1200),
		}
		if result.DatagramsSent > 0 {
			lost := result.DatagramsSent - result.DatagramsReceived
			record.UDP.Lost = &lost
			loss := float64(lost) / float64(result.DatagramsSent) * 100
			record.UDP.LossPercent = &loss
		}
		if result.Jitter > 0 {
			jitter := float64(result.Jitter) / float64(time.Millisecond)
			record.UDP.JitterMS = &jitter
		}
	}
	if runErr != nil {
		record.Warnings = append(record.Warnings, runErr.Error())
	}
	if loadgen.Pattern(pattern) == loadgen.UDP {
		if result.DatagramsReceived == 0 {
			return nil, &schema.Failure{
				Stage:   "measure",
				Class:   "no-payload",
				Message: fmt.Sprintf("no datagrams returned at %d stream(s): sent=%d", streams, result.DatagramsSent),
				LogPath: "logs/client.log",
			}
		}
	} else if payloadBytes == 0 {
		switch loadgen.Pattern(pattern) {
		case loadgen.Upload, loadgen.Download, loadgen.Duplex:
			return nil, &schema.Failure{
				Stage:   "measure",
				Class:   "no-payload",
				Message: fmt.Sprintf("no payload arrived for %s at %d stream(s): sent=%d target_received=%d client_received=%d", pattern, streams, result.SentBytes, stats.Bytes, result.ReceivedBytes),
				LogPath: "logs/client.log",
			}
		}
	}
	_ = runID

	if !sess.client.alive() {
		return record, &schema.Failure{Stage: "measure", Message: "client exited during measurement: " + sess.client.tail(10), LogPath: "logs/client.log", Class: "process-exit"}
	}
	if !sess.server.alive() {
		return record, &schema.Failure{Stage: "measure", Message: "server exited during measurement: " + sess.server.tail(10), LogPath: "logs/server.log", Class: "process-exit"}
	}
	return record, nil
}

func procRecord(pid int, meter *cpuMeter, summary sampler.Summary, payloadBytes int64) *schema.Proc {
	record := &schema.Proc{PID: pid}
	cpu, startRSS, endRSS := meter.finish()
	if cpu != nil {
		record.CPUSeconds = cpu
		record.CPUSource = "proc-boundary"
		// Below a megabyte of payload the ratio is quantisation noise: one
		// 10 ms tick over a few kilobytes reads as thousands of seconds per GiB
		// and looks like a protocol cost when it is a sampling artefact.
		if payloadBytes >= 1<<20 {
			perGiB := *cpu / (float64(payloadBytes) / (1 << 30))
			record.CPUSecondsPerGiB = &perGiB
		}
	}
	record.RSSStartBytes = startRSS
	record.RSSEndBytes = endRSS
	if summary.Samples > 0 {
		record.RSSPeakBytes = summary.RSSPeak
		record.ThreadsPeak = summary.ThreadsPeak
		record.FDsPeak = summary.FDsPeak
		record.Samples = summary.Samples
	}
	return record
}

func latencyFrom(result loadgen.Result) *schema.Latency {
	if len(result.Latencies) == 0 {
		return nil
	}
	millis := make([]float64, len(result.Latencies))
	for i, value := range result.Latencies {
		millis[i] = float64(value) / float64(time.Millisecond)
	}
	sort.Float64s(millis)
	latency := &schema.Latency{Count: len(millis)}
	low, high := millis[0], millis[len(millis)-1]
	latency.MinMS, latency.MaxMS = &low, &high
	latency.P50MS = percentile(millis, 50)
	if len(millis) >= 20 {
		latency.P95MS = percentile(millis, 95)
	}
	if len(millis) >= 100 {
		latency.P99MS = percentile(millis, 99)
	}
	var sum float64
	for _, value := range millis {
		sum += value
	}
	mean := sum / float64(len(millis))
	latency.MeanMS = &mean
	return latency
}

func integrityFrom(result loadgen.Result, stats target.Stats, kind string) *schema.Integrity {
	integrity := &schema.Integrity{}
	switch target.Kind(kind) {
	case target.Sink:
		integrity.VerifiedBytes = stats.Verified
		integrity.OK = stats.Mismatched == 0
		if stats.Mismatched > 0 {
			integrity.Detail = fmt.Sprintf("%d bytes did not match the pattern at the target", stats.Mismatched)
		} else {
			integrity.Detail = "target verified every block against the pattern page"
		}
	case target.Source:
		integrity.VerifiedBytes = result.VerifiedBytes
		integrity.OK = result.MismatchedBytes == 0
		if result.MismatchedBytes > 0 {
			integrity.Detail = fmt.Sprintf("%d bytes did not match the pattern at the client", result.MismatchedBytes)
		} else {
			integrity.Detail = "client verified every block against the pattern page"
		}
	default:
		integrity.OK = true
		integrity.Detail = "no payload integrity check for this target kind"
	}
	return integrity
}

func summarise(reps []schema.Rep) []schema.Summary {
	type key struct {
		pattern string
		streams int
	}
	goodput := make(map[key][]float64)
	clientCPU := make(map[key][]float64)
	serverCPU := make(map[key][]float64)
	wireGbit := make(map[key][]float64)
	overhead := make(map[key][]float64)
	latency50 := make(map[key][]float64)
	latency95 := make(map[key][]float64)
	latency99 := make(map[key][]float64)
	order := make([]key, 0)
	seen := make(map[key]bool)

	for _, rep := range reps {
		k := key{rep.Pattern, rep.Streams}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		if rep.GoodputMiBPerS != nil {
			goodput[k] = append(goodput[k], *rep.GoodputMiBPerS)
		}
		if rep.Client != nil && rep.Client.CPUSecondsPerGiB != nil {
			clientCPU[k] = append(clientCPU[k], *rep.Client.CPUSecondsPerGiB)
		}
		if rep.Server != nil && rep.Server.CPUSecondsPerGiB != nil {
			serverCPU[k] = append(serverCPU[k], *rep.Server.CPUSecondsPerGiB)
		}
		if rep.Wire != nil && rep.Wire.GbitPerSecond != nil {
			wireGbit[k] = append(wireGbit[k], *rep.Wire.GbitPerSecond)
		}
		if rep.Wire != nil && rep.Wire.OverheadRatio != nil {
			overhead[k] = append(overhead[k], *rep.Wire.OverheadRatio)
		}
		if rep.Latency != nil {
			if rep.Latency.P50MS != nil {
				latency50[k] = append(latency50[k], *rep.Latency.P50MS)
			}
			if rep.Latency.P95MS != nil {
				latency95[k] = append(latency95[k], *rep.Latency.P95MS)
			}
			if rep.Latency.P99MS != nil {
				latency99[k] = append(latency99[k], *rep.Latency.P99MS)
			}
		}
	}

	var rows []schema.Summary
	for _, k := range order {
		rows = append(rows,
			statRow("goodput", "MiB/s", k.streams, k.pattern, goodput[k]),
			statRow("cpu_seconds_per_gib_client", "s/GiB", k.streams, k.pattern, clientCPU[k]),
			statRow("cpu_seconds_per_gib_server", "s/GiB", k.streams, k.pattern, serverCPU[k]),
			statRow("wire_gbit_per_s", "Gbit/s", k.streams, k.pattern, wireGbit[k]),
			statRow("wire_overhead_ratio", "ratio", k.streams, k.pattern, overhead[k]),
			statRow("latency_p50", "ms", k.streams, k.pattern, latency50[k]),
			statRow("latency_p95", "ms", k.streams, k.pattern, latency95[k]),
			statRow("latency_p99", "ms", k.streams, k.pattern, latency99[k]),
		)
	}
	filtered := rows[:0]
	for _, row := range rows {
		if row.N > 0 {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func waitReady(p *process, ready Ready, vars templateVars) error {
	if ready.TCP != "" {
		address := vars.render(ready.TCP)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
			if !p.alive() {
				return fmt.Errorf("%s exited before %s became reachable: %s", p.name, address, p.tail(15))
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("%s did not listen on %s within 30s: %s", p.name, address, p.tail(15))
	}
	if ready.SleepMS > 0 {
		time.Sleep(time.Duration(ready.SleepMS) * time.Millisecond)
	}
	if !p.alive() {
		return fmt.Errorf("%s exited during startup: %s", p.name, p.tail(15))
	}
	return nil
}

func realityKeyPair(binary string) (privateKey, publicKey string, err error) {
	output, err := exec.Command(binary, "generate", "reality-keypair").Output()
	if err != nil {
		return "", "", fmt.Errorf("generate reality keypair: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "PrivateKey:"); ok {
			privateKey = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "PublicKey:"); ok {
			publicKey = strings.TrimSpace(value)
		}
	}
	if privateKey == "" || publicKey == "" {
		return "", "", fmt.Errorf("reality keypair output was not understood: %q", string(output))
	}
	return privateKey, publicKey, nil
}

// templateContains reports whether either config template in the scenario
// directory mentions marker. It decides which generated key material a scenario
// needs, so a template that does not use REALITY never pays for a keypair.
func templateContains(dir string, scenario *Scenario, marker string) bool {
	for _, name := range []string{scenario.Server.Config, scenario.Client.Config} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && strings.Contains(string(raw), marker) {
			return true
		}
	}
	return false
}

func wgKeyPair(binary string) (privateKey, publicKey string, err error) {
	output, err := exec.Command(binary, "generate", "wg-keypair").Output()
	if err != nil {
		return "", "", fmt.Errorf("generate wg keypair: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "PrivateKey:"); ok {
			privateKey = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "PublicKey:"); ok {
			publicKey = strings.TrimSpace(value)
		}
	}
	if privateKey == "" || publicKey == "" {
		return "", "", fmt.Errorf("wg keypair output was not understood: %q", string(output))
	}
	return privateKey, publicKey, nil
}

func wireSnapshot() (netmeter.Counters, bool) {
	iface := os.Getenv("PERF_WIRE_IFACE")
	if iface == "" {
		return netmeter.Counters{}, false
	}
	counters, err := netmeter.Read(iface)
	if err != nil {
		return netmeter.Counters{}, false
	}
	return counters, true
}

func wireDelta(before netmeter.Counters, beforeOK bool, after netmeter.Counters, afterOK bool, elapsed time.Duration, payloadBytes int64) *schema.Wire {
	if !beforeOK || !afterOK {
		return nil
	}
	delta, ok := netmeter.Delta(before, after)
	if !ok {
		return nil
	}
	wire := &schema.Wire{
		TxBytes:   int64Ptr(delta.TxBytes),
		RxBytes:   int64Ptr(delta.RxBytes),
		TxPackets: int64Ptr(delta.TxPackets),
		RxPackets: int64Ptr(delta.RxPackets),
		Source:    "veth-statistics",
	}
	if elapsed > 0 {
		gbit := float64(delta.TxBytes+delta.RxBytes) * 8 / elapsed.Seconds() / 1e9
		pps := float64(delta.TxPackets+delta.RxPackets) / elapsed.Seconds()
		wire.GbitPerSecond = &gbit
		wire.PPS = &pps
	}
	if payloadBytes > 0 {
		ratio := float64(delta.TxBytes+delta.RxBytes) / float64(payloadBytes)
		wire.OverheadRatio = &ratio
	}
	return wire
}

func networkFromEnv() schema.Network {
	network := schema.Network{
		Topology:  envOr("PERF_NET_TOPOLOGY", "loopback"),
		Profile:   os.Getenv("PERF_NET_PROFILE"),
		Namespace: os.Getenv("PERF_NET_NAMESPACE"),
		Note:      os.Getenv("PERF_NET_NOTE"),
	}
	network.DelayMS = envFloat("PERF_NET_DELAY_MS")
	network.JitterMS = envFloat("PERF_NET_JITTER_MS")
	network.LossPercent = envFloat("PERF_NET_LOSS_PCT")
	network.BandwidthMbps = envFloat("PERF_NET_BANDWIDTH_MBPS")
	if mtu := os.Getenv("PERF_NET_MTU"); mtu != "" {
		if value, err := strconv.Atoi(mtu); err == nil {
			network.MTU = &value
		}
	}
	if spec := os.Getenv("PERF_NET_QDISC"); spec != "" {
		for _, entry := range strings.Split(spec, ";") {
			fields := strings.Split(entry, "|")
			if len(fields) < 3 {
				continue
			}
			qdisc := schema.Qdisc{Device: fields[0], Kind: fields[1], Parent: fields[2]}
			if len(fields) > 3 {
				qdisc.Args = fields[3]
			}
			network.Qdiscs = append(network.Qdiscs, qdisc)
		}
	}
	ifaces := netmeter.Interfaces()
	for _, info := range ifaces {
		network.Interfaces = append(network.Interfaces, schema.Interface{Name: info.Name, MTU: info.MTU, Addresses: info.Addresses})
	}
	return network
}

func environment() []schema.EnvVar {
	var vars []schema.EnvVar
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(name, "PERF_") {
			continue
		}
		vars = append(vars, schema.EnvVar{Name: name, Value: redact(value)})
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
	return vars
}

func redact(value string) string {
	lower := strings.ToLower(value)
	for _, needle := range []string{"token", "password", "secret", "authorization"} {
		if strings.Contains(lower, needle) {
			return "<redacted>"
		}
	}
	return value
}

func methodText(scenario *Scenario) string {
	return "e2e process pair: sing-box server + sing-box client on real sockets; payload verified against a pattern page; " +
		"CPU from /proc boundary reads and wait4 rusage; RSS/threads/FDs sampled every 100ms; wire bytes from veth statistics when the run crosses an emulated link"
}

func repoRoot() string {
	if root := os.Getenv("PERF_REPO"); root != "" {
		return root
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

func relative(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

func binaryLabel(label, fallback string) string {
	if label == "" {
		return fallback
	}
	return label
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseDuration(text string, fallback time.Duration) time.Duration {
	if text == "" {
		return fallback
	}
	value, err := time.ParseDuration(text)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func nsPrefix(pid string) []string {
	if pid == "" {
		return nil
	}
	return []string{"nsenter", "-t", pid, "-n", "--"}
}

// reserveServerPort reserves a port on the server side. Ports live in their
// network namespace's own space, so a port free in the client namespace says
// nothing about the server namespace: the reservation has to run there.
func reserveServerPort(sess *session, addr string) (int, error) {
	if sess.serverNS == "" {
		return ReservePort(addr)
	}
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	command := append(nsPrefix(sess.serverNS), self, "reserve", "-addr", addr, "-count", "1")
	output, err := exec.Command(command[0], command[1:]...).Output()
	if err != nil {
		return 0, fmt.Errorf("reserve %s in server namespace: %w", addr, err)
	}
	port, convErr := strconv.Atoi(strings.TrimSpace(string(output)))
	if convErr != nil {
		return 0, fmt.Errorf("reserve %s in server namespace returned %q", addr, string(output))
	}
	return port, nil
}

// siteAddr is the address the target binds and the proxy dials. It is the
// server side's own address in a namespace run, and the host's private IPv4 on
// a loopback run.
func siteAddr() string {
	if value := os.Getenv("PERF_SITE_ADDR"); value != "" {
		return value
	}
	if server := os.Getenv("PERF_SERVER_ADDR"); server != "" && server != "127.0.0.1" && server != "::1" {
		return server
	}
	addresses, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok || network.IP.IsLoopback() {
				continue
			}
			if ip4 := network.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func envFloat(name string) *float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

func intPtr(value int) *int       { return &value }
func int64Ptr(value int64) *int64 { return &value }

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
