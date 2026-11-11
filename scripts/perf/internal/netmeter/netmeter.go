package netmeter

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// Read returns the counters for iface, or an error when they are unavailable.
//
// sysfs is not network-namespace aware: inside a new netns `/sys/class/net`
// still lists the host's devices, so a veth that exists only in the namespace
// has no statistics file there. The laboratory therefore asks `ip`, which uses
// netlink and sees the namespace's own interfaces.
func Read(iface string) (Counters, error) {
	if counters, err := readViaIP(iface); err == nil {
		return counters, nil
	}
	return readViaSysfs(iface)
}

type ipLinkStats struct {
	IfName  string `json:"ifname"`
	Stats64 struct {
		Rx struct {
			Bytes   int64 `json:"bytes"`
			Packets int64 `json:"packets"`
		} `json:"rx"`
		Tx struct {
			Bytes   int64 `json:"bytes"`
			Packets int64 `json:"packets"`
		} `json:"tx"`
	} `json:"stats64"`
}

func readViaIP(iface string) (Counters, error) {
	output, err := exec.Command("ip", "-j", "-s", "link", "show", "dev", iface).Output()
	if err != nil {
		return Counters{}, err
	}
	var links []ipLinkStats
	if err = json.Unmarshal(output, &links); err != nil {
		return Counters{}, err
	}
	if len(links) == 0 {
		return Counters{}, fmt.Errorf("ip reported no link for %s", iface)
	}
	link := links[0]
	return Counters{
		Interface: link.IfName,
		RxBytes:   link.Stats64.Rx.Bytes,
		TxBytes:   link.Stats64.Tx.Bytes,
		RxPackets: link.Stats64.Rx.Packets,
		TxPackets: link.Stats64.Tx.Packets,
	}, nil
}

func readViaSysfs(iface string) (Counters, error) {
	c := Counters{Interface: iface}
	base := "/sys/class/net/" + iface + "/statistics"
	var err error
	if c.RxBytes, err = readCounter(base + "/rx_bytes"); err != nil {
		return c, err
	}
	if c.TxBytes, err = readCounter(base + "/tx_bytes"); err != nil {
		return c, err
	}
	if c.RxPackets, err = readCounter(base + "/rx_packets"); err != nil {
		return c, err
	}
	if c.TxPackets, err = readCounter(base + "/tx_packets"); err != nil {
		return c, err
	}
	return c, nil
}

// Interfaces lists the interfaces of the current network namespace.
func Interfaces() []InterfaceInfo {
	output, err := exec.Command("ip", "-j", "link", "show").Output()
	if err != nil {
		return nil
	}
	var links []struct {
		IfName string `json:"ifname"`
		MTU    int    `json:"mtu"`
	}
	if err = json.Unmarshal(output, &links); err != nil {
		return nil
	}
	out := make([]InterfaceInfo, 0, len(links))
	for _, link := range links {
		out = append(out, InterfaceInfo{Name: link.IfName, MTU: link.MTU})
	}
	return out
}

// InterfaceInfo is one interface in the namespace.
type InterfaceInfo struct {
	Name      string
	MTU       int
	Addresses []string
}
