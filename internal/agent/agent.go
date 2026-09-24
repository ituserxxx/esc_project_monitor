// Package agent 实现轻量采集 Agent：
// 定时携带当前版本号向 Hub 拉取配置，版本不一致时拉取全量并写入本地缓存，
// 调度器对比差异热生效；Hub 不可达时降级使用本地缓存运行。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sentinel/internal/core"
	"sentinel/internal/scheduler"
)

// Config 是 Agent 启动参数。
type Config struct {
	HubAddr     string // Hub 地址，如 http://127.0.0.1:8080
	AgentID     string
	CachePath   string // 本地配置缓存文件
	SyncSeconds int    // 配置拉取间隔
	ReportBatch int    // 上报攒批条数
}

// Agent 采集代理。
type Agent struct {
	cfg    Config
	sched  *scheduler.Scheduler
	client *http.Client

	mu      sync.Mutex
	version string
	pending []reportBatch
}

type reportBatch struct {
	Task    core.Task     `json:"task"`
	Metrics []core.Metric `json:"metrics"`
	At      time.Time     `json:"at"`
}

// New 创建 Agent。
func New(cfg Config) *Agent {
	if cfg.SyncSeconds <= 0 {
		cfg.SyncSeconds = 30
	}
	if cfg.ReportBatch <= 0 {
		cfg.ReportBatch = 20
	}
	a := &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
	a.sched = scheduler.New(a.onResult)
	return a
}

// Run 启动主循环（阻塞）。
func (a *Agent) Run(ctx context.Context) error {
	// 启动时先用本地缓存兜底
	if cfg, err := a.loadCache(); err == nil {
		a.version = cfg.Version
		a.sched.Sync(cfg.Tasks)
		log.Printf("agent: bootstrapped from cache (version %s, %d tasks)", cfg.Version, len(cfg.Tasks))
	} else {
		log.Printf("agent: no usable cache: %v", err)
	}

	syncTick := time.NewTicker(time.Duration(a.cfg.SyncSeconds) * time.Second)
	reportTick := time.NewTicker(10 * time.Second)
	defer syncTick.Stop()
	defer reportTick.Stop()

	a.pullConfig(ctx) // 启动立即拉一次

	for {
		select {
		case <-ctx.Done():
			a.sched.StopAll()
			return ctx.Err()
		case <-syncTick.C:
			a.pullConfig(ctx)
		case <-reportTick.C:
			a.flush(ctx)
		}
	}
}

// pullConfig 携带版本号请求配置；一致则无变化，不一致则更新并热生效。
func (a *Agent) pullConfig(ctx context.Context) {
	url := a.cfg.HubAddr + "/api/v1/agent/config"
	body, _ := json.Marshal(map[string]string{
		"agent_id": a.cfg.AgentID, "version": a.version,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("agent: hub unreachable, keep running on cache: %v", err)
		return // 降级：继续使用当前（缓存）配置
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return // 版本一致，无变化
	case http.StatusOK:
		var cfg core.AgentConfig
		if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
			log.Printf("agent: decode config: %v", err)
			return
		}
		oldVersion := a.version
		a.version = cfg.Version
		if err := a.saveCache(cfg); err != nil {
			log.Printf("agent: save cache: %v", err)
		}
		a.sched.Sync(cfg.Tasks)
		log.Printf("agent: config updated %s -> %s (%d tasks)", oldVersion, cfg.Version, len(cfg.Tasks))
	default:
		log.Printf("agent: config pull returned %s", resp.Status)
	}
}

// onResult 采集回调：攒批。
func (a *Agent) onResult(res scheduler.Result) {
	a.mu.Lock()
	a.pending = append(a.pending, reportBatch{Task: res.Task, Metrics: res.Metrics, At: res.At})
	full := len(a.pending) >= a.cfg.ReportBatch
	a.mu.Unlock()
	if full {
		a.flush(context.Background())
	}
}

// flush 上报攒批数据。
func (a *Agent) flush(ctx context.Context) {
	a.mu.Lock()
	batch := a.pending
	a.pending = nil
	version := a.version
	a.mu.Unlock()

	if len(batch) == 0 {
		return
	}
	payload := map[string]interface{}{
		"agent_id": a.cfg.AgentID, "version": version, "batches": batch,
	}
	body, _ := json.Marshal(payload)
	url := a.cfg.HubAddr + "/api/v1/agent/report"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("agent: report failed (%d batches dropped): %v", len(batch), err)
		return
	}
	resp.Body.Close()
}

// saveCache 把最新配置写入本地缓存。
func (a *Agent) saveCache(cfg core.AgentConfig) error {
	if a.cfg.CachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.cfg.CachePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.cfg.CachePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.cfg.CachePath)
}

// loadCache 读取本地缓存配置。
func (a *Agent) loadCache() (core.AgentConfig, error) {
	var cfg core.AgentConfig
	if a.cfg.CachePath == "" {
		return cfg, fmt.Errorf("no cache path configured")
	}
	b, err := os.ReadFile(a.cfg.CachePath)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Version 当前配置版本。
func (a *Agent) Version() string { return a.version }
