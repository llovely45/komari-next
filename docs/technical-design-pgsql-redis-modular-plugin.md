# PostgreSQL、Redis 与模块化插件技术设计

> [!NOTE]
> 本文是演进设计和模块化路线，不是当前运行时 API/部署合同。当前已实现的启动参数、数据库边界和 Redis 生命周期以[配置参考](./configuration.md)与[当前技术架构](./technical-overview.md)为准：正常入口使用 PostgreSQL，指标表复用主库连接池，Redis 使用容器内固定的 127.0.0.1:6379。

## 1. 设计范围

本设计覆盖 komari-next 服务端的模块生命周期、主数据库、指标数据库、Redis 缓存和第三方插件进程。前端主题和现有 JavaScript 插件保持兼容，不在本阶段替换为新的前端框架。

## 2. 模块 API

模块接口位于核心层，不能依赖 Gin、GORM 或具体数据库驱动：

~~~go
type Module interface {
    ID() string
    Dependencies() []string
    Init(context.Context, Host) error
    Start(context.Context) error
    Stop(context.Context) error
}
~~~

首个实现切片的 Host 只提供最小、稳定的能力边界；这只是首个切片，未来 capability ports 分阶段加入：

~~~go
type Host struct {
    Logger     *slog.Logger
    Extensions ExtensionPoint
}
~~~

`ExtensionPoint` 是版本化的 typed extension point；它只允许后续任务注册经过审查的能力类型。`ConfigPort`、`RepositoryFactory`、`MetricsPort`、`CachePort`、`EventBus` 和 `Scheduler` 是目标架构的后续 capability ports，不是首个切片或 Task 2 的 Host 字段；每个端口必须在后续阶段单独定义契约和测试。

禁止将以下对象放入 Host：

- gorm.DB
- Redis 原始客户端
- Gin Engine
- 插件进程的内部句柄
- 管理员密码、Token 原文和数据库 DSN 原文

模块注册表负责：

1. 拒绝重复 ID。
2. 检查依赖是否存在。
3. 对依赖图做拓扑排序。
4. 按依赖顺序 Init/Start。
5. Start 失败时按已启动模块的逆序 Stop。
6. 关闭时按逆序停止所有模块。

## 3. PostgreSQL 存储设计

### 3.1 控制面数据库

主数据库使用 GORM 的 PostgreSQL Dialector，连接参数来自：

~~~text
KOMARI_DB_DSN=postgres://user:password@host:5432/komari?sslmode=require
~~~

命令行参数提供等价覆盖：

~~~text
--db-dsn <dsn>
~~~

方言单测只构造 PostgreSQL `Dialector`，不得调用会连接服务器的路径。若必须测试 GORM open path，测试配置必须使用 `gorm.Config{DisableAutomaticPing: true}`，并且不得调用 `Ping` 或 `AutoMigrate`；测试不依赖运行中的 PostgreSQL。

SQLite 仍保留为兼容模式。新部署的生产文档使用 PostgreSQL；已有 SQLite 数据只能通过显式迁移切换。

### 3.2 指标数据库

当前实现使用现有 pkg/metric 的 database/sql 路径，并复用主 PostgreSQL 连接池；后续如果实测证明控制面与指标写入需要隔离，目标架构才考虑独立 schema、数据库或连接池：

- 控制面表和指标表通过 metric_ 前缀区分。
- 主连接池当前由 dbcore 统一管理；不要在指标设置中再创建第二个连接池。
- 采集写入使用有界内存队列和微批事务。
- 统计查询使用批量 series 查询，避免 N+1。

### 3.3 迁移要求

迁移过程必须：

1. 读取源库并记录迁移版本。
2. 使用 keyset pagination，不使用大 OFFSET。
3. 每批事务提交并保存进度。
4. 对用户、节点、任务、插件配置和指标分别校验数量与摘要。
5. 目标库校验通过后才切换配置。
6. 源 SQLite 文件保留备份，失败时可以回切。

## 4. Redis 设计

Redis 仅作为 Cache Port 的实现。核心接口不暴露 go-redis 类型：

~~~go
type Cache interface {
    GetJSON(context.Context, string, any) (bool, error)
    SetJSON(context.Context, string, any, time.Duration) error
    Delete(context.Context, ...string) error
    Close() error
}
~~~

`cache.ErrMiss` 是唯一的 miss 信号。`GetJSON` 的契约为：命中返回 `(true, nil)`；miss 返回 `(false, err)` 且 `errors.Is(err, cache.ErrMiss)` 为真（实现可以包装该错误）；后端故障返回 `(false, err)`，且该错误必须与 `cache.ErrMiss` 区分。`(false, nil)` 不表示 miss，包括 Noop 实现；调用方对 miss 或其他 Redis 后端错误都回源 PostgreSQL，只有源库读取成功后才 best-effort 回填。

