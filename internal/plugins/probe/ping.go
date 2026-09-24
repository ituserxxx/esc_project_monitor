package probe

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"sentinel/internal/core"
)

type pingPlugin struct{}

func init() { core.Register("ping", pingPlugin{}) }

var reLatency = regexp.MustCompile(`time[=<]\s*([0-9.]+)\s*ms`)

// Collect 通过系统 ping 命令探测主机可达性。
// 参数：host（必填）、count（默认 1）、timeout（秒，默认 5）。
func (pingPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	host := core.ParamString(task, "host", "")
	if host == "" {
		return nil, fmt.Errorf("ping plugin requires param: host")
	}
	count := int(core.ParamFloat(task, "count", 1))
	timeout := int(core.ParamFloat(task, "timeout", 5))

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", fmt.Sprint(count), "-w", fmt.Sprint(timeout*1000), host)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", fmt.Sprint(count), "-W", fmt.Sprint(timeout), host)
	}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := float64(time.Since(start).Milliseconds())

	if err != nil {
		return []core.Metric{
			{Name: "probe_success", Value: 0, Up: false,
				Message: fmt.Sprintf("ping %s failed: %v", host, err), Labels: lbl(host)},
			{Name: "probe_latency_ms", Value: elapsed, Up: false, Labels: lbl(host)},
		}, nil
	}

	latency := elapsed
	if m := reLatency.FindSubmatch(out); m != nil {
		if v, perr := strconv.ParseFloat(string(m[1]), 64); perr == nil {
			latency = v
		}
	}
	return []core.Metric{
		{Name: "probe_success", Value: 1, Up: true, Message: "reachable", Labels: lbl(host)},
		{Name: "probe_latency_ms", Value: latency, Up: true, Labels: lbl(host)},
	}, nil
}
