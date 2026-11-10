# Nexus — 统一 LLM 与算力服务平台

**商业化官网**：https://nexus-plum-eta.vercel.app

![License](https://img.shields.io/badge/license-MIT-2dd4bf)
![Deployed on Vercel](https://img.shields.io/badge/deployed%20on-Vercel-111?logo=vercel)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)

> 企业级、高性能、可水平扩展的统一 LLM 网关 + 自主 Agent 运行时。
> 以 Go 重写并升级自 [`deepseek-ai/dsh-superagent-prototype`](https://github.com/deepseek-ai/dsh-superagent-prototype)（TypeScript 研究原型）。

Nexus 用一套 **OpenAI 兼容 API** 统一接入异构模型供应商，对上提供单一入口，对下屏蔽差异，并继承 dsh-superagent 的双层 Router/Worker、事件驱动与插件化思想，为后续算力调度与自主 Agent 提供统一底座。

## 核心特性

- **统一接入**：通用 OpenAI 兼容适配器，可对接 OpenAI、DeepSeek、本地 vLLM/Ollama 等任意 OpenAI Chat 协议端点；请求体原样透传，不丢失供应商扩展参数。
- **高性能**：标准库 `net/http` + goroutine，零重依赖；SSE 流式边收边发，首 token 延迟接近直连。
- **负载均衡**：平滑加权轮询（SWRR）、最少连接、加权随机；主动 + 被动健康检查，故障节点自动摘除。
- **韧性治理**：令牌桶限流（Key/租户/全局三级）、三态熔断器、指数退避重试（仅对可安全重试的状态码，流式产出后不重试）。
- **多租户与计量**：API Key 鉴权、租户隔离、模型白名单、token 用量与成本记录。
- **可观测**：`slog` 结构化日志 + 自实现 Prometheus 指标（QPS、延迟、token、上游健康、熔断状态）。
- **可靠运行**：优雅关停、在途请求排空、panic 自动恢复。

## 快速开始

### 1. 构建

```bash
go build -o bin/nexus ./cmd/nexus
```

### 2. 配置

```bash
cp configs/config.example.yaml configs/config.yaml
# 编辑 config.yaml，配置上游地址与密钥；支持 ${ENV_VAR} 环境变量展开
export DEEPSEEK_API_KEY=sk-xxx
```

### 3. 运行

```bash
./bin/nexus -config configs/config.yaml
```

## 调用示例

携带平台 API Key（即 `config.yaml` 中 `auth.keys` 配置的密钥）：

```bash
# 非流式
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-nexus-demo-0001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "你好"}]
  }'

# 流式（SSE）
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-nexus-demo-0001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "stream": true,
    "messages": [{"role": "user", "content": "你好"}]
  }'

# 模型列表
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer sk-nexus-demo-0001"
```

## 测试与冒烟

```bash
go test ./...          # 单元测试
go test -race ./...    # 竞态检测
bash scripts/smoke.sh  # 端到端冒烟：启动 mock 上游，实际转发非流式/流式请求
```

## 端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/chat/completions` | OpenAI 兼容对话（含 SSE 流式） |
| GET | `/v1/models` | 已注册模型列表 |
| GET | `/healthz` | 存活探针 |
| GET | `/readyz` | 就绪探针（至少一个上游健康） |
| GET | `/metrics` | Prometheus 指标 |

## 官网与 Vercel 部署

商业化营销官网（产品介绍、定价、FAQ、等待名单）以纯静态站点托管在 Vercel，源码位于 [`web/`](web)；有状态的网关后端仍以 Docker/服务器部署。

- 官网地址：https://nexus-plum-eta.vercel.app
- 一键部署：

[![Deploy with Vercel](https://vercel.com/button)](https://vercel.com/new/clone?repository-url=https://github.com/gpteth/nexus&project-name=nexus&repository-name=nexus)

本地预览：

```bash
cd web && python3 -m http.server 8000
```

## Docker 部署

```bash
# 镜像构建（静态二进制 + distroless）
docker build -f deploy/Dockerfile -t nexus:latest .
docker run -p 8080:8080 \
  -e DEEPSEEK_API_KEY=sk-xxx \
  nexus:latest

# 或使用 docker-compose
cd deploy && DEEPSEEK_API_KEY=sk-xxx docker compose up
```

## 目录结构

```
nexus/
├── cmd/nexus/        # 入口
├── internal/
│   ├── config/       # 配置加载、默认值、校验
│   ├── logging/      # slog 结构化日志
│   ├── server/       # HTTP server、中间件、优雅关停
│   ├── gateway/      # 网关：provider/路由/负载均衡/熔断/重试/限流/handler
│   ├── sse/          # SSE 流式读写
│   ├── auth/         # API Key 鉴权
│   ├── store/        # 存储端口与内存实现
│   ├── metrics/      # Prometheus 指标
│   ├── id/           # 随机 ID
│   └── backoff/      # 指数退避
├── configs/          # 示例/冒烟配置
├── deploy/           # Dockerfile / docker-compose
├── scripts/          # 冒烟脚本与 mock 上游
└── docs/             # 架构文档
```

## 路线图

- **阶段一（已完成）**：工程骨架 + 统一 LLM 网关核心（路由、负载均衡、熔断、重试、限流、鉴权、计量、指标、Docker、端到端冒烟）。
- **阶段二（已完成）**：Agent 运行时 —— domain 模型、单一写入者引擎、Router/Worker 双层、事件总线、Effects/Outbox、工具插件协议（覆盖率 85.1%，`-race` 零竞态）。
- **阶段三（已完成）**：算力调度 —— 算力池、优先级调度、filter/score 放置、配额、抢占、弹性伸缩（覆盖率 87.1%，`-race` 零竞态）。
- **阶段四（进行中）**：生产化加固 —— 已完成安全加固（AES-256-GCM、JWT、RBAC，覆盖率 89.2%）与 SQLite 持久化（纯 Go 无 cgo，重启不丢数据，store 覆盖率 86.6%），均 `-race` 零竞态；待办管理控制台、OpenTelemetry 全链路追踪、高可用与压测。

详见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。

## 许可与致谢

设计思想源于 DeepSeek 团队开源的 dsh-superagent 项目（MIT）。本项目为独立的工程重写与扩展，不隶属于 DeepSeek。
