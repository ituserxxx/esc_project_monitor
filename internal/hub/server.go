// Package hub 实现 Sentinel Hub：配置下发、指标接收、告警、主动探测与 Web 页面。
package hub

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"sentinel/internal/core"
)

//go:embed web/index.html
var webFS embed.FS

// Server 是 Hub 的 HTTP 服务。
type Server struct {
	cfg     *core.HubConfig
	store   *Store
	alerter *Alerter
	mux     *http.ServeMux
}

// NewServer 创建 HTTP 服务。
func NewServer(cfg *core.HubConfig, st *Store, al *Alerter) *Server {
	s := &Server{cfg: cfg, store: st, alerter: al, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 暴露 http.Handler。
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/plugins", s.handlePlugins)
	s.mux.HandleFunc("/api/agents", s.handleAgents)
	s.mux.HandleFunc("/api/metrics", s.handleLatestMetrics)
	s.mux.HandleFunc("/api/alerts", s.handleAlerts)

	// Agent 接口
	s.mux.HandleFunc("/api/v1/agent/config", s.handleAgentConfig)
	s.mux.HandleFunc("/api/v1/agent/report", s.handleAgentReport)

	// Push 上报（无 Agent 方式之一）
	s.mux.HandleFunc("/api/v1/push", s.handlePush)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := webFS.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, core.List())
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	states, err := s.store.AgentStates()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, states)
}

func (s *Server) handleLatestMetrics(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	pts, err := s.store.LatestMetrics(limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, pts)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	evts, err := s.store.RecentAlertEvents(limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, evts)
}

// ---- Agent 接口 ----

type configRequest struct {
	AgentID string `json:"agent_id"`
	Version string `json:"version"`
}

// handleAgentConfig 配置下发：版本一致返回 204，不一致返回完整配置。
func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	if req.AgentID == "" {
		http.Error(w, "agent_id required", 400)
		return
	}

	cfg, err := s.store.GetAgentConfig(req.AgentID)
	if err != nil {
		// 未注册的 Agent：返回空配置，Agent 端视为无任务
		cfg = core.AgentConfig{AgentID: req.AgentID, Tasks: []core.Task{}}
		cfg.Version = core.ComputeVersion(cfg)
	}
	_ = s.store.TouchAgent(req.AgentID, req.Version)

	if req.Version == cfg.Version {
		w.WriteHeader(http.StatusNoContent) // 无变化
		return
	}
	writeJSON(w, cfg)
}

type reportRequest struct {
	AgentID string `json:"agent_id"`
	Version string `json:"version"`
	Batches []struct {
		Task    core.Task     `json:"task"`
		Metrics []core.Metric `json:"metrics"`
		At      time.Time     `json:"at"`
	} `json:"batches"`
}

// handleAgentReport 接收 Agent 上报的指标，入库并评估告警。
func (s *Server) handleAgentReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req reportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	source := "agent:" + req.AgentID
	_ = s.store.TouchAgent(req.AgentID, req.Version)

	for _, b := range req.Batches {
		if err := s.store.InsertMetric(source, b.Task.ID, b.Metrics, b.At); err != nil {
			log.Printf("hub: insert metrics from %s: %v", req.AgentID, err)
			continue
		}
		s.alerter.Evaluate(source, b.Task, b.Metrics)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- Push 上报 ----

type pushRequest struct {
	Source  string        `json:"source"`
	TaskID  string        `json:"task_id"`
	Metrics []core.Metric `json:"metrics"`
}

// handlePush 接收第三方 Push 上报的指标。
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req pushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	source := req.Source
	if source == "" {
		source = "push"
	}
	if err := s.store.InsertMetric(source, req.TaskID, req.Metrics, time.Now()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
