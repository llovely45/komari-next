# PostgreSQL Redis Modular Plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (\`- [ ]\`) syntax for tracking.

**Goal:** 在保持现有 JavaScript 插件兼容的前提下，为 komari-next 建立 Go 模块生命周期、PostgreSQL 主库、Redis 缓存和 Go 外部插件协议基础。

**Architecture:** 采用模块化单体。Go 内置模块通过类型化 Host 接口运行；第三方插件作为独立进程，目标是通过版本化 Protobuf/gRPC 协议经 Unix Socket 通信，TCP/TLS 仅作为后续需要时的显式传输选项；首个切片只交付稳定的 wire data/validation，Protobuf 字段编号、service/method、socket framing 和握手响应契约留到后续 process-manager 阶段；JavaScript 插件继续由 Goja 兼容层加载。PostgreSQL 是生产主库和指标库目标，Redis 只做可失效缓存。

**Tech Stack:** Go 1.25、GORM PostgreSQL Dialector、database/sql 指标存储、go-redis/v9、Protobuf-compatible plugin protocol、现有 Gin/Goja/RPC。

**Spec:** docs/technical-design-pgsql-redis-modular-plugin.md

## Global Constraints

- Go 负责核心、模块和高性能路径。
- Go 子进程负责第三方插件。
- JavaScript 插件保留兼容。
- Rust 只用于独立外部服务。
- PostgreSQL 是生产主数据库目标；SQLite 仅保留兼容、开发和显式迁移源。
- Redis 不作为唯一数据源；Redis 不可用时必须回源 PostgreSQL。
- 新增代码必须先有一个能够正确失败的测试，再写最小实现。
- 不将原始 gorm.DB、Redis 客户端、密钥或 DSN 注入插件 Host。
- 不改变已有 Agent 协议、公开 API 和 JavaScript 插件清单格式。

---

### Task 1: 保存架构与实施文档

**Files:**
- Create: docs/optimization-direction.md
- Create: docs/technical-design-pgsql-redis-modular-plugin.md
- Create: docs/superpowers/plans/2026-09-19-pgsql-redis-modular-plugins.md

**Interfaces:**
- Produces the design contract consumed by Tasks 2–5.

- [x] Step 1: Record the current baseline

  Record that the main database currently accepts only SQLite, while the separate metric store already supports PostgreSQL. Record the existing JavaScript plugin path and that the Task 1 baseline dependency list does not yet contain a Redis client/runtime dependency.

- [x] Step 2: Define the target architecture

  Define Go modules, Go subprocess plugins, JavaScript compatibility, PostgreSQL ownership, Redis fallback semantics, migration safety and profiling gates.

- [x] Step 3: Self-review the plan

  Verify every requested language/storage requirement has a corresponding task or explicit later phase. The first implementation slice is limited to Tasks 2–5 plus Task 6 verification: SQLite-to-PostgreSQL data migration/cutover, runtime Redis read-through integration, Go plugin process management, business-module migration, profiling/tuning, the Unix-socket/Protobuf service contract, and any Rust external-service integration remain later phases. No startup-time silent data conversion or in-process Rust is part of this slice.

### Task 2: Add the Go module lifecycle registry

**Files:**
- Create: internal/module/module.go
- Test: internal/module/module_test.go

**Interfaces:**
- Consumes: no existing application behavior.
- Produces: module.Module, module.Host, module.Registry, NewRegistry, Register, Start, Stop, and dependency validation for later server integration.

- [x] Step 1: Write failing tests

  Add tests for dependency-first startup, reverse shutdown, missing dependency rejection, cycle rejection, duplicate ID rejection, and rollback when a later module fails to start.

- [x] Step 2: Run the focused test

  Run: go test ./internal/module

  Expected: FAIL because the module package and lifecycle implementation do not exist.

- [x] Step 3: Implement the smallest registry

  Implement deterministic topological sorting, lifecycle state tracking, reverse cleanup, and wrapped errors. This is only the first implementation slice: Host contains only a logger and a versioned typed extension point; do not add database, metrics, cache, event, scheduler, or Redis concrete types. Those future capability ports are staged for later tasks with separate contracts and tests, and are not Task 2 Host fields.

- [x] Step 4: Run the focused test

  Run: go test ./internal/module

  Expected: PASS.

### Task 3: Add PostgreSQL as the primary database adapter

**Files:**
- Modify: cmd/flags/config.go
- Modify: cmd/root.go
- Modify: database/dbcore/dbcore.go
- Modify: database/dbcore/maintenance.go
- Test: cmd/flags/config_test.go
- Test: database/dbcore/dialect_test.go
- Modify: go.mod
- Modify: go.sum

**Interfaces:**
- Consumes: existing SQLite flag and dbcore initialization.
- Produces: DatabaseTypePostgres, DatabaseDSN, IsPostgres, --db-dsn, KOMARI_DB_DSN support, and a GORM PostgreSQL dialector path.

- [x] Step 1: Write failing flag and dialector tests

  Test normalization of postgres and postgresql, supported type reporting, and selection by constructing the PostgreSQL Dialector only; the test must not open a live network connection. If a GORM open path is tested, use `gorm.Config{DisableAutomaticPing: true}` and do not call `Ping` or `AutoMigrate`.

- [x] Step 2: Run the focused tests

  Run: go test ./cmd/flags ./database/dbcore

  Expected: FAIL because PostgreSQL constants, DSN fields and dialector selection do not exist.

