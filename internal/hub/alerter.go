package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"sentinel/internal/core"
)

// Alerter 负责阈值判断、持续时间、恢复、去重、静默与企业微信通知。
type Alerter struct {
	webhook     string
	dedupWindow time.Duration
	silences    []core.Silence
	store       *Store

	mu     sync.Mutex
	states map[string]*alertState // key: source|taskID|metric
}

type alertState struct {
	firing      bool
	breachSince time.Time // 首次越阈时间
	lastAlertAt time.Time // 上次发送告警时间
	okSince     time.Time // 恢复正常起始时间
}

// NewAlerter 创建告警器。
func NewAlerter(webhook string, dedupWindowSec int, silences []core.Silence, st *Store) *Alerter {
	if dedupWindowSec <= 0 {
		dedupWindowSec = 300
	}
	return &Alerter{
		webhook:     webhook,
		dedupWindow: time.Duration(dedupWindowSec) * time.Second,
		silences:    silences,
		store:       st,
		states:      map[string]*alertState{},
	}
}

// Evaluate 对一批指标执行规则判断。source 形如 "agent:xxx" 或 "hub"。
func (a *Alerter) Evaluate(source string, task core.Task, metrics []core.Metric) {
	if task.Rule == nil || a.webhook == "" {
		return
	}
	r := task.Rule
	for _, m := range metrics {
		if r.Metric != "" && r.Metric != m.Name {
			continue
		}
		breach, err := core.CheckRule(m, r)
		if err != nil {
			log.Printf("alerter: bad rule on task %s: %v", task.ID, err)
			return
		}
		a.process(source, task, m, r, breach)
	}
}

func (a *Alerter) process(source string, task core.Task, m core.Metric, r *core.Rule, breach bool) {
	key := source + "|" + task.ID + "|" + m.Name
	now := time.Now()

	a.mu.Lock()
	st := a.states[key]
	if st == nil {
		st = &alertState{}
		a.states[key] = st
	}

	var fireMsg, resolveMsg string
	var evt *AlertEvent

	if breach {
		st.okSince = time.Time{}
		if st.breachSince.IsZero() {
			st.breachSince = now
		}
		persisted := now.Sub(st.breachSince) >= time.Duration(r.Duration)*time.Second
		duplicated := now.Sub(st.lastAlertAt) < a.dedupWindow
		if persisted && !st.firing && !duplicated {
			st.firing = true
			st.lastAlertAt = now
			fireMsg = fmt.Sprintf("指标 **%s** = %.2f，触发规则 `%s %.2f`（持续 %ds）",
				m.Name, m.Value, r.Op, r.Threshold, r.Duration)
			evt = &AlertEvent{Source: source, TaskID: task.ID, TaskName: task.Plugin,
				Rule:  fmt.Sprintf("%s %s %.2f", m.Name, r.Op, r.Threshold),
				Value: m.Value, Status: "firing", CreatedAt: now}
		}
	} else {
		st.breachSince = time.Time{}
		if st.firing {
			if st.okSince.IsZero() {
				st.okSince = now
			}
			hold := r.RecoverHold
			if hold <= 0 {
				hold = 30
			}
			if now.Sub(st.okSince) >= time.Duration(hold)*time.Second {
				st.firing = false
				resolveMsg = fmt.Sprintf("指标 **%s** = %.2f，已恢复正常", m.Name, m.Value)
				evt = &AlertEvent{Source: source, TaskID: task.ID, TaskName: task.Plugin,
					Rule:  fmt.Sprintf("%s %s %.2f", m.Name, r.Op, r.Threshold),
					Value: m.Value, Status: "resolved", CreatedAt: now}
			}
		}
	}
	silenced := a.isSilenced(task.ID, now)
	a.mu.Unlock()

	if evt != nil {
		if err := a.store.InsertAlertEvent(*evt); err != nil {
			log.Printf("alerter: save event: %v", err)
		}
		if silenced {
			log.Printf("alerter: task %s silenced, skip notify", task.ID)
			return
		}
		text := fireMsg
		title := "🔴 告警触发"
		if evt.Status == "resolved" {
			text = resolveMsg
			title = "🟢 告警恢复"
		}
		content := fmt.Sprintf("### %s\n> 来源：%s\n> 任务：%s（%s）\n> 时间：%s\n%s",
			title, source, task.ID, task.Plugin, now.Format("2006-01-02 15:04:05"), text)
		if err := a.sendWeCom(content); err != nil {
			log.Printf("alerter: send wecom: %v", err)
		}
	}
}

// isSilenced 判断任务当前是否处于静默/维护窗口。调用时需持锁或先行拷贝。
func (a *Alerter) isSilenced(taskID string, now time.Time) bool {
	for _, s := range a.silences {
		if !s.Active(now) {
			continue
		}
		if len(s.TaskIDs) == 0 {
			return true
		}
		for _, id := range s.TaskIDs {
			if id == taskID {
				return true
			}
		}
	}
	return false
}

// SetSilences 热更新静默规则（配置重载时调用）。
func (a *Alerter) SetSilences(silences []core.Silence) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.silences = silences
}

type wecomPayload struct {
	Msgtype  string        `json:"msgtype"`
	Markdown wecomMarkdown `json:"markdown"`
}

type wecomMarkdown struct {
	Content string `json:"content"`
}

// sendWeCom 发送企业微信 Markdown 消息。
func (a *Alerter) sendWeCom(content string) error {
	if len(content) > 3800 { // 企业微信 markdown 上限约 4096 字节
		content = content[:3800] + "\n...(截断)"
	}
	body, _ := json.Marshal(wecomPayload{Msgtype: "markdown", Markdown: wecomMarkdown{Content: content}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wecom webhook returned %s", resp.Status)
	}
	return nil
}

// FormatTarget 提取指标 target 标签用于展示。
func FormatTarget(m core.Metric) string {
	if t, ok := m.Labels["target"]; ok {
		return t
	}
	var parts []string
	for k, v := range m.Labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}
