# 当前技术架构

本文描述当前代码已经实现的运行时架构，不描述尚未接入运行时的目标设计。目标架构请阅读[优化方向](./optimization-direction.md)和[PostgreSQL、Redis 与模块化插件技术设计](./technical-design-pgsql-redis-modular-plugin.md)。

> [!IMPORTANT]
> 当前正常启动使用 PostgreSQL 作为唯一应用数据库。主库表和指标表共用同一个 PostgreSQL 连接池；Redis 是固定在 127.0.0.1:6379 的启动依赖。SQLite、独立指标库 DSN 和可配置 Redis URL 只在兼容测试或历史迁移辅助代码中保留。

## 系统边界

Komari 是一个 Go 单体服务：HTTP、WebSocket、JSON-RPC、后台调度、控制面数据库、监控指标存储和 JavaScript 插件运行时都在同一个进程内。Docker 镜像还会在同一容器内启动一个仅绑定回环地址的 Redis 进程。Agent 通过客户端 Token 连接或上报，Web UI 通过 Session Cookie、API Key 或匿名身份访问。

~~~mermaid
flowchart TD
    Main[main.go] --> Command[cmd.RunServer]
    Command --> Lifecycle[internal/server.App]
    Lifecycle --> Bootstrap[Bootstrap: data directories, PostgreSQL, settings]
    Bootstrap --> MainDB[(PostgreSQL)]
    Bootstrap --> Retired[Remove retired metric backend settings]
    Lifecycle --> Guides[Install / database migration guide]
    Lifecycle --> Metric[Metric Store]
    Metric --> MetricTables[(metric_* tables)]
    MetricTables --> MainDB
    Lifecycle --> Cache[Cache boundary]
    Cache --> Redis[(Redis 127.0.0.1:6379)]
    Lifecycle --> Providers[OAuth, GeoIP, message sender]
    Lifecycle --> Router[HTTP router and middleware]
    Router --> Public[Public REST and JSON-RPC]
    Router --> Admin[Admin REST, JSON-RPC and streams]
    Router --> Agent[Agent v2 HTTP/WebSocket]
    Router --> Static[Embedded frontend and plugin pages]
    Agent --> Runtime[In-memory presence and latest reports]
    Runtime --> Metric
    Admin --> MainDB
    Admin --> Metric
    Plugins[JavaScript plugin runtime] --> Router
    Plugins --> MainDB
~~~

## 组件职责

| 组件 | 位置 | 职责 |
| --- | --- | --- |
| 命令入口 | main.go, cmd/ | 解析监听地址和 PostgreSQL DSN，串联启动阶段 |
| 生命周期 | internal/server/ | 初始化数据目录、主库、指标存储、内置 Redis、Provider、路由和关闭流程 |
| HTTP 路由 | web/router/ | 注册公开、Agent、管理员、流式传输和静态资源路由 |
| API 身份层 | web/api/ | 识别匿名用户、Session、API Key 和 Agent Token，并执行角色校验 |
| JSON-RPC | pkg/rpc/, web/rpc/jsonrpc/ | 注册方法、权限匹配、参数绑定、HTTP/WS 传输和响应映射 |
| Agent 接入 | web/api/client/, web/agent/ | 接收 v2 上报、维护在线状态、向 Agent 投递任务和事件 |
| 控制面存储 | database/, database/dbcore/ | 用户、节点、任务、通知、插件配置、审计日志等持久化数据 |
| 指标存储 | pkg/metric/, internal/metricstore/ | 共享 PostgreSQL 指标表的写入、聚合、rollup、保留和结构迁移 |
| 缓存 | internal/cache/, internal/server/cache.go | 通过固定本地 Redis 缓存可丢失热点数据，不作为数据源 |
| 插件运行时 | internal/plugin/, pkg/jsruntime/ | 加载 JavaScript 插件、权限审批、RPC、Hook、静态页面和配置 |
| 前端资源 | web/public/ | 将默认主题以压缩归档嵌入二进制，并提供主题/静态页面 |

## 请求路径

### 浏览器和第三方客户端