- [x] Step 3: Add the PostgreSQL dependency

  Add gorm.io/driver/postgres at a version compatible with the existing GORM line, retaining the existing direct pgx/v5 dependency used by the metric store.

- [x] Step 4: Implement adapter selection

  Add PostgreSQL flags and environment wiring. Keep SQLite behavior unchanged. For PostgreSQL, use gorm.Open(postgres.Open(dsn), logConfig), skip SQLite file/WAL backup operations, and keep schema migration execution through the existing migration layer. This adapter task must not silently migrate or cut over SQLite data; the explicit data-migration flow remains a later phase with source backup, resumable batches, verification before cutover, and rollback support.

- [x] Step 5: Run the focused tests

  Run: go test ./cmd/flags ./database/dbcore

  Expected: PASS without requiring a running PostgreSQL server.

### Task 4: Add the Redis Cache Port and implementation

**Files:**
- Create: internal/cache/cache.go
- Create: internal/cache/redis.go
- Test: internal/cache/cache_test.go
- Modify: cmd/flags/config.go
- Modify: cmd/root.go
- Modify: cmd/server.go
- Modify: internal/server/app.go
- Create: internal/server/cache.go
- Test: internal/server/app_test.go
- Modify: go.mod
- Modify: go.sum

**Interfaces:**
- Consumes: no current cache API.
- Produces: cache.Cache, cache.ErrMiss, cache.NewRedis, JSON get/set/delete operations, and a no-op implementation for Redis-disabled development. `GetJSON` returns `(true, nil)` for a hit and `(false, err)` with `errors.Is(err, cache.ErrMiss)` for the only valid miss; `(false, nil)` is not a miss signal, including for Noop. Backend errors remain distinct from `cache.ErrMiss`, and callers fall back to PostgreSQL.

- [x] Step 1: Write failing cache contract tests

  Test JSON round-trip, TTL forwarding, namespaced keys, cache miss behavior, delete forwarding and Redis error propagation using an in-memory fake Redis client.

- [x] Step 2: Run the focused test

  Run: go test ./internal/cache

  Expected: FAIL because the Cache interface and Redis implementation do not exist.

- [x] Step 3: Implement the cache contract

  Add a small interface independent of go-redis types. Marshal values as JSON, prefix every key with komari:v1:, return `(false, cache.ErrMiss)` for cache misses (including Noop), and provide fallback behavior that reads PostgreSQL on `errors.Is(err, cache.ErrMiss)` or another Redis backend error, then best-effort fills Redis only after the source read succeeds.

- [x] Step 4: Add Redis client construction

  Add github.com/redis/go-redis/v9. Implement NewRedis from a Redis URL/options, keep timeouts bounded, and expose Close without exposing the raw client through the core interface.

- [x] Step 5: Run the focused test

  Run: go test ./internal/cache

  Expected: PASS without requiring a Redis server.

### Task 5: Add the versioned Go plugin protocol foundation

**Files:**
- Create: internal/pluginprocess/protocol.go
- Test: internal/pluginprocess/protocol_test.go

**Interfaces:**
- Consumes: no current external Go plugin protocol.
- Produces: ProtocolVersion, Handshake, Capability, Message, ValidateHandshake, and structured plugin errors as stable wire data/validation for a future process manager. It does not produce the Unix-socket transport, a Protobuf `.proto`, field numbers, service/method definitions, or handshake response envelope.

- [x] Step 1: Write failing protocol tests

  Test valid handshake acceptance, rejected protocol version, empty plugin ID, unapproved capabilities, and stable logical/JSON diagnostic field names. Do not treat JSON tags as a Protobuf schema or test field numbers/service methods in this slice.

- [x] Step 2: Run the focused test

  Run: go test ./internal/pluginprocess

  Expected: FAIL because the protocol package does not exist.

- [x] Step 3: Implement validation and wire types

  Use explicit JSON tags and integer protocol versions for the diagnostic/stable data model. Keep the wire model independent of GORM, Gin and Goja. Include plugin_id, plugin_version, komari_api_version, requested_capabilities, approved_capabilities, and protocol_version. Defer the `.proto` field numbers, Unix-socket framing, service/method definitions, and accepted/rejected handshake response to the later process-manager phase.

- [x] Step 4: Run the focused test

  Run: go test ./internal/pluginprocess

  Expected: PASS.

### Task 6: Verify the first implementation slice

**Files:**
- Modify: none unless verification exposes a defect.

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces: a reviewed branch containing the docs and independently tested foundations.

- [x] Step 1: Run formatting

  Run: gofmt -w internal/module internal/cache internal/pluginprocess cmd/flags database/dbcore

- [x] Step 2: Run focused tests

  Run: go test ./internal/module ./internal/cache ./internal/pluginprocess ./cmd/flags ./database/dbcore

- [x] Step 3: Run static checks

  Run: go vet ./internal/module ./internal/cache ./internal/pluginprocess ./cmd/flags ./database/dbcore

- [x] Step 4: Check the diff

  Run: git diff --check and git status --short.

  The full repository test remains expected to be blocked by the release source missing web/public/defaultTheme, which is built from the separate komari-web repository; this pre-existing blocker must be reported rather than hidden.

Verification result: the focused implementation tests and `go vet` pass, while
`go test ./...` also reaches existing macOS-only `pkg/jsruntime` failures
(temporary-directory permission checks and unsupported process metrics) after
the missing frontend embed errors. Those unrelated failures are not changed in
this branch.
