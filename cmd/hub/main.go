// Sentinel Hub：集中配置管理 / 指标接收 / 告警 / 主动探测 / Web 页面。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sentinel/internal/core"
	"sentinel/internal/hub"
	_ "sentinel/internal/plugins/probe"
	// 注意：主机内部指标插件（internal/plugins/sys）仅注册进 Agent（Linux-only），
	// Hub 只做主动探测（http/tcp/ping），不引用 sys 插件。
)

func main() {
	confPath := flag.String("config", "configs/hub.json", "Hub 配置文件路径")
	flag.Parse()

	cfg, err := loadConfig(*confPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	st, err := hub.OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 版本化 Agent 配置入库
	for _, ac := range cfg.Agents {
		ac.Version = core.ComputeVersion(ac)
		if err := st.UpsertAgentConfig(ac); err != nil {
			log.Fatalf("save agent config %s: %v", ac.AgentID, err)
		}
		log.Printf("hub: agent %s config version %s (%d tasks)", ac.AgentID, ac.Version, len(ac.Tasks))
	}

	alerter := hub.NewAlerter(cfg.WeComWebhook, cfg.DedupWindow, cfg.Silences, st)

	// 主动探测
	prober := hub.NewProber(st, alerter)
	prober.Sync(cfg.Probes)
	log.Printf("hub: %d probe tasks scheduled, plugins: %v", len(cfg.Probes), core.List())

	// HTTP 服务
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      hub.NewServer(cfg, st, alerter).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go hub.PruneLoop(ctx, st, 30)

	go func() {
		log.Printf("hub: listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("hub: shutting down")
	prober.StopAll()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

func loadConfig(path string) (*core.HubConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg core.HubConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
