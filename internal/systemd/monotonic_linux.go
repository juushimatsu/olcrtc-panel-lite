//go:build linux

package systemd

import (
	"os"
	"strconv"
	"strings"
)

func monotonicMicros() (uint64, bool) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	return parseProcUptimeMicros(fields[0])
}

func parseProcUptimeMicros(value string) (uint64, bool) {
	secondsText, fractionText, _ := strings.Cut(strings.TrimSpace(value), ".")
	seconds, err := strconv.ParseUint(secondsText, 10, 64)
	if err != nil {
		return 0, false
	}
	if len(fractionText) > 6 {
		fractionText = fractionText[:6]
	}
	for len(fractionText) < 6 {
		fractionText += "0"
	}
	fraction := uint64(0)
	if fractionText != "" {
		fraction, err = strconv.ParseUint(fractionText, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	if seconds > (^uint64(0)-fraction)/1_000_000 {
		return 0, false
	}
	return seconds*1_000_000 + fraction, true
}
