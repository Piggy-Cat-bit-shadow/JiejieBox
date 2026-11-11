// Command perflab is the runner for this repository's performance laboratory.
//
// It is a separate module on purpose: the lab must be buildable without the
// protocol modules, and building the kernel must not depend on the lab.
//
//	perflab env                 print the host and source facts a run would record
//	perflab run -scenario <dir> execute one scenario and write run.json + summary.md
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/host"
	"github.com/sagernet/sing-box/scripts/perf/internal/runner"
	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
	"github.com/sagernet/sing-box/scripts/perf/internal/source"
	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "env":
		if err := runEnv(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "perflab env:", err)
			os.Exit(1)
		}
	case "run":
		if err := runScenario(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "perflab run:", err)
			os.Exit(1)
		}
	case "target":
		if err := runTarget(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "perflab target:", err)
			os.Exit(1)
		}
	case "reserve":
		if err := runReserve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "perflab reserve:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `perflab — performance laboratory runner

  perflab env [-binary <path>] [-repo <dir>] [-json]
  perflab target -kind <sink|source|echo|duplex|udp-echo> -addr <host:port>
                 -control <host:port> [-duration 5s] [-verify=true]
  perflab run -scenario <dir> -out <dir> [-bin-lab <path>] [-bin-default <path>]
             [-bin-xray <path>]
             [-pattern upload] [-streams 1,4] [-duration 5s] [-warmup 1s] [-reps 3]
             [-run-id <id>] [-verify=true]

A scenario directory contains scenario.json, server.json.tmpl and
client.json.tmpl. Templates use the repository's __NAME__ placeholder
convention.

