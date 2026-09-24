// Package core 定义 Agent 与 Hub 共用的基础抽象：
// 指标结构、采集插件统一接口与全局注册表。
package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Metric 是一条采集结果。
type Metric struct {
	Plugin  string            `json:"plugin"`            // 插件名
	Name    string            `json:"name"`              // 指标名
	Value   float64           `json:"value"`             // 数值
	Up      bool              `json:"up"`                // 采集是否成功
	Message string            `json:"message,omitempty"` // 附加说明（失败原因等）
	Labels  map[string]string `json:"labels,omitempty"`  // 维度标签
}

// Task 是调度层传给插件的一次采集任务（对应配置中的一个插件实例）。
type Task struct {
	ID       string                 `json:"id"`       // 实例 ID（配置内唯一）
	Plugin   string                 `json:"plugin"`   // 插件名
	Interval int                    `json:"interval"` // 采集间隔（秒）
	Params   map[string]interface{} `json:"params"`   // 插件参数
	Rule     *Rule                  `json:"rule"`     // 可选：告警规则
}

// Rule 告警规则（阈值 + 持续时间 + 恢复条件）。
type Rule struct {
	Metric      string  `json:"metric"`       // 指标名
	Op          string  `json:"op"`           // > >= < <= == !=
	Threshold   float64 `json:"threshold"`    // 阈值
	Duration    int     `json:"duration"`     // 持续秒数，0 表示立即触发
	RecoverHold int     `json:"recover_hold"` // 恢复需持续的秒数
}

// Plugin 是所有采集插件的统一接口。
type Plugin interface {
	Collect(ctx context.Context, task Task) ([]Metric, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Plugin{}
)

// Register 在插件 init() 中调用，把插件注册进全局注册表。
func Register(name string, p Plugin) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("sentinel: duplicate plugin registration: " + name)
	}
	registry[name] = p
}

// Get 按名称取插件。
func Get(name string) (Plugin, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// List 返回全部已注册插件名（排序后）。
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// CheckRule 判断指标是否命中规则（未命中恢复逻辑由 alerter 处理）。
func CheckRule(m Metric, r *Rule) (bool, error) {
	if r == nil || (r.Metric != "" && r.Metric != m.Name) {
		return false, nil
	}
	switch r.Op {
	case ">":
		return m.Value > r.Threshold, nil
	case ">=":
		return m.Value >= r.Threshold, nil
	case "<":
		return m.Value < r.Threshold, nil
	case "<=":
		return m.Value <= r.Threshold, nil
	case "==":
		return m.Value == r.Threshold, nil
	case "!=":
		return m.Value != r.Threshold, nil
	}
	return false, fmt.Errorf("unknown rule op: %q", r.Op)
}

// ParamString 从任务参数里取字符串。
func ParamString(t Task, key, def string) string {
	if v, ok := t.Params[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// ParamFloat 从任务参数里取数字（JSON 反序列化后为 float64）。
func ParamFloat(t Task, key string, def float64) float64 {
	if v, ok := t.Params[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return def
}
