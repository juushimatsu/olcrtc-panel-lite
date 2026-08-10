//go:build !linux

package systemd

func monotonicMicros() (uint64, bool) {
	return 0, false
}
