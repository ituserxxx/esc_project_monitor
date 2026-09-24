//go:build linux

package sys

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"sentinel/internal/core"
)

func collectMem(ctx context.Context, task core.Task) ([]core.Metric, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	kv := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		kv[strings.TrimSuffix(fields[0], ":")] = v // kB
	}
	total := kv["MemTotal"]
	avail := kv["MemAvailable"]
	if total == 0 {
		return nil, fmt.Errorf("MemTotal not found")
	}
	used := total - avail
	pct := float64(used) / float64(total) * 100
	return []core.Metric{
		{Name: "mem_usage_percent", Value: pct, Up: true},
		{Name: "mem_total_mb", Value: float64(total) / 1024, Up: true},
		{Name: "mem_available_mb", Value: float64(avail) / 1024, Up: true},
	}, nil
}
