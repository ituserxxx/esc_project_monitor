// Package scheduler 提供通用的采集任务调度器：
// 对比新旧任务集差异，动态停止 / 重启 / 新增插件任务，
// 全程不重启宿主进程。Agent 与 Hub 探测共用。
package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"sentinel/internal/core"
)

// Result 是上报给结果处理器的单次采集输出。
type Result struct {
	Task    core.Task
	Metrics []core.Metric
	Err     error
	At      time.Time
}

// Handler 处理一次采集结果（上报或本地告警判断）。
type Handler func(Result)

type runner struct {
	task   core.Task
	cancel context.CancelFunc
}

// Scheduler 管理一组周期任务。
type Scheduler struct {
	mu      sync.Mutex
	runners map[string]*runner
	handler Handler
}

// New 创建调度器。
func New(h Handler) *Scheduler {
	return &Scheduler{runners: map[string]*runner{}, handler: h}
}

func taskKey(t core.Task) string {
	b, _ := json.Marshal(t)
	return string(b)
}

// Sync 对比新旧配置差异：新增的启动、变化的先停后起、消失的停止。
func (s *Scheduler) Sync(tasks []core.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := map[string]core.Task{}
	for _, t := range tasks {
		want[t.ID] = t
	}

	// 停止已消失或已变化的任务
	for id, r := range s.runners {
		nt, ok := want[id]
		if !ok {
			log.Printf("scheduler: stop removed task %s", id)
			r.cancel()
			delete(s.runners, id)
			continue
		}
		if taskKey(r.task) != taskKey(nt) {
			log.Printf("scheduler: restart changed task %s (%s)", id, nt.Plugin)
			r.cancel()
			delete(s.runners, id)
			s.startLocked(nt)
		}
	}

	// 启动新增任务
	for id, t := range want {
		if _, ok := s.runners[id]; !ok {
			log.Printf("scheduler: start new task %s (%s)", id, t.Plugin)
			s.startLocked(t)
		}
	}
}

// StopAll 停止全部任务。
func (s *Scheduler) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.runners {
		r.cancel()
		delete(s.runners, id)
	}
}

// Count 返回运行中任务数。
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runners)
}

func (s *Scheduler) startLocked(t core.Task) {
	p, ok := core.Get(t.Plugin)
	if !ok {
		log.Printf("scheduler: unknown plugin %q for task %s, skipped", t.Plugin, t.ID)
		return
	}
	if t.Interval <= 0 {
		t.Interval = 60
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runners[t.ID] = &runner{task: t, cancel: cancel}

	go func() {
		// 启动后立即采一次，再按间隔循环
		s.collect(ctx, p, t)
		tick := time.NewTicker(time.Duration(t.Interval) * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.collect(ctx, p, t)
			}
		}
	}()
}

func (s *Scheduler) collect(ctx context.Context, p core.Plugin, t core.Task) {
	// 单次采集超时 = min(interval, 30s)
	to := time.Duration(t.Interval) * time.Second
	if to > 30*time.Second {
		to = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	metrics, err := p.Collect(cctx, t)
	if err != nil {
		metrics = []core.Metric{{
			Plugin: t.Plugin, Name: "up", Value: 0, Up: false, Message: err.Error(),
		}}
	}
	for i := range metrics {
		metrics[i].Plugin = t.Plugin
	}
	s.handler(Result{Task: t, Metrics: metrics, Err: err, At: time.Now()})
}
