//go:build !linux

package sysinfo

func collectPlatform(_ *Metrics, _ string) {}
