# opic-ign

> 简体中文: [README-zh.md](README-zh.md)

[![License](https://img.shields.io/badge/License-MulanPSL--2.0-2E7D32)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=white)](frontend/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)](migrations)

[Documentation](#documentation) | [Architecture](#architecture) | [Usage](#usage) | [Deployment](#deployment) | [Roadmap](#roadmap) | [License](#license)

**opic-ign is the Ignition Center of the OPIC open intelligent compute platform**: a capability center for the delivery-execution domain, forked from [opic-techbase](https://github.com/Project-Opic/opic-techbase). It runs the full delivery lifecycle — release catalogs, image pre-push, executor registration and bootstrap, channel certificate binding, whitelisted execution tasks, and reconciliation — and exposes it all to upstream centers through a delivery facade.

The center answers a recurring delivery question: how do you ship software to a fleet of execution machines without letting command surfaces leak? Every destructive action passes through a whitelist of action codes; the "action code → argv" mapping is a domain-private concern that never enters the ontology models or leaves the center. Logs are masked before they are written; credentials never appear in any query response.

This repository contains the backend service (Gin), the console frontend (React + Ant Design), business migrations, ontology models (YAML), deployment manifests, and developer documentation.

## Documentation

| Document | Contents |
|---|---|
| [Onboarding](docs/ONBOARDING.md) | Background, architecture, environment, first run, development workflow |
| [Build & Deployment](docs/DEPLOYMENT.md) | Build, production rollout, migration rollback, fork checklist |
| [Operations](docs/OPERATIONS.md) | Health checks, monitoring and alerting, logging, backup, credentials, troubleshooting |
| [FAQ](docs/FAQ.md) | Field notes on SSO, SQL, deployment, frontend, and AI |
| [Acceptance Report](docs/P3真实例验收报告-2026-10-07.html) | Real-instance acceptance evidence (2026-10-07) |
| [Web Test Report](docs/Web测试报告-点火与集群中心-2026-10-06.html) | Console web test evidence (2026-10-06) |

## Architecture

opic-ign runs as two units that share a single `internal/service` business layer:

- **`ignition-center`** — the primary backend. Gin serves the `/api/v1/ign/*` REST surface plus `/health` and `/metrics` probes. On startup it runs goose migrations, imports M5 seeds (roles / permissions / menus / accounts / parameter defaults / the LOCAL executor / the facade service account) and the M6 approval flow, all idempotently.
- **`frontend`** — a React SPA served by nginx, covering the seven business screens plus the approval center.

Request paths:

- Browser → Traefik (`ign.<domain>`) → `/api`, `/health`, `/metrics` to `ignition-center`; everything else to `frontend`.
- Upstream center (e.g. O-CLC) → `POST /api/v1/ign/delivery/*` (Bearer facade token) → delivery facade → `internal/service`.

Data plane: PostgreSQL 16 (business database, center schema `ign`, 11 tables across 5 goose migrations), Redis (token revocation blacklist, optional), NATS JetStream (stream `OPIC_IGN`, subject domain `opic.ign.>`, optional), and Temporal (task queue `ign`, optional).

Every business response uses a uniform envelope — `{"returnInfo":{"returnCode","errorMsg"},"data":...}` with `SUC0000` on success. The delivery facade is a deliberate exception: it answers with a flat `{return_code, error_msg}` body plus a non-200 status (403 governance refusal / 400 form and rule violations / 409 state, capacity, uniqueness / 404 reference miss / 500 otherwise), and never carries the command surface in its request body.

### Core Capabilities

| Capability | What it does |
|---|---|
| **Release catalog** | Delivery version registration, modification, download, archiving, soft delete, list, detail, and cross-center view (FUNC-01~09, 28) |
| **Image pre-push** | Pre-push items with overwrite and breakpoint resume, plus detail listing (FUNC-10~12) |
| **Executor management** | Executor registration, token re-signing, deregistration, capability adjustment, list, detail, cross-center view (FUNC-13~19, 23) |
| **Channel certificates** | TLS binding renewal, degradation, and inventory (FUNC-20~22) |
| **Execution tasks** | Task submission with idempotent replay protection, cancellation, ledger, detail, and logs (FUNC-23~26) |
| **Reconciliation** | Statistics overview, runtime parameters, reconciliation and correction ledger (FUNC-27, 29, 30) |
| **Force-removal approval** | Single-level approval for executor force removal (`FLOW-PROC-APP-01`, IGN-ROLE-02), with the fixed R-07 teardown chain (FUNC-31~33) |
| **Delivery facade** | Submit / receipt with incremental logs / cancel for upstream centers such as O-CLC, with source task traceability (FUNC-34~37) |
| **Local execution kernel** | The backend container itself is the execution seat: fork with line-by-line stdout/stderr capture, cursor-monotonic masked logs, cancellation via request marker, and a fourth terminal state `LOST` for in-flight tasks after a platform restart |

## How opic-ign Relates to Other Tools

opic-ign is a **capability center**, not a replacement for a database, an identity provider, or a workflow engine. It inherits the opic-techbase engineering baseline and layers the delivery-execution domain on top.

| Layer | Tools | Relationship |
|---|---|---|
| Engineering baseline | [opic-techbase](https://github.com/Project-Opic/opic-techbase) | Platform-level fork; the baseline carries the technology layer only, while business semantics belong to this center — the center evolves independently and security fixes flow back one-way |
| Engineering model and visual system | [gopherforge](https://github.com/SuperiorChuo/gopherforge) (MIT) | Technology selection and repository layout baseline (inherited via the baseline fork); the console's glass theme is ported from its frontend with source headers preserved |
| Identity and authentication | ZITADEL | OIDC provider (Authorization Code + PKCE, JWKS RS256 verification, role-claim mapping and auto-provisioning); `AUTH_MODE=local` keeps a local fallback with graphical captcha |
| Workflow runtime | Temporal | Task queue `ign`; the force-removal approval flow runs on the inherited approval engine; `flowGuardian` SLA watchdog applies |
| Event bus | NATS JetStream | Domain events (`opic.ign.version.*`, `executor.*`, `prepush.*`, `task.*`, `tls.*`, `ops.*`, `delivery.accepted`) published side-channel; failures never block the main path |
| Data and cache | PostgreSQL (provisioned by Pigsty), Redis | PostgreSQL holds business data in the center schema `ign`; Redis is used only for token revocation and degrades silently when disabled |
| Upstream centers | O-CLC (cluster center) | Cluster lifecycle actions are delivered step-by-step through the delivery facade (Bearer token, `source_task_no` + `source_step_index` traceability); O-AST asset checks and O-CRT certificate channels are reserved for later phases |

## Repository Map

| Component | Path | Description |
|---|---|---|
| **Backend service** | [`cmd/techbase/`](cmd/techbase) | Composition root inherited from the baseline: config → logging → PostgreSQL (goose) → seeds → Redis → Gin → graceful shutdown |
| **HTTP layer** | [`internal/api/`](internal/api) | `routes.go` route table (`/api/v1/ign/*`, 37 endpoints), `business/ign.go` center controller, plus the facade and agent-side route groups |
| **Business layer** | [`internal/service/`](internal/service) | `ign_common.go` numbering and events, `ign_rules.go` M3 rule equivalents, `ign_exec.go` local execution kernel, aggregate files (`ign_cluster`-style split per aggregate), `assistant.go` AI workspace, `seed.go` M5 seeds, workbench and flow adapters |
| **Shared libraries** | [`pkg/`](pkg) | Error codes, logging, Redis, tokens, metrics, health checks, graceful shutdown, event bus, workflow wrappers (inherited from the baseline) |
| **Migrations** | [`migrations/`](migrations) | Sequential goose migrations: `000004_business_ign.sql` (9 tables) and `000005_ign_delivery.sql` (v1.1 delivery columns and indexes) |
| **Ontology models** | [`models/`](models) | Seven-model YAML (M1/M2/M3/M5/M6/M7/MU) with `manifest.json`; M6 carries an executable `nodeGraph` |
| **Frontend** | [`frontend/`](frontend) | React 19 + Ant Design 6 + Redux Toolkit + Vite; seven business screens under `src/pages/ign/` plus approval-center pages |
| **Deployment** | [`docker-compose.yml`](docker-compose.yml), [`docker-compose.infra.yml`](docker-compose.infra.yml), [`Dockerfile`](Dockerfile) | Separate application stack and data stack, with `BASE_DOMAIN` subdomain routing |
| **Documentation** | [`docs/`](docs) | Onboarding, deployment, operations, FAQ, acceptance and web-test evidence |

## Base Software

Versions are pinned in `go.mod`, `frontend/package.json`, and the Dockerfiles. Infrastructure versions are those used by the production deployment.

### Runtime and infrastructure

| Component | Version | Upstream repository |
|---|---|---|
| Go | 1.27 | <https://github.com/golang/go> |
| PostgreSQL | 16 | <https://github.com/postgres/postgres> |
| Redis | 8 (optional) | <https://github.com/redis/redis> |
| NATS Server | 2.14 (optional) | <https://github.com/nats-io/nats-server> |
| Temporal | 1.29 (optional) | <https://github.com/temporalio/temporal> |
| Traefik | v3 | <https://github.com/traefik/traefik> |
| nginx | (frontend serving) | <https://github.com/nginx/nginx> |
| Node.js | 20+ (build stage) | <https://github.com/nodejs/node> |

### Backend (Go modules)

| Module | Version | Upstream repository |
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

### Frontend (npm)

| Package | Version | Upstream repository |
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

All third-party components above ship under permissive licenses (MIT / Apache-2.0 / BSD). Refer to each upstream repository for the exact terms.

## Usage

### Local Development Environment

Prerequisites: Go 1.27+, Node 20.12+ (nvm 20.19.5 recommended), and a reachable PostgreSQL 16 instance (production Pigsty HAProxy at `192.168.3.200:5433`).

```bash
# 1) Create the center schema (the application also runs goose migrations on startup)
psql -c "CREATE SCHEMA IF NOT EXISTS ign AUTHORIZATION postgres;"   # database opicdb

# 2) Configure (passwords are injected via environment variables only, never in the repo)
export CENTER_CODE=ign DB_SCHEMA=ign
export DB_HOST=192.168.3.200 DB_PORT=5433 DB_USER=postgres DB_NAME=opicdb
export REDIS_ENABLED=false
export EVENTBUS_DRIVER=nats EVENTBUS_URL="nats://192.168.3.200:14222"
export WORKFLOW_DRIVER=local
export APP_PORT=9709

# 3) Build and start (goose migrations + M5 seeds + M6 flow import, idempotent)
go build -o bin/ignition-center ./cmd/techbase
./bin/ignition-center          # → http://localhost:9709

# 4) Frontend (development mode)
make frontend-install && make dev-frontend   # http://localhost:5173, proxy → 9709
```

Default accounts (`AUTH_MODE=local`, captcha login, password `admin123`):

| Account | Role | Permission summary |
|---|---|---|
| `admin` | System administrator | Super-admin position (injected at code level) |
| `ignadmin` | IGN-ROLE-01 Delivery administrator | All business writes plus FUNC-31 submission and FUNC-37 facade ledger |
| `opsadmin` | IGN-ROLE-02 Platform ops administrator | Degradation declaration, runtime parameters, approvals FUNC-32/33, FUNC-37 |
| `auditor` | IGN-ROLE-03 Delivery auditor | Full-menu read-only plus reconciliation ledger and FUNC-37 |
| `ignviewer` | IGN-ROLE-04 Delivery read-only user | Catalog and ledger read-only plus FUNC-37 |

IGN-ROLE-05 is a service identity (executor agent and upstream-center facade) and has no interactive account.

One-command full stack (compose):

```bash
cp .env.example .env
make compose-up           # data stack (--wait) → app stack: ign.opic-ai.ccoe.tech
make compose-down
```

### Smoke Path

```bash
CENTER_CODE=ign DB_SCHEMA=ign go run ./cmd/smoke   # infrastructure probes (pg/redis/nats/temporal)
```

Verified path: register a delivery version (FUNC-01, `VER-YYYYMMDD-0001`) → register an executor (FUNC-13, bootstrap reconnection consumes the credential to READY) → submit a task (FUNC-23, replay protection returns the original task) → the execution kernel forks `ezctl`, streams masked logs, and the verdict gateway writes the terminal state with its evidence source → the reconciliation ledger records the outcome.

### Building Individual Components

```bash
make build                 # backend binary → bin/ignition-center
make frontend-build        # frontend assets → frontend/dist

make test                  # unit tests
make lint                  # go vet
make migrate-status        # migration status
```

## Deployment

Production deployment runs through `make deploy-prod`: it rsyncs the source to the target host, builds the images there, copies compose and config files, then runs `docker compose up -d`. Migrations, seeds, and the M6 flow import execute automatically when the container starts.

```bash
make deploy-prod           # build, deploy, start
make deploy-prod-down      # stop the application stack
```

Key environment variables (see the [Deployment Guide](docs/DEPLOYMENT.md) for the full list):

| Variable | Description |
|---|---|
| `CENTER_CODE` / `DB_SCHEMA` | Center domain code and schema (isolation root) |
| `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` | PostgreSQL connection (Pigsty HAProxy) |
| `APP_PORT` | HTTP port (default 9709) |
| `REDIS_ENABLED/REDIS_HOST/REDIS_PORT/REDIS_PASSWORD` | Redis (logout revocation blacklist) |
| `EVENTBUS_DRIVER/URL/USERNAME/PASSWORD` | Event bus (`inproc` or `nats`) |
| `WORKFLOW_DRIVER/ADDRESS/NAMESPACE` | Workflow runtime (`local` or `temporal`, queue `ign`) |
| `METRICS_ENABLED` / `JWT_SECRET` / `AUTH_MODE` | Observability / session secret / auth mode |
| `IGN_DELIVERY_TOOL_BIN` / `IGN_DELIVERY_TOOL_ROOT` / `IGN_DELIVERY_SSH_DIR` | Local execution seat: delivery tool binary, working root, and execution-plane credential injection (unset ⇒ `LOCAL` executors cannot be registered, REF-16) |
| `IGN_DELIVERY_FACADE_TOKEN` | Remote delivery facade token (unset ⇒ the three facade endpoints stay closed with 401) |

### Distribution Channels

Images are pushed to the internal Harbor registry: backend `192.168.3.200:18700/opic/ign`, frontend `192.168.3.200:18700/opic/ign-frontend` (`make harbor-push`). The production subdomain is `ign.opic-ai.ccoe.tech`.

## Contributing

Before opening a pull request, please confirm:

1. `go build ./...`, `go vet ./...`, and `cd frontend && npm run build` all pass.
2. Any added or changed endpoint keeps the `returnInfo` envelope and OPIC O-ARC error codes intact.
3. Schema changes ship as a new goose migration (never `AutoMigrate`), with a working `Down`.
4. Rule violations (`INV-*` / `R-*`) keep their Chinese explanations in the service layer; credentials and tokens are masked before any log write.

Branch names and commit messages follow `feat|fix|docs|chore(scope): summary`.

By submitting a contribution, you agree to license it under the [Mulan PSL v2](LICENSE).

## Roadmap

This repository does not maintain a long-range feature roadmap. Planned work and progress are tracked in the open:

- **Recently completed**: the seven-model landing (M1/M2/M3/M5/M6/M7/MU with an executable `nodeGraph`), center isolation (`ign` schema / stream / subject / queue), the v1.1 delivery chain (step codes, source traceability, verdict sources, masked task logs), the delivery facade with flat-JSON semantics, and the local execution kernel with its four terminal states.
- **Known gaps**: O-AST asset existence checks and O-CRT certificate issue/revocation cross-center channels are not yet wired; the REMOTE executor proxy counterpart is reserved but unimplemented (Q-68); real mounts and upstream joint testing remain to be verified on 3.200.
- **Limitations**: the action-code whitelist is a three-layer reachable surface (11 delivery actions for humans and the facade, `ezctl.new` internal-only, `scripts.*` platform-only); in-flight tasks cannot be verified after a platform restart and are therefore explicitly `LOST`; the facade deliberately does not wrap responses in `returnInfo`.

## Community

- **Issues and suggestions**: open a repository issue with reproduction steps and environment details (version, migration number, deployment mode).
- **Internal channels**: the OPIC platform team; production incidents go through the on-call channel.

## Security & Support

- **Reporting a vulnerability**: please do not open a public issue; contact the platform team directly. The security baseline includes facade-token gating (missing token ⇒ 401 closed, never degraded), governance refusal for out-of-whitelist or command-concatenation risk submissions with audit, irreversible masking of passwords / tokens / private keys before log writes, and a read-only SQL whitelist that deliberately excludes `bootstrap_credential` (INV-22).
- **Support scope**: the current main branch and the deployed production version.

## License

Copyright © 2026 wjluo

This project is licensed under the **Mulan Permissive Software License, Version 2 (Mulan PSL v2)**. The full text is in [`LICENSE`](LICENSE) and is also available at <http://license.coscl.org.cn/MulanPSL2>.

Subject to the terms of the license, you may copy, use, modify, and distribute this software, including for commercial purposes; copyright and license notices must be retained when redistributing. Section 3 grants no trademark license, and Section 5 provides the software on an "AS IS" basis without warranties of any kind.

### Third-Party and Ported Code

- **opic-techbase** (Mulan PSL v2): this repository is a platform-level fork; the technology layer inherits the baseline, and the baseline's license continues to apply to that portion.
- **gopherforge** (MIT): the engineering pattern and technology selection baseline, and the admin console's visual system (glass theme, login page, dashboard styling) is ported from its frontend, with source headers preserved in the ported files. Its MIT notice continues to apply to that portion.
