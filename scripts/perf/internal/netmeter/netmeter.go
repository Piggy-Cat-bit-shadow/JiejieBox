// Package netmeter reads the kernel's own counters for the emulated link.
//
// A payload byte count and a wire byte count answer different questions, and
// the ratio between them is the protocol's overhead. The veth the run crosses
// is the only place where both directions can be counted without a packet
// capture and without privileges, so this package reads its statistics files.
package netmeter

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Counters is one interface's snapshot.
type Counters struct {
	Interface string
	RxBytes   int64
	TxBytes   int64
	RxPackets int64
	TxPackets int64
}

// Read returns the counters for iface, or an error when they are unavailable.
func Read(iface string) (Counters, error) {
	c := Counters{Interface: iface}
	base := filepath.Join("/sys/class/net", iface, "statistics")
	var err error
	if c.RxBytes, err = readCounter(filepath.Join(base, "rx_bytes")); err != nil {
		return c, err
	}
	if c.TxBytes, err = readCounter(filepath.Join(base, "tx_bytes")); err != nil {
		return c, err
	}
	if c.RxPackets, err = readCounter(filepath.Join(base, "rx_packets")); err != nil {
		return c, err
	}
	if c.TxPackets, err = readCounter(filepath.Join(base, "tx_packets")); err != nil {
		return c, err
	}
	return c, nil
}

// Delta subtracts before from after. A counter reset is reported by returning
// no delta rather than a negative number.
func Delta(before, after Counters) (Counters, bool) {
	if after.RxBytes < before.RxBytes || after.TxBytes < before.TxBytes ||
		after.RxPackets < before.RxPackets || after.TxPackets < before.TxPackets {
		return Counters{}, false
	}
	return Counters{
		Interface: after.Interface,
		RxBytes:   after.RxBytes - before.RxBytes,
		TxBytes:   after.TxBytes - before.TxBytes,
		RxPackets: after.RxPackets - before.RxPackets,
		TxPackets: after.TxPackets - before.TxPackets,
	}, true
}

// Interfaces lists the interfaces in the current network namespace with their
// MTU and addresses, for the run record.
func Interfaces() []InterfaceInfo {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []InterfaceInfo
	for _, entry := range entries {
		name := entry.Name()
		info := InterfaceInfo{Name: name}
		if mtu, mtuErr := readCounter(filepath.Join("/sys/class/net", name, "mtu")); mtuErr == nil {
			info.MTU = int(mtu)
		}
		info.Addresses = interfaceAddresses(name)
		out = append(out, info)
	}
	return out
}

// InterfaceInfo is one interface in the namespace.
type InterfaceInfo struct {
	Name      string
	MTU       int
	Addresses []string
}

func readCounter(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

func interfaceAddresses(name string) []string {
	raw, err := os.ReadFile(filepath.Join("/proc/net/fib_trie"))
	_ = raw
	_ = err
	// /proc/net/fib_trie does not attribute addresses to interfaces. The
	// addresses that matter to the lab are the ones the runner configured, and
	// those are recorded from the environment instead; this stays empty rather
	// than guessing.
	return nil
}
