# Project Sentinel — 智能项目监控系统

## 项目概述

Project Sentinel 是一套低资源占用、易扩展、配置灵活、告警及时的智能项目监控系统。它采用 **Hub-Agent 架构**，以插件化采集、版本化配置下发和企业微信告警为核心能力，整体资源占用约 **60–110MB**，远低于 Prometheus + Grafana 等传统方案。

## 整体架构

### Hub（集中管控端）

Hub 是系统的中枢，负责：

- 集中管理所有监控配置
- 接收 Agent 上报的监控指标
- 执行告警规则判断与通知发送
- 支撑轻量 Web 前端，提供可视化与配置管理入口

### Agent（可选采集端）

Agent 为可选组件，系统**优先采用无 Agent 采集方式**：

- HTTP / TCP / Ping 主动探测
- SSH 轮询
- SNMP
- 云监控 API
- Push 主动上报

仅在需要采集 **CPU、内存、磁盘等主机内部指标** 时，才安装轻量 Go Agent，资源目标：

- 内存占用 **< 10MB**
- CPU 占用 **< 1%**

## 配置下发机制

配置下发采用 **Agent 主动拉取（Pull）模式**：

1. Hub 为每个 Agent 保存**版本化配置**。
2. Agent 定时携带当前版本号向 Hub 发起请求：
   - **版本一致**：仅返回"无变化"，避免无效传输；
   - **版本不一致**：拉取完整配置并写入本地缓存。
3. Agent 内置调度器对比新旧配置差异，动态执行：
   - 停止、重启或新增采集插件
   - 调整采集频率与参数
   - **全程不重启 Agent 进程**，实现配置热生效
4. **降级容错**：Hub 不可达时，Agent 自动降级使用本地缓存继续运行，保障监控不中断。

## 插件化采集体系

采集能力完全插件化，每个监控项均为**独立插件**，例如：

- CPU、内存、磁盘
- TCP 检测、HTTP 检测
- 进程存活检测

所有插件通过**统一接口 + 全局注册表**管理。**新增监控项只需两步**：编写插件并注册 → 在 Hub 配置中启用，无需修改 Agent 核心代码。

## 告警机制

告警通过**企业微信机器人 Webhook** 发送，消息采用 **Markdown 格式**，支持：

- 阈值触发
- 持续时间判断（防抖动）
- 恢复条件与恢复通知
- 告警去重
- 告警静默
- 维护窗口

## 技术选型

| 组件 | 技术 |
| --- | --- |
| Agent / Hub | Go |
| Web 前端 | Svelte（轻量） |
| 存储（起步） | SQLite |
| 存储（规模化） | VictoriaMetrics（可平滑迁移） |

## 实施路线

### 第一阶段：MVP

- 主动探测（HTTP / TCP / Ping）
- 基础 Agent（CPU / 内存 / 磁盘采集）
- 配置拉取 + 版本化 + 本地缓存
- 企业微信告警

### 第二阶段：能力完善

- 插件配置热生效
- 更多采集插件
- Svelte 轻量前端
- SSH 轮询
- Push 上报 API

### 第三阶段：规模化扩展

- VictoriaMetrics 存储迁移
- WebSocket 实时配置通知
- 动态插件加载
- Agent 分组批量配置
- 灰度发布

## 设计目标

> 低资源占用 · 易扩展 · 配置灵活 · 告警及时

---

## 代码实现（当前进度：第一阶段 MVP）

本仓库已实现 MVP + 部分第二阶段能力：

- ✅ Hub：配置下发（版本化）、指标接收、SQLite 存储、企业微信告警（阈值/持续时间/恢复/去重/静默/维护窗口）
- ✅ Hub 主动探测：HTTP / TCP / Ping（复用插件体系）
- ✅ Agent：配置拉取、版本对比、本地缓存降级、调度器热生效（差异停止/重启/新增，不重启进程）
- ✅ 插件：cpu / mem / disk / proc / http / tcp / ping（统一接口 + 全局注册表，**cpu/mem/disk/proc 为 Linux-only，基于 /proc 与 statfs**）
- ✅ Push 上报 API：`POST /api/v1/push`
- ✅ 内置轻量 Web 页（原生 HTML 内嵌，Svelte 前端在第二阶段替换）

> **当前部署形态：以 Agent 采集为主，Agent 仅支持 Linux/Ubuntu。** 被监控主机（Linux/Ubuntu）部署轻量 Agent（内存 <10MB、CPU <1%），采集 CPU/内存/磁盘/进程等内部指标上报 Hub；Hub 侧 HTTP/TCP/Ping 主动探测与 Push API 作为补充手段保留，可按需在 `configs/hub.json` 的 `probes` 中配置。Hub 本身可编译运行于 Linux 与 Windows（开发调试用）。