正常路由在 [internal/server/runtime.go](../internal/server/runtime.go) 中创建 Gin 引擎，并依次应用日志/恢复、CORS、身份识别、私有站点访问控制和 API 禁止缓存策略。之后由 [web/router/router.go](../web/router/router.go) 注册路由。

JSON 接口有两种传输方式：

1. 传统路径通过 jsonRpc.Bind 将路径参数、查询参数和 JSON body 合并后调用一个 RPC 方法，再映射为旧 REST 响应形状。
2. /api/rpc2 直接接收 JSON-RPC 2.0。POST 处理单条或批量请求，GET 升级为 WebSocket。

二进制和长连接接口不经过 JSON-RPC 响应包装，包括文件传输、终端、主题/插件上传、备份下载和 WebSocket 数据通道。

### Agent

Agent 使用 /api/clients/v2/rpc：

- POST 接收单条 v2 JSON-RPC 请求，支持 Content-Encoding: gzip。
- GET 必须升级为 WebSocket，之后每条消息都是一条 v2 JSON-RPC 请求或服务端事件。
- Agent Token 默认通过查询参数 token 传入；POST body 也可以携带 token，Authorization 查询参数是兼容别名。
- POST 上报会刷新约 35 秒的在线状态；WebSocket 在线状态由连接生命周期维护。

详细的请求方法、事件和 Report 字段见 [Agent v2 协议](./api/agent-v2.md)。

## 数据与存储

### 主数据库

正常应用入口使用 PostgreSQL，连接串来自 KOMARI_DB_DSN 或 --db-dsn。当前启动入口不注册 --db-type、--database 或 Redis URL 参数，也不会在 DSN 缺失时自动创建 SQLite 主库。

主库连接由 GORM 管理，底层 PostgreSQL database/sql 连接池默认最多 25 个打开连接、5 个空闲连接。主库保存用户、Session、客户端、任务、通知、主题/插件配置和审计日志。

database/dbcore 仍保留 SQLite Dialector、文件恢复和测试辅助路径，但它们不是当前正常部署的配置合同。不要据此推断新实例会生成 data/komari.db。

### 指标存储

指标存储始终启用。正常配置不保存独立指标驱动或 DSN，而是通过 `dbcore.GetSQLDB()` 复用主 PostgreSQL 连接池，并以 `metric_` 前缀区分指标表。旧版本的独立指标后端配置键会在启动时清理；当前运行时不再通过它们切换数据库。

采集写入由有界批处理器提交，后台任务负责 rollup 压缩和按指标保留策略清理。查询会根据时间范围选择原始点或分钟、五分钟、小时、日级 rollup；返回中可能出现 downsampled、interval_seconds 和 count，客户端不能假设每个查询都返回原始采样点。

admin:dbQuery、admin:dbExec 和 admin:dbTables 中的 database=main|metrics 是两个逻辑目标，当前都落在同一个 PostgreSQL 数据库/连接池；metrics 不是第二个物理数据库。

### Redis

Redis 是缓存和短期状态依赖，不是数据源。当前应用固定使用 redis://127.0.0.1:6379/0：

- Docker 入口脚本启动容器内 Redis，并只绑定 127.0.0.1。
- 直接运行二进制或源码时，运维环境必须自行提供该地址的 Redis。
- internal/server.InitCache 会在正常路由创建前 Ping Redis；无法连接会终止启动。
- 已经运行的 read-through 缓存路径在后端故障时可以回源 PostgreSQL；缓存丢失不会改变主库数据。

模块只依赖 internal/cache.Cache，不会拿到原始 go-redis 客户端。缓存 Key 使用命名空间，缓存值必须设置正 TTL，且不应写入完整 Token、密码或 DSN。

### 数据目录

相对路径默认以进程工作目录为基准。正常 PostgreSQL 部署需要持久化本地资源目录，但数据库备份要单独处理：

| 路径 | 内容 |
| --- | --- |
| data/theme/ | 用户主题数据 |
| data/plugin/ | 插件包和插件运行数据 |
| data/plugin-data/ | 插件持久化目录 |
| data/backup/ | 版本升级或恢复过程中生成的备份归档 |
| data/metrics.db | 旧版本可能遗留的文件；当前正常运行不会自动读取或写入 |