当前服务端固定连接容器内的 127.0.0.1:6379；Docker 入口脚本负责启动 Redis，
直接运行二进制时由运维环境提供 Redis。正常启动会 Ping 该服务，连接失败会阻止
普通路由启动。未来若需要外置 Redis，应先扩展稳定的配置合同；不能把环境变量或
可选 Noop 行为写进当前部署文档。Redis 客户端由 internal/server.App 持有，并在
关闭阶段统一释放；缓存适配器仍不向模块暴露原始 go-redis 客户端。

### 缓存规则

- Key 格式：komari:v1:<domain>:<id>:<revision>。
- 每一个 Key 必须有 TTL。
- PostgreSQL 写入成功后发布失效事件。
- 读请求只有在 `ok=true` 且无错误时直接返回；仅当 `ok=false` 且 `errors.Is(err, cache.ErrMiss)` 时判定为 miss，回源 PostgreSQL 并 best-effort 回填。其他 Redis 错误记录指标和日志后同样回源 PostgreSQL，但不能把该错误当成缓存命中或只写 Redis。
- Redis 错误只记录指标和日志，不改变数据库写入结果。
- 不缓存带有敏感凭据、完整 Token 或密码的对象。

### 初始缓存对象

第一批只缓存低风险、高读频对象：

- public settings
- 节点摘要
- 版本信息
- 只读的主题/插件市场目录

用户、权限、插件审批状态和任务执行结果先不做长 TTL 缓存，避免一致性风险。

## 5. Go 外部插件协议

### 5.1 进程模型

~~~text
komari-next
  ├─ plugin-host: plugin-a
  └─ plugin-host: plugin-b
~~~

后续 process-manager 阶段的每个插件独立进程将优先使用 Unix Socket；TCP/TLS 仅作为后续需要时的显式传输选项，不能改变协议版本和能力审批边界。本阶段的 `internal/pluginprocess` 只定义稳定的握手/消息数据和验证，不建立 socket、不定义 gRPC service，也不声称 JSON tags 等同于 Protobuf schema。下面是逻辑字段模型：

~~~text
protocol_version
plugin_id
plugin_version
komari_api_version
requested_capabilities
~~~

后续 process-manager 阶段必须用 `.proto` 明确字段编号、service/method、消息 envelope 和握手响应（接受的协议版本、批准的能力或结构化拒绝原因）。版本不兼容或能力未批准时，插件不进入 Running 状态；这些传输与响应细节不属于本阶段的 wire-data/validation 包。

### 5.2 事件与调用

逻辑消息分为三类；它们不是本阶段已经固化的 gRPC service 定义：

- Call：主进程调用插件方法。
- Event：主进程批量推送节点、任务、指标等事件。
- Result：返回结果或结构化错误。

高频指标事件必须批量发送，例如每 100 ms 或达到 500 条时发送一次；禁止每条采样点单独进行 IPC。

### 5.3 插件生命周期

~~~text
Installed -> Approved -> Starting -> Running
                                      │
                         crash -> Backoff -> Starting
                                      │
                              Stop / Disabled
~~~

插件管理器需要记录 PID、协议版本、最近错误、重启次数和最后心跳时间。

## 6. JavaScript 兼容

现有 JavaScript 插件继续使用 komari-plugin.json 和 script.js。兼容层保留：

- server.registerRPC
- server.getConfig
- server.cron
- 已有权限审批
- 管理页和公开页

新的 Go 插件 API 不直接复用 Goja 内部类型；两者都实现同一组逻辑能力，但通过不同 Adapter 接入。

## 7. 安全边界

- 默认拒绝 exec、任意监听、全盘文件访问和系统 RPC。
- 插件只能访问自己的持久化目录。
- 插件包必须校验文件数量、解压大小和路径遍历。
- Go 插件二进制必须校验 SHA-256；生产环境可增加签名校验。
- 插件日志必须脱敏，不能写入 DSN、Cookie、密码和 Token。
- 外部插件退出时，主进程必须回收 Socket、定时任务和事件订阅。

## 8. 性能验收

必须保留以下基准：

~~~text
metric_write_batch
metric_series_query
postgres_repository_read
redis_cache_hit
redis_cache_miss_and_fill
plugin_ipc_call
plugin_event_batch
~~~

每次优化记录：

- 吞吐量
- p50/p95/p99
- allocs/op 和 bytes/op
- PostgreSQL 查询计划
- Redis 命中率
- goroutine 和 RSS 变化

## 9. 本阶段实现边界

本阶段先提交可以独立测试的基础设施：模块注册表、PostgreSQL 连接适配、Redis
Cache Port/实现及服务端生命周期接入、Go 插件稳定 wire data/validation
类型和测试。Unix-socket framing、Protobuf `.proto` 字段编号、service/method、
握手响应和插件进程管理必须在后续 process-manager 阶段推进；业务功能的完整迁移、
Redis read-through 热点接入、SQLite 到 PostgreSQL 的数据切换和 Rust 外部服务
评估也必须在这些接口稳定后按后续阶段推进。迁移不得在启动时静默执行，必须保留
备份、进度、校验和回切能力，避免一次性重写。
