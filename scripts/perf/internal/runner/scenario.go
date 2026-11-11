// Package runner starts a real server and a real client, drives traffic between
// them, and writes the result in the shared schema.
//
// The division of labour is deliberate: this package owns process lifecycle,
// sampling and bookkeeping; the configs under scenarios/ own protocol details.
// Adding a protocol therefore means adding two templates, not touching the
// measurement code.
package runner

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Scenario is the on-disk description of one cell.
type Scenario struct {
	ID          string `json:"id"`
	Protocol    string `json:"protocol"`
	Mode        string `json:"mode,omitempty"`
	Transport   string `json:"transport,omitempty"`
	Security    string `json:"security,omitempty"`
	Description string `json:"description,omitempty"`

	Server Side `json:"server"`
	Client Side `json:"client"`

	Target Target `json:"target"`

	// Vars are extra template variables, e.g. a uuid or a password.
	Vars map[string]string `json:"vars,omitempty"`

	// Extra is copied into the run record, for protocol-specific facts.
	Extra map[string]any `json:"extra,omitempty"`

	Defaults Defaults `json:"defaults"`
}

// Side is one of the two proxy processes.
type Side struct {
	Config string `json:"config"`
	// Binary selects which build to use: "lab" (extended tags) or "default".
	Binary string `json:"binary,omitempty"`
	// Args are extra command-line arguments after "run -c <config>".
	Args []string `json:"args,omitempty"`
	// Ready describes how to know the process is serving.
	Ready Ready `json:"ready"`
}

// Ready is a readiness probe.
type Ready struct {
	// TCP is an address template that must accept connections.
	TCP string `json:"tcp,omitempty"`
	// SleepMS is a settle time after the process starts.
	SleepMS int `json:"sleep_ms,omitempty"`
}

// Target is the far end behind the proxy.
//
// Bind and Address differ for tunnels: the endpoint's own stack rewrites a
// destination that is its local tunnel address back to loopback, so the target
// must listen on loopback while the load generator dials the tunnel address.
type Target struct {
	Kind    string `json:"kind"`
	Network string `json:"network"`
	// Bind is the address the target listens on; empty means the run's site
	// address.
	Bind string `json:"bind,omitempty"`
	// Address is the address the load generator dials through the proxy; empty
	// means the bind address.
	Address string `json:"address,omitempty"`
}

// Defaults are the load model used when the command line does not override it.
type Defaults struct {
	Patterns []string `json:"patterns"`
	Streams  []int    `json:"streams"`
	Duration string   `json:"duration"`
	Warmup   string   `json:"warmup"`
	Reps     int      `json:"reps"`
	// RestartPerPattern starts a fresh process pair for every pattern instead of
	// reusing one pair for the whole scenario. It exists for protocols whose
	// userspace stack carries state across flows that outlives a phase: a cell
	// that only works as the first pattern of a process is a measurement
	// artefact, and isolating the patterns is cheaper than restarting the whole
	// scenario by hand.
	RestartPerPattern bool `json:"restart_per_pattern,omitempty"`
}

// LoadScenario reads scenario.json from dir.
func LoadScenario(dir string) (*Scenario, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "scenario.json"))
	if err != nil {
		return nil, err
	}
	var scenario Scenario
	if err = json.Unmarshal(raw, &scenario); err != nil {
		return nil, fmt.Errorf("parse scenario.json: %w", err)
	}
	if scenario.ID == "" {
		scenario.ID = filepath.Base(dir)
	}
	if scenario.Server.Config == "" || scenario.Client.Config == "" {
		return nil, fmt.Errorf("scenario %s must name server.config and client.config", scenario.ID)
	}
	return &scenario, nil
}

// templateVars are the substitutions available to config templates. They use
// the same __NAME__ convention as the repository's existing fixtures, so a
// reader does not have to learn a second templating language.
type templateVars map[string]string

func (v templateVars) render(text string) string {
	pairs := make([]string, 0, len(v)*2)
	for key, value := range v {
		pairs = append(pairs, "__"+key+"__", value)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// renderFile substitutes vars into the template at path and writes the result.
func renderFile(templatePath, outputPath string, vars templateVars) error {
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	rendered := vars.render(string(raw))
	if unresolved := findPlaceholders(rendered); len(unresolved) > 0 {
		return fmt.Errorf("%s still contains placeholders %v", templatePath, unresolved)
	}
	return os.WriteFile(outputPath, []byte(rendered), 0o644)
}

func findPlaceholders(text string) []string {
	var found []string
	seen := make(map[string]bool)
	for {
		start := strings.Index(text, "__")
		if start < 0 {
			break
		}
		rest := text[start+2:]
		end := strings.Index(rest, "__")
		if end < 0 {
			break
		}
		name := rest[:end]
		if name != "" && !strings.ContainsAny(name, " \t\n\"':,") && !seen[name] {
			seen[name] = true
			found = append(found, name)
		}
		text = rest[end+2:]
	}
	return found
}

// ReservePort finds a port that is free for both TCP and UDP on addr, because a
// QUIC or WireGuard listener needs the UDP side while the readiness probe uses
// the TCP side.
func ReservePort(addr string) (int, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	for attempt := 0; attempt < 200; attempt++ {
		port := 20000 + int(time.Now().UnixNano()%20000) + attempt
		if port > 60000 {
			port = 20000 + attempt
		}
		tcpAddr := net.JoinHostPort(host, strconv.Itoa(port))
		listener, listenErr := net.Listen("tcp", tcpAddr)
		if listenErr != nil {
			continue
		}
		packet, packetErr := net.ListenPacket("udp", tcpAddr)
		if packetErr != nil {
			_ = listener.Close()
			continue
		}
		_ = packet.Close()
		_ = listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free tcp+udp port on %s", addr)
}
