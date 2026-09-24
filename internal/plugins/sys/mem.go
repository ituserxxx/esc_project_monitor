//go:build linux

package sys

import (
	"context"

	"sentinel/internal/core"
)

type memPlugin struct{}

func init() { core.Register("mem", memPlugin{}) }

// Collect 输出内存使用率与可用量，实现见 mem_linux.go（读 /proc/meminfo）。
func (memPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	return collectMem(ctx, task)
}
