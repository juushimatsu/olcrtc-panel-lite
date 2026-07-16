// Package sysinfo collects lightweight host and panel metrics.
package sysinfo

import (
	"runtime"
	"time"
)

// Metrics is the dashboard system snapshot.
type Metrics struct {
	CPUPercent      float64   `json:"cpu_percent"`
	CPUCores        int       `json:"cpu_cores"`
	MemoryUsed      uint64    `json:"memory_used"`
	MemoryTotal     uint64    `json:"memory_total"`
	SwapUsed        uint64    `json:"swap_used"`
	SwapTotal       uint64    `json:"swap_total"`
	DiskUsed        uint64    `json:"disk_used"`
	DiskTotal       uint64    `json:"disk_total"`
	Load1           float64   `json:"load_1"`
	Load5           float64   `json:"load_5"`
	Load15          float64   `json:"load_15"`
	OSUptimeSeconds int64     `json:"os_uptime_seconds"`
	PanelHeap       uint64    `json:"panel_heap"`
	Goroutines      int       `json:"goroutines"`
	CollectedAt     time.Time `json:"collected_at"`
}

// Collect returns portable runtime metrics plus platform host metrics.
func Collect(diskPath string) Metrics {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := Metrics{CPUCores: runtime.NumCPU(), PanelHeap: memory.HeapAlloc, Goroutines: runtime.NumGoroutine(), CollectedAt: time.Now()}
	collectPlatform(&result, diskPath)
	return result
}
