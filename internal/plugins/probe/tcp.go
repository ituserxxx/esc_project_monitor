package probe

import (
	"context"
	"fmt"
	"net"
	"time"

	"sentinel/internal/core"
)

type tcpPlugin struct{}

func init() { core.Register("tcp", tcpPlugin{}) }

// Collect 探测 TCP 端口连通性。
// 参数：host（必填）、port（必填）、timeout（秒，默认 5）。
func (tcpPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	host := core.ParamString(task, "host", "")
	port := int(core.ParamFloat(task, "port", 0))
	if host == "" || port == 0 {
		return nil, fmt.Errorf("tcp plugin requires params: host, port")
	}
	timeout := time.Duration(core.ParamFloat(task, "timeout", 5)) * time.Second
	addr := net.JoinHostPort(host, fmt.Sprint(port))

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	latency := float64(time.Since(start).Milliseconds())

	succ, msg := 0.0, ""
	up := true
	if err != nil {
		msg = err.Error()
	} else {
		conn.Close()
		succ = 1
		msg = "connected"
	}
	return []core.Metric{
		{Name: "probe_success", Value: succ, Up: up, Message: msg, Labels: lbl(addr)},
		{Name: "probe_latency_ms", Value: latency, Up: up, Labels: lbl(addr)},
	}, nil
}
