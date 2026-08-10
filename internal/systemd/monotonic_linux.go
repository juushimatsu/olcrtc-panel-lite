//go:build linux

package systemd

import "golang.org/x/sys/unix"

func monotonicMicros() (uint64, bool) {
	var value unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &value); err != nil || value.Sec < 0 || value.Nsec < 0 {
		return 0, false
	}
	return uint64(value.Sec)*1_000_000 + uint64(value.Nsec)/1_000, true
}
