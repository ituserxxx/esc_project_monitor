//go:build linux

package sys

import (
	"context"

	"sentinel/internal/core"
)

type procPlugin struct{}

func init() { core.Register("proc", procPlugin{}) }

// Collect 检查指定进程名是否存在（/proc/<pid>/comm 精确匹配），实现见 proc_linux.go。
// 参数：name（进程名）。
func (procPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	name := core.ParamString(task, "name", "")
	if name == "" {
		return nil, errParam("proc plugin requires param: name")
	}
	return collectProc(ctx, name)
}

type paramError string

func (e paramError) Error() string { return string(e) }
func errParam(s string) error      { return paramError(s) }