### 目录结构

```
cmd/hub/             Hub 入口
cmd/agent/           Agent 入口
internal/core/       指标结构、插件接口与注册表、配置结构与版本哈希
internal/scheduler/  通用调度器（配置差异热生效，Agent/Hub 探测共用）
internal/plugins/    采集插件（sys: cpu/mem/disk/proc，Linux-only；probe: http/tcp/ping，跨平台）
internal/agent/      Agent：配置拉取 / 版本对比 / 本地缓存降级 / 攒批上报
internal/hub/        Hub：HTTP API / SQLite / 告警器 / 探测调度 / Web 页
configs/             示例配置
```

### 新增一个监控插件

```go
package myplugin

import (
    "context"
    "sentinel/internal/core"
)

type p struct{}

func init() { core.Register("myitem", p{}) } // 注册

func (p) Collect(ctx context.Context, t core.Task) ([]core.Metric, error) {
    return []core.Metric{{Name: "my_metric", Value: 1, Up: true}}, nil
}
```

然后在 `cmd/agent`（或 `cmd/hub`）中 import 该包，并在 Hub 配置中启用对应任务即可，无需修改核心代码。

### API 一览

| 接口 | 说明 |
| --- | --- |
| `POST /api/v1/agent/config` | Agent 配置拉取（版本一致返回 204） |
| `POST /api/v1/agent/report` | Agent 指标上报 |
| `POST /api/v1/push` | 第三方 Push 上报 |
| `GET /api/agents` `/api/metrics` `/api/alerts` `/api/plugins` | Web 页数据接口 |

### 在 WSL 中构建与运行

```bash
# 1. 安装 Go 1.22+（如未安装）
sudo apt update && sudo apt install -y golang-go    # 或安装官方 tarball

# 2. 拉取依赖（首次）
cd /mnt/d/DDD/xxx/code/esc_project_monitor
go mod tidy

# 3. 编译（Hub 与 Agent 均为 Linux 二进制；Agent 仅支持 Linux）
mkdir -p bin
go build -o bin/hub ./cmd/hub
go build -o bin/agent ./cmd/agent

# 4. 准备配置
cp configs/hub.example.json configs/hub.json
# 编辑 configs/hub.json：填入企业微信 webhook key，
# 并在 agents 段为每台被监控主机配置采集任务（cpu/mem/disk/proc）

# 5. 启动 Hub（前台）
./bin/hub -config configs/hub.json
# 浏览器打开 http://127.0.0.1:8080

# 6. 在每台被监控的 Linux/Ubuntu 主机启动 Agent（ID 需与配置中的 agent_id 对应）
./bin/agent -id node-1 -hub http://<hub-ip>:8080

# 7.（可选）自测 Push 上报
curl -X POST http://127.0.0.1:8080/api/v1/push \
  -H 'Content-Type: application/json' \
  -d '{"source":"test","task_id":"demo","metrics":[{"name":"demo_value","value":42,"up":true}]}'
```

**Agent 采集典型配置**（`configs/hub.json` 的 `agents` 段，每台主机一组任务）：

```json
{
  "agent_id": "node-1",
  "tasks": [
    {"id": "cpu",        "plugin": "cpu",  "interval": 30, "params": {},
     "rule": {"metric": "cpu_usage_percent",  "op": ">", "threshold": 90, "duration": 120}},
    {"id": "mem",        "plugin": "mem",  "interval": 30, "params": {},
     "rule": {"metric": "mem_usage_percent",  "op": ">", "threshold": 90, "duration": 120}},
    {"id": "disk-root",  "plugin": "disk", "interval": 60, "params": {"path": "/"},
     "rule": {"metric": "disk_usage_percent", "op": ">", "threshold": 85}},
    {"id": "nginx-alive","plugin": "proc", "interval": 30, "params": {"name": "nginx"},
     "rule": {"metric": "proc_alive", "op": "<", "threshold": 1, "duration": 60}}
  ]
}
```

验证配置热生效：修改 `configs/hub.json` 中某个 Agent 的任务后重启 Hub（版本重新计算入库），Agent 将在下一个同步周期内自动拉取并热加载新任务，进程不重启。Hub 短暂不可达时 Agent 自动降级使用本地缓存继续采集。

### 依赖说明

仅一个外部依赖：`modernc.org/sqlite`（纯 Go SQLite，无需 CGO，跨平台编译零障碍）。Agent 侧主机指标采集仅使用标准库（/proc、statfs），无任何第三方依赖。
