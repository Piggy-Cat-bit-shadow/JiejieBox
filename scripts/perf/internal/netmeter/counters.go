package netmeter

import (
	"os"
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

func readCounter(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return parseInt(string(raw))
}

func parseInt(text string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}
