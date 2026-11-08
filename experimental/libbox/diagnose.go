package libbox

import (
	stdjson "encoding/json"

	"github.com/sagernet/sing-box/common/physicalpath"
	E "github.com/sagernet/sing/common/exceptions"
)

// ExplainPath returns the passive path explanation for root/network as JSON.
//
// # What it is
//
// A projection of the SAME read-only status model the Box already publishes: control descent,
// packet-order hops, readiness/owned state, the recorded failure and every unknown. It adds no
// matcher, dial, DNS query, session, health write or selection rotation - Box.Status is documented
// and tested as a value read, and this method only marshals it.
//
// # What it is not
//
// It does not explain which route RULE matched, and it does not run a connection test; those are
// separate slices (O01 remainder / O02). A missing hop is reported as unknown with its reason rather
// than being omitted.
func (s *CommandServer) ExplainPath(root string, network string) (string, error) {
	if s == nil || s.StartedService == nil {
		return "", E.New("command server is not started")
	}
	instance := s.Instance()
	if instance == nil {
		return "", E.New("service is not running")
	}
	boxInstance := instance.Box()
	if boxInstance == nil {
		return "", E.New("box is not running")
	}
	explanation := newPathExplanation(boxInstance.Status(root, network))
	encoded, err := stdjson.Marshal(explanation)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type pathExplanation struct {
	Root        string               `json:"root"`
	Network     string               `json:"network"`
	Ready       bool                 `json:"ready"`
	Entry       string               `json:"entry,omitempty"`
	Exit        string               `json:"exit,omitempty"`
	ControlPath []string             `json:"controlPath,omitempty"`
	Controls    []controlExplanation `json:"controls,omitempty"`
	Hops        []hopExplanation     `json:"hops,omitempty"`
	Failure     *failureExplanation  `json:"failure,omitempty"`
	Unknowns    []unknownExplanation `json:"unknowns,omitempty"`
}

type controlExplanation struct {
	Tag       string `json:"tag"`
	Decision  string `json:"decision,omitempty"`
	Committed bool   `json:"committed"`
	Reason    string `json:"reason,omitempty"`
}

type hopExplanation struct {
	Position           int    `json:"position"`
	Tag                string `json:"tag"`
	LeafTag            string `json:"leafTag,omitempty"`
	Type               string `json:"type,omitempty"`
	Endpoint           bool   `json:"endpoint,omitempty"`
	SelectedBy         string `json:"selectedBy,omitempty"`
	Readiness          string `json:"readiness"`
	ClaimedState       string `json:"claimedState"`
	LastError          string `json:"lastError,omitempty"`
	LastErrorPhase     string `json:"lastErrorPhase,omitempty"`
	LastErrorRecovered bool   `json:"lastErrorRecovered,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type failureExplanation struct {
	Hop       string   `json:"hop"`
	Position  int      `json:"position"`
	HopCount  int      `json:"hopCount"`
	Phase     string   `json:"phase"`
	Reached   []string `json:"reached,omitempty"`
	Unreached []string `json:"unreached,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	Recovered bool     `json:"recovered"`
}

type unknownExplanation struct {
	Node     string `json:"node"`
	Position int    `json:"position"`
	Reason   string `json:"reason"`
}

func newPathExplanation(status physicalpath.PathStatus) pathExplanation {
	explanation := pathExplanation{
		Root:        status.Root,
		Network:     status.Network,
		Ready:       status.Ready(),
		Entry:       status.Entry,
		Exit:        status.Exit,
		ControlPath: append([]string(nil), status.ControlPath...),
	}
	for _, control := range status.Controls {
		explanation.Controls = append(explanation.Controls, controlExplanation{
			Tag:       control.Tag,
			Decision:  control.Decision,
			Committed: control.Committed,
			Reason:    control.Reason,
		})
	}
	for _, hop := range status.Hops {
		entry := hopExplanation{
			Position:           hop.Position,
			Tag:                hop.Tag,
			LeafTag:            hop.LeafTag,
			Type:               hop.Type,
			Endpoint:           hop.Endpoint,
			SelectedBy:         hop.SelectedBy,
			Readiness:          hop.Readiness.String(),
			ClaimedState:       hop.ClaimedState.String(),
			LastError:          hop.LastError,
			LastErrorRecovered: hop.LastErrorRecovered,
			Reason:             hop.Reason,
		}
		if hop.LastError != "" {
			entry.LastErrorPhase = hop.LastErrorPhase.String()
		}
		explanation.Hops = append(explanation.Hops, entry)
	}
	if status.Failure != nil {
		explanation.Failure = &failureExplanation{
			Hop:       status.Failure.Hop,
			Position:  status.Failure.Position,
			HopCount:  status.Failure.HopCount,
			Phase:     status.Failure.Phase.String(),
			Reached:   append([]string(nil), status.Failure.Reached...),
			Unreached: append([]string(nil), status.Failure.Unreached...),
			Detail:    status.Failure.Detail,
			Recovered: status.Failure.Recovered,
		}
	}
	for _, unknown := range status.Unknowns {
		explanation.Unknowns = append(explanation.Unknowns, unknownExplanation{
			Node:     unknown.Node,
			Position: unknown.Position,
			Reason:   unknown.Reason,
		})
	}
	return explanation
}
