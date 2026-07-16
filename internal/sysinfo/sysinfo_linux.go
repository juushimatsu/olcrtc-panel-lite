//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func collectPlatform(result *Metrics, diskPath string) {
	result.CPUPercent = readCPUPercent()
	readMemInfo(result)
	readLoad(result)
	readUptime(result)
	var stat unix.Statfs_t
	if unix.Statfs(diskPath, &stat) == nil {
		result.DiskTotal = stat.Blocks * uint64(stat.Bsize)
		result.DiskUsed = (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	}
}

func readCPUPercent() float64 {
	firstIdle, firstTotal, ok := readCPUStat()
	if !ok {
		return 0
	}
	time.Sleep(75 * time.Millisecond)
	secondIdle, secondTotal, ok := readCPUStat()
	if !ok || secondTotal <= firstTotal {
		return 0
	}
	total := secondTotal - firstTotal
	idle := secondIdle - firstIdle
	return float64(total-idle) / float64(total) * 100
}

func readCPUStat() (uint64, uint64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return idle, total, true
}

func readMemInfo(result *Metrics) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	result.MemoryTotal = values["MemTotal"]
	result.MemoryUsed = result.MemoryTotal - values["MemAvailable"]
	result.SwapTotal = values["SwapTotal"]
	result.SwapUsed = result.SwapTotal - values["SwapFree"]
}

func readLoad(result *Metrics) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fmtFields := strings.Fields(string(b))
	if len(fmtFields) < 3 {
		return
	}
	result.Load1, _ = strconv.ParseFloat(fmtFields[0], 64)
	result.Load5, _ = strconv.ParseFloat(fmtFields[1], 64)
	result.Load15, _ = strconv.ParseFloat(fmtFields[2], 64)
}

func readUptime(result *Metrics) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	value, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	result.OSUptimeSeconds = int64(value)
}
