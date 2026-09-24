//go:build linux

package sys

import (
	"fmt"
	"syscall"

	"sentinel/internal/core"
)

func diskUsage(path string) ([]core.Metric, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	labels := map[string]string{"path": path}
	return []core.Metric{
		{Name: "disk_usage_percent", Value: pct, Up: true, Labels: labels,
			Message: fmt.Sprintf("%s %.1f%%", path, pct)},
		{Name: "disk_total_gb", Value: float64(total) / (1 << 30), Up: true, Labels: labels},
		{Name: "disk_free_gb", Value: float64(free) / (1 << 30), Up: true, Labels: labels},
	}, nil
}
