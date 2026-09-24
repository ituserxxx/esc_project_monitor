// Package probe 无 Agent 主动探测插件：http / tcp / ping（Hub 与 Agent 均可使用）。
package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sentinel/internal/core"
)

type httpPlugin struct{}

func init() { core.Register("http", httpPlugin{}) }

// Collect 探测目标 URL。
// 参数：url（必填）、method（默认 GET）、expect_status（默认 200-399）、
// keyword（响应体需包含的关键字，可选）、timeout（秒，默认 5）。
func (httpPlugin) Collect(ctx context.Context, task core.Task) ([]core.Metric, error) {
	url := core.ParamString(task, "url", "")
	if url == "" {
		return nil, fmt.Errorf("http plugin requires param: url")
	}
	method := core.ParamString(task, "method", http.MethodGet)
	timeout := time.Duration(core.ParamFloat(task, "timeout", 5)) * time.Second

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}

	start := time.Now()
	resp, err := client.Do(req)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return []core.Metric{
			{Name: "probe_success", Value: 0, Up: false, Message: err.Error(), Labels: lbl(url)},
			{Name: "probe_latency_ms", Value: latency, Up: false, Labels: lbl(url)},
		}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	expect := int(core.ParamFloat(task, "expect_status", 0))
	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	if expect > 0 {
		ok = resp.StatusCode == expect
	}
	msg := fmt.Sprintf("status=%d", resp.StatusCode)
	if kw := core.ParamString(task, "keyword", ""); kw != "" && ok {
		if !strings.Contains(string(body), kw) {
			ok = false
			msg = fmt.Sprintf("status=%d keyword %q missing", resp.StatusCode, kw)
		}
	}
	succ := 0.0
	if ok {
		succ = 1
	}
	return []core.Metric{
		{Name: "probe_success", Value: succ, Up: true, Message: msg, Labels: lbl(url)},
		{Name: "probe_latency_ms", Value: latency, Up: true, Labels: lbl(url)},
		{Name: "probe_status_code", Value: float64(resp.StatusCode), Up: true, Labels: lbl(url)},
	}, nil
}

func lbl(target string) map[string]string { return map[string]string{"target": target} }
