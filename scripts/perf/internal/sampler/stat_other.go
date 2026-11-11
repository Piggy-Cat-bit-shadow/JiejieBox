//go:build !linux && !windows

package sampler

import (
	"errors"
	"time"
)

func linuxClockTicks() int { return 0 }

func readStat(pid int) (Stat, error) {
	return Stat{At: time.Now()}, errors.New("process sampling is not implemented on this platform")
}
