package hub

import (
	"context"
	"log"
	"time"

	"sentinel/internal/core"
	"sentinel/internal/scheduler"
)

// Prober 在 Hub 侧运行主动探测任务（无 Agent 采集）。
type Prober struct {
	sched   *scheduler.Scheduler
	store   *Store
	alerter *Alerter
}

// NewProber 创建探测调度器。
func NewProber(st *Store, al *Alerter) *Prober {
	p := &Prober{store: st, alerter: al}
	p.sched = scheduler.New(p.handle)
	return p
}

// Sync 按探测配置同步任务（配置热重载时复用）。
func (p *Prober) Sync(probes []core.ProbeTask) {
	tasks := make([]core.Task, 0, len(probes))
	for _, pb := range probes {
		tasks = append(tasks, core.Task{
			ID: "probe:" + pb.ID, Plugin: pb.Plugin,
			Interval: pb.Interval, Params: pb.Params, Rule: pb.Rule,
		})
	}
	p.sched.Sync(tasks)
}

// StopAll 停止全部探测。
func (p *Prober) StopAll() { p.sched.StopAll() }

func (p *Prober) handle(res scheduler.Result) {
	if err := p.store.InsertMetric("hub", res.Task.ID, res.Metrics, res.At); err != nil {
		log.Printf("prober: insert metrics: %v", err)
	}
	p.alerter.Evaluate("hub", res.Task, res.Metrics)
}

// PruneLoop 周期清理过期指标。
func PruneLoop(ctx context.Context, st *Store, keepDays int) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := st.Prune(ctx, keepDays); err != nil {
				log.Printf("hub: prune: %v", err)
			}
		}
	}
}
