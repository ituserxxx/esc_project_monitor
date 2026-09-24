package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ProbeTask 是 Hub 侧主动探测任务（无 Agent 采集方式）。
type ProbeTask struct {
	ID       string                 `json:"id"`
	Plugin   string                 `json:"plugin"`   // http / tcp / ping 等探测插件
	Interval int                    `json:"interval"` // 探测间隔（秒）
	Params   map[string]interface{} `json:"params"`
	Rule     *Rule                  `json:"rule"`
}

// Silence 告警静默规则。
type Silence struct {
	TaskIDs   []string `json:"task_ids"` // 命中的任务 ID，空表示全部
	Start     string   `json:"start"`    // RFC3339 起始时间
	End       string   `json:"end"`      // RFC3339 结束时间
	Reason    string   `json:"reason,omitempty"`
	CreatedBy string   `json:"created_by,omitempty"`
}

// Active 判断当前是否处于静默期。
func (s Silence) Active(now time.Time) bool {
	st, err1 := time.Parse(time.RFC3339, s.Start)
	et, err2 := time.Parse(time.RFC3339, s.End)
	if err1 != nil || err2 != nil {
		return false
	}
	return now.After(st) && now.Before(et)
}

// AgentConfig 是 Hub 为单个 Agent 保存的版本化配置。
type AgentConfig struct {
	Version string `json:"version"` // 内容哈希
	AgentID string `json:"agent_id"`
	Tasks   []Task `json:"tasks"`
}

// ComputeVersion 计算配置内容哈希（忽略传入的 Version 字段）。
func ComputeVersion(c AgentConfig) string {
	c.Version = ""
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// HubConfig 是 Hub 主配置文件（configs/hub.json）。
type HubConfig struct {
	Listen  string `json:"listen"`   // HTTP 监听地址，如 :8080
	DBPath  string `json:"db_path"`  // SQLite 文件路径
	DataDir string `json:"data_dir"` // Agent 配置缓存目录

	WeComWebhook string `json:"wecom_webhook"` // 企业微信机器人 Webhook
	DedupWindow  int    `json:"dedup_window"`  // 告警去重窗口（秒）
	RecoverHold  int    `json:"recover_hold"`  // 全局默认恢复持续秒数

	Probes   []ProbeTask   `json:"probes"`   // Hub 主动探测任务
	Agents   []AgentConfig `json:"agents"`   // Agent 配置（版本由 Hub 启动时计算）
	Silences []Silence     `json:"silences"` // 静默 / 维护窗口
}

// Validate 做最小校验。
func (h *HubConfig) Validate() error {
	if h.Listen == "" {
		h.Listen = ":8080"
	}
	if h.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	if h.DataDir == "" {
		h.DataDir = "data/agents"
	}
	for _, p := range h.Probes {
		if p.ID == "" || p.Plugin == "" {
			return fmt.Errorf("probe requires id and plugin")
		}
		if p.Interval <= 0 {
			p.Interval = 60
		}
	}
	for _, a := range h.Agents {
		if a.AgentID == "" {
			return fmt.Errorf("agent config requires agent_id")
		}
		for _, t := range a.Tasks {
			if t.ID == "" || t.Plugin == "" {
				return fmt.Errorf("agent %s: task requires id and plugin", a.AgentID)
			}
		}
	}
	return nil
}
