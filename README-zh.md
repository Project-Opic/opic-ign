# opic-ign

> English: [README.md](README.md)

[![License](https://img.shields.io/badge/License-MulanPSL--2.0-2E7D32)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=white)](frontend/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)](migrations)

[文档](#文档) | [架构](#架构) | [使用](#使用) | [部署](#部署) | [路线图](#路线图) | [许可证](#许可证)

**opic-ign 是 OPIC 开放智能算力平台的点火中心（O-IGN）**：交付执行域的能力中心，平台级 fork 自 [opic-techbase](https://github.com/Project-Opic/opic-techbase)，落地交付版本目录、镜像预推、执行机注册与引导、通道证书绑定、白名单执行任务与对账纠偏的完整链路，并通过交付门面向 O-CLC 等上游中心单步开放。

中心侧关注的是「软件如何安全地送到一批执行机上」：一切破坏性动作都必须经过动作码白名单，「动作码 → argv」是本域私有映射——不入模型、不出域，对上游只暴露动作码与结论；日志行在写入前即不可逆脱敏；凭据三要素不出现在任何查询响应里。

本仓库包含后端服务（Gin）、控制台前端（React + Ant Design）、业务迁移、本体模型（YAML）、部署编排与开发文档。

## 文档

| 文档 | 内容 |
|---|---|
| [新人培训入门](docs/ONBOARDING.md) | 背景 / 架构 / 环境 / 首次启动 / 开发流程 |
| [编译与部署指南](docs/DEPLOYMENT.md) | 构建、生产部署、迁移回滚、fork 清单 |
| [运维手册](docs/OPERATIONS.md) | 健康检查 / 监控告警 / 日志 / 备份 / 凭据 / 故障速查 |
| [FAQ](docs/FAQ.md) | 实战踩坑速查（SSO / SQL / 部署 / 前端 / AI） |
| [P3 真实例验收报告](docs/P3真实例验收报告-2026-10-07.html) | 真实例验收证据（2026-10-07） |
| [Web 测试报告](docs/Web测试报告-点火与集群中心-2026-10-06.html) | 控制台 Web 测试证据（2026-10-06） |

## 架构

opic-ign 由两个运行单元组成，共享同一套 `internal/service` 业务层：

- **`ignition-center`** —— 后端主服务。Gin 提供 `/api/v1/ign/*` REST 接口与 `/health`、`/metrics` 探针；启动时自动执行 goose 迁移、导入 M5 种子（角色 / 权限 / 菜单 / 账号 / 参数缺省 / LOCAL 执行机 / 门面服务账号）与 M6 审批流，全部幂等。
- **`frontend`** —— nginx 托管的 React SPA，含七大业务屏与审批中心页面。

请求链路：

- 浏览器 → Traefik（`ign.<domain>`）→ `/api`、`/health`、`/metrics` 转发到 `ignition-center`，其余路径到 `frontend`。
- 上游中心（如 O-CLC）→ `POST /api/v1/ign/delivery/*`（Bearer 门面令牌）→ 交付门面 → `internal/service`。

数据面依赖：PostgreSQL 16（业务库，中心 schema `ign`，5 个 goose 迁移共 11 张表）、Redis（令牌吊销黑名单，可选）、NATS JetStream（stream `OPIC_IGN`，主题域 `opic.ign.>`，可选）、Temporal（任务队列 `ign`，可选）。

所有业务接口使用统一的 `{"returnInfo":{"returnCode","errorMsg"},"data":...}` 信封，成功码 `SUC0000`。交付门面是刻意的例外：以 flat JSON `{return_code, error_msg}` + 非 200 状态应答（403 治理拒绝 / 400 形态与规则 / 409 状态·容量·唯一 / 404 引用未命中 / 其余 500），且请求体不含命令面与参数数组。

### 核心能力

| 能力 | 说明 |
|---|---|
| **版本目录** | 交付版本登记 / 修改 / 下载 / 归档 / 软删 / 列表 / 详情 / 跨中心视图（FUNC-01~09、28） |
| **镜像预推** | 预推明细、覆写、断点续推与明细列表（FUNC-10~12） |
| **执行机管理** | 执行机注册 / 令牌重签 / 注销 / 能力调整 / 列表 / 详情 / 跨中心视图（FUNC-13~19、23） |
| **通道证书** | TLS 绑定续期 / 降级 / 清单（FUNC-20~22） |
| **执行任务** | 任务提交（防重放）/ 取消 / 台账 / 详情 / 日志（FUNC-23~26） |
| **对账纠偏** | 统计总览 / 运行参数 / 对账纠偏台账（FUNC-27、29、30） |
| **强制注销审批** | 执行机强制注销单级审批（`FLOW-PROC-APP-01`，IGN-ROLE-02 审批，R-07 拆除链次序固定）（FUNC-31~33） |
| **交付门面** | 面向 O-CLC 等上游中心的受理 / 回执与增量日志 / 撤销，带来源任务反查留痕（FUNC-34~37） |
| **本机执行位内核** | 执行位即后端容器自身：fork + 逐行采集 stdout/stderr、游标单调的脱敏日志、请求标记取消、平台重启后在途任务显式判 `LOST` 第四终态 |

## 与其他工具的关系

opic-ign 是**能力中心**，不替代数据库、身份提供方或工作流引擎，而是在 opic-techbase 工程基线之上叠加交付执行域语义。

| 层次 | 相关工具 | 关系 |
|---|---|---|
| 工程基线 | [opic-techbase](https://github.com/Project-Opic/opic-techbase) | 平台级 fork：底座仅承载技术设施层，业务语义层归本中心——中心业务独立演进，底座安全修复单向回流 |
| 工程模式与视觉体系 | [gopherforge](https://github.com/SuperiorChuo/gopherforge)（MIT） | 经基线 fork 继承的技术选型与工程布局基准；控制台玻璃主题移植自其前端并保留来源注释 |
| 身份与认证 | ZITADEL | OIDC 身份提供方（Authorization Code + PKCE、JWKS RS256 验签、角色声明映射与自动 provisioning）；`AUTH_MODE=local` 保留本地回退（图形验证码登录） |
| 流程运行时 | Temporal | 任务队列 `ign`；强制注销审批跑在继承的审批引擎上；`flowGuardian` SLA 看护适用 |
| 事件总线 | NATS JetStream | 领域事件（`opic.ign.version.*` / `executor.*` / `prepush.*` / `task.*` / `tls.*` / `ops.*` / `delivery.accepted`）旁路发布，失败不阻塞主链路 |
| 数据与缓存 | PostgreSQL（Pigsty 供给）、Redis | PG 承载中心 schema `ign` 内的业务数据；Redis 仅用于令牌吊销，未启用时静默降级 |
| 上游中心 | O-CLC（集群能力中心） | 集群全生命周期动作经交付门面单步下发（Bearer 令牌，`source_task_no` + `source_step_index` 反查留痕）；O-AST 资产存在性核对、O-CRT 证书签发/吊销两处跨中心通道本期留位 |

## 仓库结构

| 组件 | 路径 | 说明 |
|---|---|---|
| **后端服务** | [`cmd/techbase/`](cmd/techbase) | 组合根（继承基线）：配置 → 日志 → PostgreSQL（goose）→ 种子 → Redis → Gin → 优雅退出 |
| **HTTP 层** | [`internal/api/`](internal/api) | `routes.go` 路由表（`/api/v1/ign/*`，37 个端点）、`business/ign.go` 中心控制器，以及门面与代理侧路由组 |
| **业务层** | [`internal/service/`](internal/service) | `ign_common.go` 编号与事件、`ign_rules.go` M3 规则等价实现、`ign_exec.go` 本机执行位内核、各聚合文件、`assistant.go` AI 工作区、`seed.go` M5 种子、工作台与流程适配 |
| **共享库** | [`pkg/`](pkg) | 错误码、日志、Redis、令牌、指标、健康检查、优雅退出、事件总线、工作流封装（继承基线） |
| **数据库迁移** | [`migrations/`](migrations) | goose 序号迁移：`000004_business_ign.sql`（9 表）与 `000005_ign_delivery.sql`（v1.1 交付列与索引） |
| **本体模型** | [`models/`](models) | 七模型 YAML（M1/M2/M3/M5/M6/M7/MU）+ `manifest.json`；M6 含可执行 `nodeGraph` |
| **前端** | [`frontend/`](frontend) | React 19 + Ant Design 6 + Redux Toolkit + Vite；`src/pages/ign/` 七大业务屏 + 审批中心页面 |
| **部署编排** | [`docker-compose.yml`](docker-compose.yml)、[`docker-compose.infra.yml`](docker-compose.infra.yml)、[`Dockerfile`](Dockerfile) | 应用栈与数据栈分离，`BASE_DOMAIN` 子域路由 |
| **文档** | [`docs/`](docs) | 入门、部署、运维、FAQ、验收与 Web 测试证据 |

## 基础软件依赖

版本以 `go.mod`、`frontend/package.json` 与 Dockerfile 为准；基础设施版本为生产部署实际使用的版本。

### 运行时与基础设施

| 组件 | 版本 | 上游仓库 |
|---|---|---|
| Go | 1.27 | <https://github.com/golang/go> |
| PostgreSQL | 16 | <https://github.com/postgres/postgres> |
| Redis | 8（可选） | <https://github.com/redis/redis> |
| NATS Server | 2.14（可选） | <https://github.com/nats-io/nats-server> |
| Temporal | 1.29（可选） | <https://github.com/temporalio/temporal> |
| Traefik | v3 | <https://github.com/traefik/traefik> |
| nginx | （前端托管） | <https://github.com/nginx/nginx> |
| Node.js | 20+（构建阶段） | <https://github.com/nodejs/node> |

### 后端（Go 模块）

| 模块 | 版本 | 上游仓库 |
|---|---|---|
| `github.com/gin-gonic/gin` | v1.12.0 | <https://github.com/gin-gonic/gin> |
| `gorm.io/gorm` | v1.31.0 | <https://github.com/go-gorm/gorm> |
| `gorm.io/driver/postgres` | v1.6.0 | <https://github.com/go-gorm/postgres> |
| `github.com/pressly/goose/v3` | v3.27.3 | <https://github.com/pressly/goose> |
| `github.com/golang-jwt/jwt/v5` | v5.3.0 | <https://github.com/golang-jwt/jwt> |
| `github.com/redis/go-redis/v9` | v9.22.0 | <https://github.com/redis/go-redis> |
| `github.com/nats-io/nats.go` | v1.54.0 | <https://github.com/nats-io/nats.go> |
| `go.temporal.io/sdk` | v1.49.0 | <https://github.com/temporalio/sdk-go> |
| `go.uber.org/zap` | v1.28.0 | <https://github.com/uber-go/zap> |
| `gopkg.in/natefinch/lumberjack.v2` | v2.2.1 | <https://github.com/natefinch/lumberjack> |
| `golang.org/x/crypto` | v0.57.0 | <https://github.com/golang/crypto> |
| `gopkg.in/yaml.v3` | v3.0.1 | <https://github.com/go-yaml/yaml> |

### 前端（npm）

| 包 | 版本 | 上游仓库 |
|---|---|---|
| `react` / `react-dom` | 19 | <https://github.com/facebook/react> |
| `antd` | 6 | <https://github.com/ant-design/ant-design> |
| `@ant-design/icons` | 6 | <https://github.com/ant-design/ant-design-icons> |
| `@reduxjs/toolkit` | 2.5 | <https://github.com/reduxjs/redux-toolkit> |
| `react-router-dom` | 7.2 | <https://github.com/remix-run/react-router> |
| `axios` | 1.8 | <https://github.com/axios/axios> |
| `@xyflow/react` | 12.3 | <https://github.com/xyflow/xyflow> |
| `vite` | 8 | <https://github.com/vitejs/vite> |
| `typescript` | 5.8 | <https://github.com/microsoft/TypeScript> |

以上第三方组件均使用宽松许可证（MIT / Apache-2.0 / BSD），具体条款以上游仓库为准。

## 使用

### 本地开发环境

前置：Go 1.27+、Node ≥ 20.12（推荐 nvm 20.19.5）、可达的 PostgreSQL 16（生产 192.168.3.200:5433）。

```bash
# 1) 建中心 schema（首次执行；应用启动也会跑 goose 迁移）
psql -c "CREATE SCHEMA IF NOT EXISTS ign AUTHORIZATION postgres;"   # 库 opicdb

# 2) 配置（口令只经环境变量注入，不写进命令行与仓库）
export CENTER_CODE=ign DB_SCHEMA=ign
export DB_HOST=192.168.3.200 DB_PORT=5433 DB_USER=postgres DB_NAME=opicdb
export REDIS_ENABLED=false
export EVENTBUS_DRIVER=nats EVENTBUS_URL="nats://192.168.3.200:14222"
export WORKFLOW_DRIVER=local
export APP_PORT=9709

# 3) 构建并启动（goose 迁移 + M5 种子 + M6 流程导入，幂等）
go build -o bin/ignition-center ./cmd/techbase
./bin/ignition-center          # → http://localhost:9709

# 4) 前端（开发模式）
make frontend-install && make dev-frontend   # http://localhost:5173, proxy → 9709
```

默认账号（`AUTH_MODE=local`，验证码登录，口令均为 `admin123`）：

| 账号 | 角色 | 权限概要 |
|---|---|---|
| `admin` | 系统管理员 | 超管位（代码层注入） |
| `ignadmin` | IGN-ROLE-01 交付管理员 | 全部业务写 + FUNC-31 提交 + FUNC-37 门面台账 |
| `opsadmin` | IGN-ROLE-02 平台运维管理员 | 降级声明 / 运行参数 / 审批 FUNC-32·33 + FUNC-37 |
| `auditor` | IGN-ROLE-03 交付审计员 | 全菜单只读 + 对账台账 + FUNC-37 |
| `ignviewer` | IGN-ROLE-04 交付只读用户 | 目录 / 台账只读 + FUNC-37 |

IGN-ROLE-05 为服务身份（执行机代理与上游中心门面身份），不建交互账号。

一键全栈（compose）：

```bash
cp .env.example .env
make compose-up           # 数据栈(--wait) → 应用栈: ign.opic-ai.ccoe.tech
make compose-down
```

### 冒烟路径

```bash
CENTER_CODE=ign DB_SCHEMA=ign go run ./cmd/smoke   # 基础设施探针（pg/redis/nats/temporal）
```

实测路径：登记交付版本（FUNC-01，`VER-YYYYMMDD-0001`）→ 注册执行机（FUNC-13，引导回连消费凭据至 READY）→ 提交任务（FUNC-23，防重放返回原任务）→ 执行位内核 fork `ezctl`、流式输出脱敏日志、判定网关写终态与结论依据 → 对账台账留痕。

### 单独构建

```bash
make build                 # 后端二进制 → bin/ignition-center
make frontend-build        # 前端产物 → frontend/dist

make test                  # 单元测试
make lint                  # go vet
make migrate-status        # 查看迁移状态
```

## 部署

生产部署通过 `make deploy-prod` 完成：rsync 源码到目标机 → 在目标机构建镜像 → 复制 compose 与配置 → `docker compose up -d`。迁移、种子与 M6 流程导入在容器启动时自动执行。

```bash
make deploy-prod           # 构建 + 部署 + 拉起
make deploy-prod-down      # 停止应用栈
```

关键环境变量（完整清单见 [部署指南](docs/DEPLOYMENT.md)）：

| 变量 | 说明 |
|---|---|
| `CENTER_CODE` / `DB_SCHEMA` | 中心域码与 schema（隔离三件套根） |
| `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` | PostgreSQL 连接（Pigsty haproxy） |
| `APP_PORT` | HTTP 端口（默认 9709） |
| `REDIS_ENABLED/REDIS_HOST/REDIS_PORT/REDIS_PASSWORD` | Redis（登出吊销黑名单） |
| `EVENTBUS_DRIVER/URL/USERNAME/PASSWORD` | 事件总线（`inproc` 或 `nats`） |
| `WORKFLOW_DRIVER/ADDRESS/NAMESPACE` | 工作流运行时（`local` 或 `temporal`，队列 `ign`） |
| `METRICS_ENABLED` / `JWT_SECRET` / `AUTH_MODE` | 可观测 / 会话密钥 / 认证模式 |
| `IGN_DELIVERY_TOOL_BIN` / `IGN_DELIVERY_TOOL_ROOT` / `IGN_DELIVERY_SSH_DIR` | 本机执行位：交付工具可执行文件、工作根目录与执行面凭据注入方式（未配置 ⇒ `LOCAL` 执行机不可登记，REF-16） |
| `IGN_DELIVERY_FACADE_TOKEN` | 远程交付门面令牌（未配置 ⇒ 门面三接口一律 401 关闭） |

### 分发渠道

镜像推送到内部 Harbor：后端 `192.168.3.200:18700/opic/ign`，前端 `192.168.3.200:18700/opic/ign-frontend`（`make harbor-push`）。生产子域 `ign.opic-ai.ccoe.tech`。

## 贡献

提交前请确认：

1. `go build ./...`、`go vet ./...` 通过，且 `cd frontend && npm run build` 通过；
2. 新增或修改接口时保持 `returnInfo` 信封与 OPIC O-ARC 错误码口径不变；
3. 涉及数据库变更时新增 goose 迁移文件（禁止 AutoMigrate），并保证 `Down` 可回滚；
4. 规则违反（`INV-*` / `R-*`）在服务层保留中文提示；口令 / 令牌 / 私钥在写入日志前完成不可逆脱敏。

分支与提交信息沿用 `feat|fix|docs|chore(scope): 说明` 格式。

提交贡献即表示同意以[木兰宽松许可证第 2 版](LICENSE)授权该贡献。

## 路线图

本仓库不维护长期功能路线图，计划与进展以下列方式公开：

- **已完成**（近期）：七模型入库（M1/M2/M3/M5/M6/M7/MU + 可执行 `nodeGraph`）；中心隔离（`ign` schema / stream / 主题域 / 任务队列）；v1.1 交付链路（步骤编码、来源反查、结论依据、脱敏任务日志）；flat JSON 语义的交付门面；四终态的本机执行位内核。
- **待办**（已知缺口）：O-AST 资产存在性核对与 O-CRT 证书签发/吊销两处跨中心通道尚未接入；REMOTE 代理对端本期留位不实现（Q-68）；真实挂载与上游联调在 3.200 待验。
- **限制**：动作码白名单是三层可达面（交付动作层 11 项面向人工与门面、`ezctl.new` 仅内部、`scripts.*` 仅平台自动行为）；平台重启后在途任务无法验真，显式判 `LOST`；门面响应刻意不套 `returnInfo`。

## 社区

- **问题与建议**：通过仓库 Issue 反馈，附复现步骤与环境信息（版本、迁移号、部署方式）。
- **内部沟通**：OPIC 平台团队；生产问题走运维值班渠道。

## 安全与支持

- **报告漏洞**：请勿公开提交 Issue，直接联系平台团队。安全基线包括门面令牌门禁（未配置即 401 关闭、不降级放行）、白名单外动作与命令拼接风险提交一律治理拒绝并审计、口令 / 令牌 / 私钥写前不可逆脱敏、只读 SQL 白名单刻意排除 `bootstrap_credential`（INV-22）。
- **支持范围**：当前主分支与生产部署版本。

## 许可证

Copyright © 2026 wjluo

本项目采用**木兰宽松许可证第 2 版（Mulan PSL v2）**，全文见 [`LICENSE`](LICENSE)，也可从 <http://license.coscl.org.cn/MulanPSL2> 获取。

在遵守许可证条款的前提下，你可以自由复制、使用、修改和分发本软件，包括商业使用；分发时需保留版权与许可声明。许可证第 3 条明确不授予商标许可，第 5 条按「现状」提供软件、不附带任何明示或默示担保。

### 第三方与移植代码

- **opic-techbase**（Mulan PSL v2）：本仓库为平台级 fork，技术设施层继承自基线，基线的许可证继续适用于该部分代码。
- **gopherforge**（MIT）：工程模式与技术选型基准；管理控制台视觉体系（玻璃主题、登录页、Dashboard 样式）移植自其前端，移植文件头部保留来源注释；其 MIT 许可声明继续适用于该部分代码。
