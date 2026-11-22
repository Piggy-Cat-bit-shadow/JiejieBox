package catalog

import (
	"encoding/json"
	"strings"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"
)

// NetworkList is the record form of option.NetworkList. The in-memory type stores a newline-joined
// string; a record must round-trip the EXACT value (including the empty default), so the wire form is
// that string, with the same validation the configuration decoder applies.
type NetworkList struct {
	value option.NetworkList
}

func WrapNetworkList(value option.NetworkList) NetworkList {
	return NetworkList{value: value}
}

func (n NetworkList) Value() option.NetworkList {
	return n.value
}

func (n NetworkList) Build() []string {
	return n.value.Build()
}

func (n NetworkList) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(n.value))
}

func (n *NetworkList) UnmarshalJSON(content []byte) error {
	var raw string
	if err := json.Unmarshal(content, &raw); err != nil {
		return err
	}
	if raw != "" {
		for _, networkName := range strings.Split(raw, "\n") {
			switch networkName {
			case N.NetworkTCP, N.NetworkUDP:
			default:
				return E.New("node catalog: unknown network: " + networkName)
			}
		}
	}
	n.value = option.NetworkList(raw)
	return nil
}

// StringList is the record form of badoption.Listable[string]. A one-element list is encoded as a
// JSON array here (not a scalar), so a record is self-describing and immutable.
type StringList []string

func WrapStringList(value badoption.Listable[string]) StringList {
	return StringList(value)
}

func (l StringList) Value() badoption.Listable[string] {
	return badoption.Listable[string](l)
}

// Dialer is the subset of option.DialerOptions an admitted shape may carry. It is shared so Trojan,
// SOCKS and HTTP records cannot drift on how an accepted dialer field is represented.
type Dialer struct {
	ConnectTimeout             badoption.Duration    `json:"connect_timeout,omitempty"`
	TCPFastOpen                bool                  `json:"tcp_fast_open,omitempty"`
	TCPMultiPath               bool                  `json:"tcp_multipath,omitempty"`
	DisableTCPKeepAlive        bool                  `json:"disable_tcp_keep_alive,omitempty"`
	TCPKeepAlive               badoption.Duration    `json:"tcp_keep_alive,omitempty"`
	TCPKeepAliveInterval       badoption.Duration    `json:"tcp_keep_alive_interval,omitempty"`
	TCPKeepAliveSystemDefaults bool                  `json:"tcp_keep_alive_system_defaults,omitempty"`
	ReuseAddr                  bool                  `json:"reuse_addr,omitempty"`
	ProtectPath                string                `json:"protect_path,omitempty"`
	UDPBindPort                uint16                `json:"udp_bind_port,omitempty"`
	UDPFragment                *bool                 `json:"udp_fragment,omitempty"`
	UDPFragmentDefault         bool                  `json:"udp_fragment_default,omitempty"`
	DomainStrategy             option.DomainStrategy `json:"domain_strategy,omitempty"`
}

func WrapDialer(options option.DialerOptions) Dialer {
	return Dialer{
		ConnectTimeout:             options.ConnectTimeout,
		TCPFastOpen:                options.TCPFastOpen,
		TCPMultiPath:               options.TCPMultiPath,
		DisableTCPKeepAlive:        options.DisableTCPKeepAlive,
		TCPKeepAlive:               options.TCPKeepAlive,
		TCPKeepAliveInterval:       options.TCPKeepAliveInterval,
		TCPKeepAliveSystemDefaults: options.TCPKeepAliveSystemDefaults,
		ReuseAddr:                  options.ReuseAddr,
		ProtectPath:                options.ProtectPath,
		UDPBindPort:                options.UDPBindPort,
		UDPFragment:                copyBool(options.UDPFragment),
		UDPFragmentDefault:         options.UDPFragmentDefault,
		DomainStrategy:             options.DomainStrategy,
	}
}

func (d Dialer) Apply(options *option.DialerOptions) {
	options.ConnectTimeout = d.ConnectTimeout
	options.TCPFastOpen = d.TCPFastOpen
	options.TCPMultiPath = d.TCPMultiPath
	options.DisableTCPKeepAlive = d.DisableTCPKeepAlive
	options.TCPKeepAlive = d.TCPKeepAlive
	options.TCPKeepAliveInterval = d.TCPKeepAliveInterval
	options.TCPKeepAliveSystemDefaults = d.TCPKeepAliveSystemDefaults
	options.ReuseAddr = d.ReuseAddr
	options.ProtectPath = d.ProtectPath
	options.UDPBindPort = d.UDPBindPort
	options.UDPFragment = copyBool(d.UDPFragment)
	options.UDPFragmentDefault = d.UDPFragmentDefault
	options.DomainStrategy = d.DomainStrategy
}

func copyBool(source *bool) *bool {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