perflab target is the out-of-process form of the measurement target. The runner
starts it inside the server's network namespace when PERF_SERVER_NS is set, so
"server to target" stays local and only the tunnel crosses the shaped link.
`)
}

func runEnv(args []string) error {
	flags := flag.NewFlagSet("env", flag.ExitOnError)
	binary := flags.String("binary", "", "kernel binary to record build tags for")
	repo := flags.String("repo", "", "repository root (defaults to the working directory)")
	asJSON := flags.Bool("json", false, "print JSON instead of a summary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *repo == "" {
		if wd, err := os.Getwd(); err == nil {
			*repo = wd
		}
	}
	record := struct {
		SchemaVersion string        `json:"schema_version"`
		Source        schema.Source `json:"source"`
		Host          schema.Host   `json:"host"`
	}{
		SchemaVersion: schema.Version,
		Source:        source.Collect(*repo, *binary),
		Host:          host.Info(),
	}
	if *asJSON {
		raw, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
		return nil
	}
	fmt.Printf("repo:      %s\n", record.Source.Repo)
	fmt.Printf("branch:    %s\n", record.Source.Branch)
	fmt.Printf("commit:    %s %s\n", record.Source.Commit, record.Source.CommitSubject)
	if record.Source.Dirty != nil {
		fmt.Printf("dirty:     %v (%d files)\n", *record.Source.Dirty, len(record.Source.DirtyFiles))
	}
	fmt.Printf("go:        %s\n", record.Source.GoVersion)
	fmt.Printf("tags:      %s\n", strings.Join(record.Source.BuildTags, ","))
	fmt.Printf("host:      %s/%s %s %s kernel=%s wsl=%v\n", record.Host.OS, record.Host.Arch, record.Host.Distro, record.Host.DistroVersion, record.Host.Kernel, record.Host.WSL)
	fmt.Printf("cpu:       %s (%d cores)\n", record.Host.CPUModel, record.Host.CPUCores)
	fmt.Printf("clk_tck:   %v\n", valueOrNull(record.Host.ClkTck))
	fmt.Printf("glibc:     %s\n", record.Host.Glibc)
	return nil
}

func runScenario(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	scenarioDir := flags.String("scenario", "", "scenario directory")
	outDir := flags.String("out", "results", "output directory")
	runID := flags.String("run-id", "", "run identifier (defaults to scenario id + timestamp)")
	binLab := flags.String("bin-lab", "", "binary built with the lab tag set")
	binDefault := flags.String("bin-default", "", "binary built with the default tag set")
	binXray := flags.String("bin-xray", "", "reference Xray binary, for cells that need a server this repository does not implement")
	pattern := flags.String("pattern", "", "comma-separated patterns (overrides scenario defaults)")
	streams := flags.String("streams", "", "comma-separated concurrency levels (overrides scenario defaults)")
	duration := flags.Duration("duration", 0, "measured window per repetition")
	warmup := flags.Duration("warmup", 0, "warmup window per repetition")
	reps := flags.Int("reps", 0, "repetitions per cell")
	verify := flags.Bool("verify", true, "verify payload integrity")
	stagger := flags.Duration("stagger", 0, "space stream starts by this much (diagnostic: does the failure need simultaneous handshakes)")
	udpRate := flags.Int("udp-rate", 0, "paced UDP offered rate in packets/s (0 keeps the historical unpaced saturation mode)")
	udpDrain := flags.Duration("udp-drain", 0, "UDP post-send read window (0 = default: 500ms paced, none unpaced; negative disables)")
	udpPacketSize := flags.Int("udp-packet-size", 0, "UDP datagram size including the 20-byte header (default 1200)")
	quiet := flags.Bool("quiet", false, "only print the run directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *scenarioDir == "" {
		return fmt.Errorf("-scenario is required")
	}
	if *binLab == "" {
		return fmt.Errorf("-bin-lab is required (path to the kernel binary built with the lab tag set)")
	}
	options := runner.Options{
		ScenarioDir:   *scenarioDir,
		OutDir:        *outDir,
		RunID:         *runID,
		Binaries:      map[string]string{"lab": *binLab, "default": *binDefault, "xray": *binXray},
		Patterns:      splitList(*pattern),
		Streams:       parseIntList(*streams),
		Duration:      *duration,
		Warmup:        *warmup,
		Reps:          *reps,
		Verify:        *verify,
		Stagger:       *stagger,
		UDPRate:       *udpRate,
		UDPDrain:      *udpDrain,
		UDPPacketSize: *udpPacketSize,
	}
	if !*quiet {
		started := time.Now()
		options.Logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[%6.2fs] "+format+"\n", append([]any{time.Since(started).Seconds()}, args...)...)
		}
	}
	started := time.Now()
	outcome, err := runner.Run(options)
	if outcome != nil {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "run dir: %s (%s)\n", outcome.RunDir, time.Since(started).Round(time.Millisecond))
		} else {
			absolute, _ := filepath.Abs(outcome.RunDir)
			fmt.Println(absolute)
		}
	}
	return err
}

func runTarget(args []string) error {
	flags := flag.NewFlagSet("target", flag.ExitOnError)
	kind := flags.String("kind", "sink", "sink|source|echo|duplex|udp-echo")
	addr := flags.String("addr", "", "address to serve on")
	control := flags.String("control", "", "control endpoint address")
	duration := flags.Duration("duration", 5*time.Second, "how long a source keeps writing")
	verify := flags.Bool("verify", true, "verify payload against the pattern page")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *addr == "" || *control == "" {
		return fmt.Errorf("-addr and -control are required")
	}
	server, err := target.Start(target.Kind(*kind), *addr, *duration, *verify)
	if err != nil {
		return err
	}
	controlServer, err := target.ServeControl(*control, server)
	if err != nil {
		_ = server.Close()
		return err
	}
	_ = controlServer
	fmt.Printf("target %s serving %s, control %s\n", *kind, *addr, *control)
	// The runner terminates this process; it has no reason to exit on its own.
	select {}
}

func runReserve(args []string) error {
	flags := flag.NewFlagSet("reserve", flag.ExitOnError)
	addr := flags.String("addr", "", "address template to reserve on, e.g. 10.213.0.2:0")
	count := flags.Int("count", 1, "how many ports to reserve")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *addr == "" {
		return fmt.Errorf("-addr is required")
	}
	for i := 0; i < *count; i++ {
		port, err := runner.ReservePort(*addr)
		if err != nil {
			return err
		}
		fmt.Println(port)
	}
	return nil
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseIntList(value string) []int {
	var out []int
	for _, item := range splitList(value) {
		var parsed int
		if _, err := fmt.Sscanf(item, "%d", &parsed); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}

func valueOrNull(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
