package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"sentinel/internal/core"
)

// Store 封装 SQLite 存储：指标、告警事件、Agent 配置。
type Store struct {
	db *sql.DB
	mu sync.Mutex // sqlite 串行写入
}

// AlertEvent 是一条告警/恢复事件。
type AlertEvent struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"` // agent:<id> 或 hub
	TaskID    string    `json:"task_id"`
	TaskName  string    `json:"task_name"`
	Rule      string    `json:"rule"`
	Value     float64   `json:"value"`
	Status    string    `json:"status"` // firing / resolved
	CreatedAt time.Time `json:"created_at"`
}

// MetricPoint 用于 Web 页展示。
type MetricPoint struct {
	Source  string    `json:"source"`
	TaskID  string    `json:"task_id"`
	Plugin  string    `json:"plugin"`
	Name    string    `json:"name"`
	Value   float64   `json:"value"`
	Up      bool      `json:"up"`
	Message string    `json:"message"`
	TS      time.Time `json:"ts"`
}

// OpenStore 打开（必要时创建）SQLite 库并建表。
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			task_id TEXT NOT NULL,
			plugin TEXT NOT NULL,
			name TEXT NOT NULL,
			value REAL NOT NULL,
			up INTEGER NOT NULL,
			message TEXT DEFAULT '',
			labels TEXT DEFAULT '',
			ts DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(ts);`,
		`CREATE TABLE IF NOT EXISTS alert_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			task_id TEXT NOT NULL,
			task_name TEXT DEFAULT '',
			rule TEXT DEFAULT '',
			value REAL DEFAULT 0,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS agent_configs (
			agent_id TEXT PRIMARY KEY,
			version TEXT NOT NULL,
			config TEXT NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS agent_state (
			agent_id TEXT PRIMARY KEY,
			last_seen DATETIME,
			version TEXT DEFAULT ''
		);`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// InsertMetric 写入一批指标。
func (s *Store) InsertMetric(source, taskID string, metrics []core.Metric, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO metrics(source,task_id,plugin,name,value,up,message,labels,ts)
		VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range metrics {
		lb, _ := json.Marshal(m.Labels)
		up := 0
		if m.Up {
			up = 1
		}
		if _, err := stmt.Exec(source, taskID, m.Plugin, m.Name, m.Value, up, m.Message, string(lb), ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestMetrics 返回最近 limit 条指标（Web 页用）。
func (s *Store) LatestMetrics(limit int) ([]MetricPoint, error) {
	rows, err := s.db.Query(`SELECT source,task_id,plugin,name,value,up,message,ts
		FROM metrics ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var p MetricPoint
		var up int
		if err := rows.Scan(&p.Source, &p.TaskID, &p.Plugin, &p.Name, &p.Value, &up, &p.Message, &p.TS); err != nil {
			return nil, err
		}
		p.Up = up == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// InsertAlertEvent 记录告警事件。
func (s *Store) InsertAlertEvent(e AlertEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO alert_events(source,task_id,task_name,rule,value,status,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		e.Source, e.TaskID, e.TaskName, e.Rule, e.Value, e.Status, e.CreatedAt)
	return err
}

// RecentAlertEvents 返回最近事件。
func (s *Store) RecentAlertEvents(limit int) ([]AlertEvent, error) {
	rows, err := s.db.Query(`SELECT id,source,task_id,task_name,rule,value,status,created_at
		FROM alert_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEvent
	for rows.Next() {
		var e AlertEvent
		if err := rows.Scan(&e.ID, &e.Source, &e.TaskID, &e.TaskName, &e.Rule, &e.Value, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertAgentConfig 保存 Agent 版本化配置。
func (s *Store) UpsertAgentConfig(cfg core.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agent_configs(agent_id,version,config,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET version=excluded.version, config=excluded.config, updated_at=excluded.updated_at`,
		cfg.AgentID, cfg.Version, string(b), time.Now())
	return err
}

// GetAgentConfig 读取 Agent 配置。
func (s *Store) GetAgentConfig(agentID string) (core.AgentConfig, error) {
	var cfg core.AgentConfig
	var raw string
	err := s.db.QueryRow(`SELECT config FROM agent_configs WHERE agent_id=?`, agentID).Scan(&raw)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}

// TouchAgent 记录 Agent 心跳。
func (s *Store) TouchAgent(agentID, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO agent_state(agent_id,last_seen,version) VALUES(?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET last_seen=excluded.last_seen, version=excluded.version`,
		agentID, time.Now(), version)
	return err
}

// AgentStates 返回全部 Agent 心跳状态。
func (s *Store) AgentStates() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`SELECT agent_id,last_seen,version FROM agent_state ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id, ver string
		var seen time.Time
		if err := rows.Scan(&id, &seen, &ver); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"agent_id": id, "last_seen": seen, "version": ver,
		})
	}
	return out, rows.Err()
}

// Prune 清理 n 天前的指标，防止 SQLite 无限膨胀。
func (s *Store) Prune(ctx context.Context, keepDays int) error {
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	_, err := s.db.ExecContext(ctx, `DELETE FROM metrics WHERE ts < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("prune metrics: %w", err)
	}
	return nil
}
