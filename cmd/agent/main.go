//go:build linux

// Sentinel Agent：轻量采集代理（目标内存 <10MB、CPU <1%）。
// 仅支持 Linux/Ubuntu（主机指标插件依赖 /proc 与 statfs）。
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"sentinel/internal/agent"
	_ "sentinel/internal/plugins/probe"
	_ "sentinel/internal/plugins/sys"
)

func main() {
	hubAddr := flag.String("hub", "http://127.0.0.1:8080", "Hub 地址")
	id := flag.String("id", "", "Agent ID（必填）")
	cache := flag.String("cache", "data/agent-cache.json", "本地配置缓存路径")
	syncSec := flag.Int("sync", 30, "配置拉取间隔（秒）")
	flag.Parse()

	if *id == "" {
		log.Fatal("agent: -id is required")
	}

	a := agent.New(agent.Config{
		HubAddr:     *hubAddr,
		AgentID:     *id,
		CachePath:   *cache,
		SyncSeconds: *syncSec,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("agent: id=%s hub=%s sync=%ds", *id, *hubAddr, *syncSec)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("agent exited: %v", err)
	}
	log.Println("agent: bye")
}