当前正常启动不会创建 data/komari.db 或 data/metrics.db。PostgreSQL 负责主库与指标表，data/ 负责主题、插件和归档。

## 启动阶段与受限模式

启动顺序由 [cmd/server.go](../cmd/server.go) 明确串联：

~~~mermaid
sequenceDiagram
    participant P as Process
    participant A as server.App
    participant DB as PostgreSQL
    participant M as Shared Metric Store
    participant R as Local Redis
    participant H as HTTP listener

    P->>A: Bootstrap
    A->>DB: Connect, migrations, AutoMigrate
    A->>A: Remove retired metric backend settings
    A->>A: Load settings
    A->>A: First-run install check
    A->>A: Database migration check
    A->>M: Open shared metric tables (retry 3 times)
    A->>R: Create and Ping 127.0.0.1:6379
    A->>A: Init stores, providers, schedules and plugins
    A->>A: Build normal router
    A->>H: Listen and serve
~~~

首次安装或指标结构/旧表迁移需要管理员操作时，程序不会把不完整的普通路由暴露出去，而是启动临时受限服务：

| 模式 | 页面 | API 前缀 | 说明 |
| --- | --- | --- | --- |
| 首次安装 | /install | /api/install | 创建首个管理员并保存站点设置；不接收指标库 DSN |
| 数据库迁移 | /admin/database-migration | /api/admin/database-migration | 执行指标 store 结构升级，或把旧监控表导入指标表 |

`DatabaseMigrationRequired` 先检查指标 store 是否需要结构升级，再检查旧的 legacy monitoring 表。结构升级向导可以选择保留历史数据重建，或在明确操作后丢弃历史指标；旧 monitoring 表导入完成后会清理旧表。该向导只处理共享 PostgreSQL 中的表结构和旧主库表，不接受或切换独立指标库 DSN。

当前 cmd.RunServer 不挂载 /database-recovery 恢复向导；主库或指标 store 无法初始化时会在启动阶段失败（指标 store 会进行有限重试），不会自动把 DSN 恢复页面暴露到正常服务。

## 身份与权限模型

身份识别优先级是 API Key、Session Cookie、Agent Token、匿名。权限是角色集合，不是简单的“管理员自动拥有客户端上报权限”：

| 主体 | 默认角色 | 典型用途 |
| --- | --- | --- |
| 匿名访客 | guest | 公开节点、公开设置、公开指标查询 |
| Agent Token | client | Agent v2 上报和接收控制事件 |
| 管理员 Session | admin | Web 管理端和管理员 RPC |
| API Key | admin | 自动化管理调用 |

public:* 和 common:* 的公开方法允许 guest；admin:* 要求 admin；Agent 专用方法要求 client。私有站点模式会在 RPC 分发前拒绝未认证访客，但会放行登录页所需的 public:getMe、public:getPublicSettings 和 public:getVersion 等元信息。

## 插件边界

当前运行时主要是 JavaScript 插件：插件通过 manifest 声明权限，使用 Goja 运行时，并可注册 RPC、定时任务、HTTP/HTML/WebSocket Hook 和管理/公开页面。启用权限发生变化的插件时，管理员 API 可能先返回 requires_approval。

internal/pluginprocess/ 当前提供外部 Go 插件协议的数据结构和校验基础；Unix Socket framing、Protobuf 字段编号、完整进程管理和独立崩溃隔离仍属于演进设计，不应在当前实现上假设已经可用。

## 开发与验证

前端默认主题需要存在 web/public/defaultTheme/dist.tar.zst，否则包含静态资源的构建/测试会失败。仓库的 CI 使用 .github/actions/build-frontend 生成该资源。

常用检查：

~~~bash
go test ./...
go build .
git diff --check
~~~

修改路由或 RPC 后，至少应检查：

1. [HTTP 路由参考](./api/http-endpoints.md) 是否仍覆盖新增/删除的路径。
2. [JSON-RPC 方法参考](./api/json-rpc.md) 的方法名、角色、参数和敏感操作说明是否同步。
3. [Agent v2 协议](./api/agent-v2.md) 是否仍与 protocol/v2/jsonrpc.go 一致。
