//go:build linux

// Package sys 主机内部指标插件：cpu / mem / disk / proc（仅供 Agent 使用）。
// 全部实现依赖 Linux（/proc 与 statfs），仅支持 Linux/Ubuntu 环境。
package sys

import (
	"context"

	"sentinel/internal/core"
)

type cpuPlugin struct{}

func init() { core.Register("cpu", cpuPlugin{}) }

// Collect 采样两次计数器（默认间隔 1s）计算 CPU 使用率百分比。
// 实现见 cpu_linux.go（读 /proc/stat）。
func (cpuPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	return collectCPU(ctx, task)
}
