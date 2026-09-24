//go:build linux

package sys

import (
	"context"

	"sentinel/internal/core"
)

type diskPlugin struct{}

func init() { core.Register("disk", diskPlugin{}) }

// Collect 对指定挂载点（默认 /）输出磁盘使用率，实现见 diskstat_linux.go。
func (diskPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	path := core.ParamString(task, "path", "/")
	return diskUsage(path)
}
