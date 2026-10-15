package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The runner: it builds the matrix, starts one reference server per
// environment, starts both clients against it, and measures them alternately.
//
// Three properties make the comparison usable:
//
//  1. ONE reference server per environment, shared by both clients. A server
//     restart between the two sides would move the server-side cost too.
//  2. Alternating order. Repetition 1 measures our client first, repetition 2
//     measures Xray first, so a thermal drift or a background process that
//     arrives mid-cell cannot be mistaken for a client difference.
//  3. Medians across repetitions, with the spread kept in the raw results. A
//     single run of a network measurement on a laptop is a sample, not a fact.
//
// The runner never edits the transport. It reports what it measured, and the
// optimization work is judged by re-running exactly this.

const (
	affinityServer  = 0x000F // logical CPUs 0-3
	affinityClient  = 0x00F0 // logical CPUs 4-7
	affinityLoadGen = 0x0F00 // logical CPUs 8-11
	affinityHelper  = 0x3000 // logical CPUs 12-13
)

type benchLoad struct {
	Pattern     string `json:"pattern"`
	Concurrency int    `json:"concurrency"`
	DurationMs  int    `json:"duration_ms,omitempty"`
	Iterations  int    `json:"iterations,omitempty"`
	Payload     int    `json:"payload,omitempty"`
	Quantum     int    `json:"quantum,omitempty"`
}

func (load benchLoad) name() string {
	switch load.Pattern {
	case "pingpong":
		return fmt.Sprintf("%s-c%d-%dB", load.Pattern, load.Concurrency, load.Payload)
	case "connect":
		return fmt.Sprintf("%s-c%d", load.Pattern, load.Concurrency)
	default:
		return fmt.Sprintf("%s-c%d", load.Pattern, load.Concurrency)
	}
}

