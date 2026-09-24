//go:build linux

package sys

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"sentinel/internal/core"
)

type cpuTimes struct{ idle, total uint64 }

func readCPUTimes() (cpuTimes, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		var vals []uint64
		for _, s := range fields {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return cpuTimes{}, err
			}
			vals = append(vals, v)
		}
		if len(vals) < 4 {
			return cpuTimes{}, fmt.Errorf("unexpected /proc/stat cpu line")
		}
		var total uint64
		for _, v := range vals {
			total += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		return cpuTimes{idle: idle, total: total}, nil
	}
	return cpuTimes{}, fmt.Errorf("no aggregate cpu line in /proc/stat")
}

// collectCPU 采样两次 /proc/stat（默认间隔 1s）计算 CPU 使用率百分比。
func collectCPU(ctx context.Context, task core.Task) ([]core.Metric, error) {
	sample := time.Duration(core.ParamFloat(task, "sample_ms", 1000)) * time.Millisecond

	t1, err := readCPUTimes()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(sample):
	}
	t2, err := readCPUTimes()
	if err != nil {
		return nil, err
	}

	dTotal := float64(t2.total - t1.total)
	dIdle := float64(t2.idle - t1.idle)
	usage := 0.0
	if dTotal > 0 {
		usage = (1 - dIdle/dTotal) * 100
	}
	return []core.Metric{{
		Name: "cpu_usage_percent", Value: usage, Up: true,
		Message: fmt.Sprintf("%.1f%%", usage),
	}}, nil
}