type benchRunResult struct {
	Env    string         `json:"env"`
	Side   string         `json:"side"`
	Load   string         `json:"load"`
	Rep    int            `json:"rep"`
	Order  int            `json:"order"`
	Report *loadGenReport `json:"report,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func runBench(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	xrayBinary := flags.String("xray", "", "path to the reference xray binary")
	singBoxBinary := flags.String("sing-box", "", "path to this fork's sing-box binary")
	workDir := flags.String("work", "", "directory for generated configs and logs (required)")
	outDir := flags.String("out", "", "directory for results (required)")
	reps := flags.Int("reps", 3, "repetitions per load, measured alternately")
	modes := flags.String("modes", "packet-up", "comma-separated XHTTP modes")
	securities := flags.String("security", "tls", "comma-separated: tls,reality")
	versions := flags.String("versions", "h1,h2", "comma-separated HTTP versions for TLS cells: h1,h2")
	h3 := flags.Bool("h3", false, "also measure HTTP/3 cells (TLS only)")
	impairments := flags.String("impair", "", "comma-separated impairments: rtt50,rtt200,lossy (empty = clean path)")
	xmuxes := flags.String("xmux", "default", "comma-separated XMUX variants: default,single,rotate")
	patterns := flags.String("patterns", "download,upload,pingpong,connect", "comma-separated load patterns")
	concurrencies := flags.String("concurrency", "1", "comma-separated concurrency levels")
	durationMs := flags.Int("duration-ms", 4000, "measured window for throughput patterns")
	quantum := flags.Int("quantum", 4<<20, "bytes per request in duration mode")
	payload := flags.Int("pingpong-payload", 512, "bytes per request for the ping-pong pattern")
	pingpongIterations := flags.Int("pingpong-iterations", 2000, "iterations per connection for ping-pong")
	connectIterations := flags.Int("connect-iterations", 300, "iterations per connection for the connect pattern")
	warmupMs := flags.Int("warmup-ms", 800, "discarded warmup before each measured phase")
	loadTimeoutMs := flags.Int("load-timeout-ms", 90000, "per-operation timeout inside the load generator")
	readinessTimeout := flags.Int("readiness-ms", 45000, "how long a client is given to become usable")
	keepGoing := flags.Bool("keep-going", true, "continue after a failed cell")
	verbose := flags.Bool("verbose", false, "raise both sides' log level to debug (for diagnosing a failing cell)")
	only := flags.String("only", "", "substring filter on the environment name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	benchVerbose = *verbose
	if *xrayBinary == "" || *singBoxBinary == "" || *workDir == "" || *outDir == "" {
		return fmt.Errorf("run: -xray, -sing-box, -work and -out are all required")
	}
	if _, err := os.Stat(*xrayBinary); err != nil {
		return fmt.Errorf("run: reference binary: %w", err)
	}
	if _, err := os.Stat(*singBoxBinary); err != nil {
		return fmt.Errorf("run: sing-box binary: %w", err)
	}
	sessionDir := filepath.Join(*outDir, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return err
	}

	environments := buildEnvironments(*modes, *securities, *versions, *h3, *impairments, *xmuxes)
	if *only != "" {
		filtered := environments[:0]
		for _, env := range environments {
			if strings.Contains(env.name(), *only) {
				filtered = append(filtered, env)
			}
		}
		environments = filtered
	}
	loads := buildLoads(*patterns, *concurrencies, *durationMs, *quantum, *payload, *pingpongIterations, *connectIterations)
	if len(environments) == 0 || len(loads) == 0 {
		return fmt.Errorf("run: nothing to measure (environments=%d loads=%d)", len(environments), len(loads))
	}

	targetPort, err := freePort()
	if err != nil {
		return err
	}
	targetAddress := fmt.Sprintf("127.0.0.1:%d", targetPort)
	target, err := startSelf("target", filepath.Join(sessionDir, "target.log"), affinityHelper, "target", "-listen", targetAddress)
	if err != nil {
		return err
	}
	defer target.Stop(2 * time.Second)
	if err = waitForTCP(targetPort, 5*time.Second); err != nil {
		return fmt.Errorf("run: target did not come up: %w", err)
	}

	fmt.Printf("session %s\n  reference %s\n  client    %s\n  envs %d, loads %d, reps %d\n\n",
		filepath.Base(sessionDir), *xrayBinary, *singBoxBinary, len(environments), len(loads), *reps)

	results := make([]benchRunResult, 0, len(environments)*len(loads)*(*reps)*2)
	for _, env := range environments {
		fmt.Printf("== %s\n", env.name())
		cellResults, cellErr := runEnvironment(env, loads, *reps, *xrayBinary, *singBoxBinary,
			*workDir, sessionDir, targetAddress, targetPort, *warmupMs, *loadTimeoutMs, *readinessTimeout)
		results = append(results, cellResults...)
		if cellErr != nil {
			fmt.Printf("   FAILED: %v\n", cellErr)
			if !*keepGoing {
				break
			}
		}
	}

	if err = writeJSONFile(filepath.Join(sessionDir, "runs.json"), results); err != nil {
		return err
	}
	summary := summarizeResults(results)
	markdown := renderSummaryMarkdown(sessionDir, environments, loads, results, summary)
	if err = os.WriteFile(filepath.Join(sessionDir, "summary.md"), []byte(markdown), 0o644); err != nil {
		return err
	}
	if err = writeJSONFile(filepath.Join(sessionDir, "summary.json"), summary); err != nil {
		return err
	}
	fmt.Printf("\n%s\nresults: %s\n", markdown, sessionDir)
	return nil
}

func buildEnvironments(modes string, securities string, versions string, includeH3 bool, impairments string, xmuxes string) []benchEnv {
	clean := make([]benchEnv, 0, 64)
	for _, security := range splitList(securities) {
		versionList := splitList(versions)
		if security == "reality" {
			// REALITY rides HTTP/2 over TCP and ignores ALPN entirely; a cell
			// labelled otherwise would be a mislabelled HTTP/2 run.
			versionList = []string{"h2"}
		} else if includeH3 && !containsString(versionList, "h3") {
			versionList = append(versionList, "h3")
		}
		for _, version := range versionList {
			for _, mode := range splitList(modes) {
				for _, xmux := range splitList(xmuxes) {
					clean = append(clean, benchEnv{Mode: mode, Security: security, Version: version, Xmux: xmux})
				}
			}
		}
	}
	environments := clean
	// Impairments apply to TCP cells only: the shim forwards TCP, and a QUIC
	// path would need a UDP impairment that this host cannot produce honestly.
	for _, impairment := range splitList(impairments) {
		for _, env := range clean {
			if env.h3() {
				continue
			}
			impaired := env
			impaired.Impairment = impairment
			environments = append(environments, impaired)
		}
	}
	return environments
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func buildLoads(patterns string, concurrencies string, durationMs int, quantum int, payload int, pingpongIterations int, connectIterations int) []benchLoad {
	loads := make([]benchLoad, 0, 16)
	for _, concurrency := range splitList(concurrencies) {
		count := 0
		if _, err := fmt.Sscanf(concurrency, "%d", &count); err != nil || count < 1 {
			continue
		}
		for _, pattern := range splitList(patterns) {
			load := benchLoad{Pattern: pattern, Concurrency: count, Quantum: quantum}
			switch pattern {
			case "download", "upload", "duplex":
				load.DurationMs = durationMs
			case "pingpong":
				load.Payload = payload
				load.Iterations = pingpongIterations
			case "connect":
				load.Iterations = connectIterations
			}
			loads = append(loads, load)
		}
	}
	return loads
}

// runEnvironment starts one reference server (and, when impaired, one shim),
// then measures every load with both clients against it.
func runEnvironment(env benchEnv, loads []benchLoad, reps int, xrayBinary string, singBoxBinary string,
	workDir string, sessionDir string, targetAddress string, targetPort int,
	warmupMs int, loadTimeoutMs int, readinessTimeoutMs int) ([]benchRunResult, error) {

	results := make([]benchRunResult, 0, len(loads)*reps*2)
	ports, err := allocatePorts()
	if err != nil {
		return results, err
	}
	envDir := filepath.Join(sessionDir, "cells", env.name())
	if err = os.MkdirAll(envDir, 0o755); err != nil {
		return results, err
	}
	paths := benchPaths{
		Dir:       envDir,
		ServerLog: filepath.Join(envDir, "xray-server.log"),
		OursLog:   filepath.Join(envDir, "ours-client.log"),
		XrayLog:   filepath.Join(envDir, "xray-client.log"),
		ShimLog:   filepath.Join(envDir, "shim.log"),
		CoverLog:  filepath.Join(envDir, "cover.log"),
	}
	if env.Security == "tls" || env.Security == "reality" {
		certificates, certErr := ensureCertificates(envDir)
		if certErr != nil {
			return results, certErr
		}
		paths.CertFile = certificates.ServerCert
		paths.KeyFile = certificates.ServerKey
		paths.CAFile = certificates.CAFile
	}

	keys := benchRealityKeys{}
	if env.Security == "reality" {
		if keys, err = generateRealityKeys(); err != nil {
			return results, err
		}
		cover, coverErr := startSelf("cover", paths.CoverLog, affinityHelper, "tlscover",
			"-listen", fmt.Sprintf("127.0.0.1:%d", ports.Cover), "-cert", paths.CertFile, "-key", paths.KeyFile)
		if coverErr != nil {
			return results, coverErr
		}
		defer cover.Stop(time.Second)
	}

	serverConfigPath := filepath.Join(envDir, "xray-server.json")
	if err = writeJSONConfig(serverConfigPath, xrayServerConfig(env, ports, paths, keys, targetAddress)); err != nil {
		return results, err
	}
	server, err := startBenchProcess("xray-server", xrayBinary, []string{"run", "-c", serverConfigPath}, paths.ServerLog, affinityServer)
	if err != nil {
		return results, err
	}
	defer server.Stop(3 * time.Second)
	if !env.h3() {
		if err = waitForTCP(ports.Server, 20*time.Second); err != nil {
			return results, fmt.Errorf("reference server never listened: %w; log: %s", err, tailLog(paths.ServerLog, 8))
		}
	} else {
		// A QUIC listener binds UDP, which a TCP connect cannot observe; the
		// readiness probe through the client is the real test.
		time.Sleep(1500 * time.Millisecond)
	}

	clientServerPort := ports.Server
	if env.Impairment != "" {
		if env.h3() {
			return results, fmt.Errorf("impairment %s is not available for H3: the shim forwards TCP only", env.Impairment)
		}
		shimArgs := []string{"shim", "-listen", fmt.Sprintf("127.0.0.1:%d", ports.Shim), "-forward", fmt.Sprintf("127.0.0.1:%d", ports.Server)}
		shimArgs = append(shimArgs, impairmentArguments(env.Impairment)...)
		shim, shimErr := startSelf("shim", paths.ShimLog, affinityHelper, shimArgs...)
		if shimErr != nil {
			return results, shimErr
		}
		defer shim.Stop(time.Second)
		if err = waitForTCP(ports.Shim, 5*time.Second); err != nil {
			return results, err
		}
		clientServerPort = ports.Shim
	}

	type sideState struct {
		name    string
		proxy   int
		process *benchProcess
	}
	sides := make([]sideState, 0, 2)
	for _, side := range []string{"ours", "xray"} {
		proxyPort := ports.Ours
		logPath := paths.OursLog
		binary := singBoxBinary
		configPath := filepath.Join(envDir, "ours-client.json")
		var config map[string]any
		if side == "ours" {
			config = oursClientConfig(env, proxyPort, clientServerPort, env.xmuxBlock(), keys, paths.CAFile)
		} else {
			proxyPort = ports.Xray
			logPath = paths.XrayLog
			binary = xrayBinary
			configPath = filepath.Join(envDir, "xray-client.json")
			config = xrayClientConfig(env, proxyPort, clientServerPort, env.xmuxBlockXray(), keys, paths.CAFile)
		}
		if err = writeJSONConfig(configPath, config); err != nil {
			return results, err
		}
		process, startErr := startBenchProcess(side+"-client", binary, []string{"run", "-c", configPath}, logPath, affinityClient)
		if startErr != nil {
			return results, startErr
		}
		defer process.Stop(2 * time.Second)
		if err = waitForTunnel(proxyPort, targetAddress, time.Duration(readinessTimeoutMs)*time.Millisecond); err != nil {
			return results, fmt.Errorf("%s client never became usable: %w; log: %s", side, err, tailLog(logPath, 8))
		}
		sides = append(sides, sideState{name: side, proxy: proxyPort, process: process})
	}

	for rep := 1; rep <= reps; rep++ {
		// Alternate which client is measured first: a drift that arrives
		// mid-cell then lands on both sides instead of on one.
		order := make([]sideState, len(sides))
		copy(order, sides)
		if rep%2 == 0 {
			for left, right := 0, len(order)-1; left < right; left, right = left+1, right-1 {
				order[left], order[right] = order[right], order[left]
			}
		}
		for _, load := range loads {
			for position, side := range order {
				label := fmt.Sprintf("%s|%s|%s|rep%d", env.name(), side.name, load.name(), rep)
				reportPath := filepath.Join(envDir, fmt.Sprintf("%s-rep%d.json", side.name, rep))
				report, runErr := runOneLoad(label, load, side.proxy, targetAddress,
					side.process.PID, server.PID, warmupMs, loadTimeoutMs, reportPath, position)
				result := benchRunResult{Env: env.name(), Side: side.name, Load: load.name(), Rep: rep, Order: position, Report: report}
				if runErr != nil {
					result.Error = runErr.Error()
					fmt.Printf("   %-28s %-16s ERROR %v\n", load.name(), side.name, runErr)
					if logTail := tailLog(side.process.Log, 6); logTail != "" {
						result.Error += " | client log: " + strings.ReplaceAll(strings.TrimSpace(logTail), "\n", " / ")
					}
				} else {
					fmt.Printf("   %-28s %-16s %s\n", load.name(), side.name, formatReportLine(report))
				}
				results = append(results, result)
			}
		}
	}
	return results, nil
}

func impairmentArguments(impairment string) []string {
	switch impairment {
	case "rtt50":
		return []string{"-delay-ms", "25"}
	case "rtt200":
		return []string{"-delay-ms", "100"}
	case "lossy":
		return []string{"-delay-ms", "25", "-jitter-ms", "10", "-stall-prob", "0.02", "-stall-ms", "40",
			"-reset-prob", "0.05", "-reset-after-ms", "2000"}
	default:
		return nil
	}
}

func formatReportLine(report *loadGenReport) string {
	if report == nil {
		return "(no report)"
	}
	parts := []string{fmt.Sprintf("%8.2f MB/s", report.MBytesPerSec)}
	if report.OpsPerSec > 0 {
		parts = append(parts, fmt.Sprintf("%8.1f ops/s", report.OpsPerSec))
	}
	if report.Latency != nil {
		parts = append(parts, fmt.Sprintf("p50 %7.1fus p99 %8.1fus", report.Latency.P50, report.Latency.P99))
	}
	parts = append(parts, fmt.Sprintf("cpu %6.3fs", report.CPUSeconds))
	if report.Mismatches != 0 {
		parts = append(parts, fmt.Sprintf("MISMATCH %d", report.Mismatches))
	}
	return strings.Join(parts, "  ")
}

// runOneLoad invokes the load generator as a child process, pinned to its own
// cores, and charges CPU to the client and the server around the measured phase.
func runOneLoad(label string, load benchLoad, proxyPort int, targetAddress string,
	clientPID int, serverPID int, warmupMs int, loadTimeoutMs int, reportPath string, order int) (*loadGenReport, error) {

	arguments := []string{
		"loadgen",
		"-pattern", load.Pattern,
		"-target", targetAddress,
		"-proxy", fmt.Sprintf("127.0.0.1:%d", proxyPort),
		"-concurrency", fmt.Sprintf("%d", load.Concurrency),
		"-warmup-ms", fmt.Sprintf("%d", warmupMs),
		"-timeout-ms", fmt.Sprintf("%d", loadTimeoutMs),
		"-label", label,
		"-report", reportPath,
		"-cpu-pid", fmt.Sprintf("%d", clientPID),
		"-cpu-pid", fmt.Sprintf("%d", serverPID),
	}
	if load.DurationMs > 0 {
		arguments = append(arguments, "-duration-ms", fmt.Sprintf("%d", load.DurationMs),
			"-quantum", fmt.Sprintf("%d", load.Quantum))
	}
	if load.Payload > 0 {
		arguments = append(arguments, "-payload", fmt.Sprintf("%d", load.Payload))
	}
	if load.Iterations > 0 {
		arguments = append(arguments, "-iterations", fmt.Sprintf("%d", load.Iterations))
	}

	// The wall-clock bound is generous enough that a slow transport is measured
	// rather than killed, but not unbounded: a wedged tunnel must fail the cell.
	budget := time.Duration(loadTimeoutMs)*time.Millisecond + time.Duration(warmupMs)*time.Millisecond + 60*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	output, err := runSelfTimed(ctx, affinityLoadGen, arguments...)
	if err != nil {
		return nil, fmt.Errorf("loadgen: %w", err)
	}
	report, parseErr := parseLoadGenReport(output)
	if parseErr != nil {
		return nil, fmt.Errorf("%w (output: %s)", parseErr, strings.TrimSpace(output))
	}
	if len(report.Errors) > 0 {
		return &report, fmt.Errorf("load reported errors: %s", strings.Join(report.Errors, "; "))
	}
	if report.Mismatches != 0 {
		return &report, fmt.Errorf("payload mismatch: %d bytes", report.Mismatches)
	}
	return &report, nil
}

// parseLoadGenReport reads the JSON object the load generator prints last.
//
// It scans lines from the end and tries each one that could start an object,
// rather than taking the last "{" in the output: that character is just as
// likely to belong to a nested object inside the report as to its start.
func parseLoadGenReport(output string) (loadGenReport, error) {
	report := loadGenReport{}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &report); err == nil {
			return report, nil
		}
	}
	return report, fmt.Errorf("no JSON report on stdout")
}

// runSelfTimed executes this binary with a wall-clock bound and a core set.
func runSelfTimed(ctx context.Context, affinity uintptr, args ...string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.SysProcAttr = backgroundProcessAttributes()
	command.Stderr = os.Stderr
	var output strings.Builder
	command.Stdout = &output
	if err = command.Start(); err != nil {
		return "", err
	}
	if affinity != 0 {
		_ = configureProcess(command.Process.Pid, affinity, priorityHigh)
	}
	if err = command.Wait(); err != nil {
		return output.String(), err
	}
	return output.String(), nil
}

func waitForTCP(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("nothing accepted a connection on 127.0.0.1:%d within %s", port, timeout)
}

// waitForTunnel proves the path end to end: a SOCKS connect through the client,
// a request to the target and its answer. A listening SOCKS port is not
// readiness — the tunnel behind it may still be failing.
func waitForTunnel(proxyPort int, targetAddress string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, err := runSelfTimed(ctx, 0, "loadgen", "-pattern", "connect",
			"-target", targetAddress,
			"-proxy", fmt.Sprintf("127.0.0.1:%d", proxyPort),
			"-iterations", "1", "-warmup-ms", "0", "-timeout-ms", "5000")
		cancel()
		if err == nil {
			if _, parseErr := parseLoadGenReport(output); parseErr == nil {
				return nil
			}
		}
		lastError = strings.TrimSpace(output)
		if err != nil {
			lastError = err.Error()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("tunnel never carried a request: %s", lastError)
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

type benchSummary struct {
	Session string             `json:"session"`
	Cells   []benchSummaryCell `json:"cells"`
	Notes   []string           `json:"notes"`
}

type benchSummaryCell struct {
	Env      string                    `json:"env"`
	Load     string                    `json:"load"`
	Sides    map[string]benchSideStats `json:"sides"`
	Verdicts map[string]string         `json:"verdicts"`
}

type benchSideStats struct {
	Runs             int     `json:"runs"`
	Errors           int     `json:"errors"`
	MBytesPerSec     float64 `json:"mbytes_per_sec_median"`
	OpsPerSec        float64 `json:"ops_per_sec_median"`
	LatencyP50Us     float64 `json:"latency_p50_us_median"`
	LatencyP99Us     float64 `json:"latency_p99_us_median"`
	CPUSeconds       float64 `json:"cpu_sec_median"`
	MicroSecPerMByte float64 `json:"us_per_mbyte_median"`
	MicroSecPerOp    float64 `json:"us_per_op_median"`
	Mismatches       int64   `json:"mismatches"`
}

func summarizeResults(results []benchRunResult) benchSummary {
	summary := benchSummary{Session: time.Now().Format(time.RFC3339)}
	type key struct{ env, load string }
	grouped := make(map[key]map[string][]benchRunResult)
	order := make([]key, 0, 32)
	for _, result := range results {
		cellKey := key{result.Env, result.Load}
		if _, ok := grouped[cellKey]; !ok {
			grouped[cellKey] = make(map[string][]benchRunResult)
			order = append(order, cellKey)
		}
		grouped[cellKey][result.Side] = append(grouped[cellKey][result.Side], result)
	}
	for _, cellKey := range order {
		cell := benchSummaryCell{
			Env:      cellKey.env,
			Load:     cellKey.load,
			Sides:    make(map[string]benchSideStats),
			Verdicts: make(map[string]string),
		}
		for side, runs := range grouped[cellKey] {
			cell.Sides[side] = summarizeSide(runs)
		}
		ours, hasOurs := cell.Sides["ours"]
		reference, hasReference := cell.Sides["xray"]
		if hasOurs && hasReference {
			cell.Verdicts["throughput"] = verdict(ours.MBytesPerSec, reference.MBytesPerSec, true)
			cell.Verdicts["throughput_ops"] = verdict(ours.OpsPerSec, reference.OpsPerSec, true)
			cell.Verdicts["latency_p50"] = verdict(ours.LatencyP50Us, reference.LatencyP50Us, false)
			cell.Verdicts["latency_p99"] = verdict(ours.LatencyP99Us, reference.LatencyP99Us, false)
			cell.Verdicts["cpu"] = verdict(ours.CPUSeconds, reference.CPUSeconds, false)
			cell.Verdicts["cpu_per_byte"] = verdict(ours.MicroSecPerMByte, reference.MicroSecPerMByte, false)
		}
		summary.Cells = append(summary.Cells, cell)
	}
	return summary
}

func summarizeSide(runs []benchRunResult) benchSideStats {
	stats := benchSideStats{}
	var throughput, ops, p50, p99, cpu, perByte, perOp []float64
	for _, run := range runs {
		if run.Report == nil {
			stats.Errors++
			continue
		}
		stats.Runs++
		stats.Mismatches += run.Report.Mismatches
		if run.Report.MBytesPerSec > 0 {
			throughput = append(throughput, run.Report.MBytesPerSec)
		}
		if run.Report.OpsPerSec > 0 {
			ops = append(ops, run.Report.OpsPerSec)
		}
		if run.Report.Latency != nil {
			p50 = append(p50, run.Report.Latency.P50)
			p99 = append(p99, run.Report.Latency.P99)
		}
		if run.Report.CPUSeconds > 0 {
			cpu = append(cpu, run.Report.CPUSeconds)
		}
		if run.Report.MicroSecPerMB > 0 {
			perByte = append(perByte, run.Report.MicroSecPerMB)
		}
		if run.Report.MicroSecPerOp > 0 {
			perOp = append(perOp, run.Report.MicroSecPerOp)
		}
	}
	stats.MBytesPerSec = median(throughput)
	stats.OpsPerSec = median(ops)
	stats.LatencyP50Us = median(p50)
	stats.LatencyP99Us = median(p99)
	stats.CPUSeconds = median(cpu)
	stats.MicroSecPerMByte = median(perByte)
	stats.MicroSecPerOp = median(perOp)
	return stats
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return round3(sorted[middle])
	}
	return round3((sorted[middle-1] + sorted[middle]) / 2)
}

// verdict compares one metric. higherIsBetter decides the direction; anything
// within 5% is reported as a tie rather than as a win, because a single-digit
// percentage on this class of measurement is inside the noise.
func verdict(ours float64, reference float64, higherIsBetter bool) string {
	if ours == 0 || reference == 0 {
		return "n/a"
	}
	ratio := ours / reference
	if !higherIsBetter {
		ratio = reference / ours
	}
	switch {
	case ratio > 1.05:
		return "ours"
	case ratio < 0.95:
		return "xray"
	default:
		return "tie"
	}
}

func renderSummaryMarkdown(sessionDir string, environments []benchEnv, loads []benchLoad, results []benchRunResult, summary benchSummary) string {
	report := &strings.Builder{}
	fmt.Fprintf(report, "## XHTTP benchmark session %s\n\n", filepath.Base(sessionDir))
	fmt.Fprintf(report, "Reference server: shared Xray process per environment. Clients: this fork's sing-box vs the reference Xray client.\n")
	fmt.Fprintf(report, "Medians over repetitions; order alternates so drift lands on both sides.\n\n")

	index := make(map[string]benchSummaryCell, len(summary.Cells))
	for _, cell := range summary.Cells {
		index[cell.Env+"|"+cell.Load] = cell
	}
	for _, env := range environments {
		fmt.Fprintf(report, "### %s\n\n", env.name())
		fmt.Fprintf(report, "| load | metric | ours | xray | ratio | verdict |\n|---|---|---|---|---|---|\n")
		any := false
		for _, load := range loads {
			cell, ok := index[env.name()+"|"+load.name()]
			if !ok {
				continue
			}
			any = true
			ours, hasOurs := cell.Sides["ours"]
			reference, hasReference := cell.Sides["xray"]
			if !hasOurs || !hasReference {
				continue
			}
			rows := [][4]string{
				{"MB/s", formatNumber(ours.MBytesPerSec), formatNumber(reference.MBytesPerSec), ratioText(ours.MBytesPerSec, reference.MBytesPerSec, true)},
				{"ops/s", formatNumber(ours.OpsPerSec), formatNumber(reference.OpsPerSec), ratioText(ours.OpsPerSec, reference.OpsPerSec, true)},
				{"p50 us", formatNumber(ours.LatencyP50Us), formatNumber(reference.LatencyP50Us), ratioText(ours.LatencyP50Us, reference.LatencyP50Us, false)},
				{"p99 us", formatNumber(ours.LatencyP99Us), formatNumber(reference.LatencyP99Us), ratioText(ours.LatencyP99Us, reference.LatencyP99Us, false)},
				{"cpu s", formatNumber(ours.CPUSeconds), formatNumber(reference.CPUSeconds), ratioText(ours.CPUSeconds, reference.CPUSeconds, false)},
				{"us/MB", formatNumber(ours.MicroSecPerMByte), formatNumber(reference.MicroSecPerMByte), ratioText(ours.MicroSecPerMByte, reference.MicroSecPerMByte, false)},
				{"us/op", formatNumber(ours.MicroSecPerOp), formatNumber(reference.MicroSecPerOp), ratioText(ours.MicroSecPerOp, reference.MicroSecPerOp, false)},
			}
			for _, row := range rows {
				metric := row[0]
				if row[1] == "0" && row[2] == "0" {
					continue
				}
				fmt.Fprintf(report, "| %s | %s | %s | %s | %s | %s |\n",
					load.name(), metric, row[1], row[2], row[3], verdictLabel(metric, row[3]))
			}
		}
		if !any {
			fmt.Fprintf(report, "| (no results) | | | | | |\n")
		}
		fmt.Fprintf(report, "\n")
	}

	// Correctness and failures are part of the verdict: a faster run that
	// corrupted the payload is not a win.
	failures := 0
	mismatches := int64(0)
	for _, result := range results {
		if result.Error != "" {
			failures++
		}
		if result.Report != nil {
			mismatches += result.Report.Mismatches
		}
	}
	fmt.Fprintf(report, "### Integrity\n\n- runs: %d, failed runs: %d, payload mismatches: %d\n",
		len(results), failures, mismatches)
	return report.String()
}

func formatNumber(value float64) string {
	if value == 0 {
		return "0"
	}
	if value >= 1000 {
		return fmt.Sprintf("%.0f", value)
	}
	if value >= 1 {
		return fmt.Sprintf("%.2f", value)
	}
	return fmt.Sprintf("%.4f", value)
}

func ratioText(ours float64, reference float64, higherIsBetter bool) string {
	if ours == 0 || reference == 0 {
		return "n/a"
	}
	ratio := ours / reference
	if !higherIsBetter {
		ratio = reference / ours
	}
	return fmt.Sprintf("%.2fx", ratio)
}

func verdictLabel(metric string, ratio string) string {
	if ratio == "n/a" {
		return "-"
	}
	value := 0.0
	_, _ = fmt.Sscanf(ratio, "%fx", &value)
	switch {
	case value > 1.05:
		return "ours"
	case value < 0.95:
		return "xray"
	default:
		return "tie"
	}
}
