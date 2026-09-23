# komari-next 优化方向

> [!NOTE]
> 本文是目标架构和后续路线，不替代[当前技术架构](./technical-overview.md)、[配置参考](./configuration.md)或 API 文档。当前运行时已经使用 PostgreSQL 主库、共享 PostgreSQL 指标表和容器内固定 Redis；本页保留未来模块化、隔离和可观测性工作的设计目标。

## 目标

本分支以 komari-next 1.4.3 为基线，目标不是把项目改写成另一种语言，而是把它演进成：

1. Go 负责核心业务、模块和高性能路径。
2. PostgreSQL 作为生产环境的主数据库，指标库也统一优先使用 PostgreSQL。
3. Redis 作为可失效的缓存、短期状态和热点读优化层，不作为唯一数据源。
4. 第三方插件以独立 Go 子进程运行，通过版本化协议与主进程通信。
5. 现有 JavaScript/Goja 插件保留兼容，不让旧插件阻塞新架构。
6. Rust 只作为独立外部服务，不嵌入 komari-next 主进程。

## 当前基线与问题

当前源码已经有 pkg/metric、internal/plugin、internal/scheduler 和 pkg/rpc 等边界，但仍存在以下耦合：

- internal/server 负责直接组装数据库、指标库、通知、OAuth、路由和插件。
- RPC 注册表、消息发送器和插件管理器带有进程级全局状态。
- Web/RPC 层仍有多处直接调用 dbcore.GetDBInstance()，传输层、业务层和存储层没有完全分离。
- 正常启动入口已经固定使用 PostgreSQL，SQLite 只保留在兼容测试和历史迁移辅助路径；数据库连接边界仍需要继续从全局对象中解耦。
- 当前运行时已经包含 Redis Cache Port，并由容器内固定的 127.0.0.1:6379 提供启动依赖；外置 Redis 配置和更细的失效策略仍属于后续设计。
- 指标存储有多个并发保护层，优化必须以写入吞吐、查询延迟和锁等待的实测数据为依据。
- 插件 HTTP Hook 和 HTML 注入可能对响应做大内存缓冲，不能让它们进入指标上报热路径。

## 目标架构

~~~text
HTTP / WebSocket / JSON-RPC
              │
          Transport
              │
       Application Service
              │
        Module Registry
              │
   ┌──────────┼──────────┐
  Auth      Clients    Metrics    Tasks / Notifications
              │
     Ports: Repository / EventBus / Scheduler / Cache
              │
   PostgreSQL ─┴─ Metric Store ─ Redis

Go/WASI Plugin
              │
      Wazero / JSON Lines
              │
          Plugin Host

Existing JavaScript Plugin
              │
        Goja Compatibility Layer
~~~

第一阶段采用“模块化单体”，不拆成多个微服务。这样可以先降低依赖耦合和数据库竞争，再根据 profiling 结果决定是否把某个高负载能力拆成独立服务。

## 模块与插件的边界

### Go 内置模块

内置模块编译到 komari-next 主程序，适合认证、节点、指标、任务和数据库迁移等核心能力。首个实现切片的 Host 合约最小化为 Logger 加版本化的 typed extension point；这只是首个切片，Repository、Metrics、Cache、EventBus、Scheduler 等未来 capability ports 将分阶段加入，不是 Task 2 的 Host 字段。模块之间不能互相获取全局数据库对象。

模块生命周期：

~~~text
Register -> ResolveDependencies -> Init -> Start -> Running
                                         │
                                  Stop (reverse order)
~~~

### Go 外部插件

Go 插件编译为 WASI 模块并由 Wazero 执行。插件不能获得主进程的 gorm.DB、宿主文件系统根目录或管理员凭据，只能获得 manifest 声明、allowlist 允许且用户批准的 Host API。宿主 HTTPS 接口限制公网 443 并拒绝重定向；插件存储限制在各自私有目录。

不采用 Go plugin.Open 作为通用插件机制，因为它依赖精确的 Go 工具链和平台，难以安全卸载，也会把插件崩溃带入主进程。

### JavaScript 兼容层

保留当前 komari-plugin.json、script.js、Goja 运行时和已有权限模型。新能力优先通过版本化 Host API 提供，旧 JS 插件通过兼容适配层继续运行。

### Rust

Rust 只用于需要独立部署、独立升级或特殊性能特征的外部服务，通过 HTTP、gRPC 或消息队列接入，不生成主进程内的动态库。

## PostgreSQL 与 Redis 策略

### PostgreSQL

- 控制面数据：用户、节点、任务、通知、主题、插件配置和审计日志。
- 指标数据：当前与控制面表共用 PostgreSQL 连接池并使用 metric_ 表前缀；只有 profiling 证明需要隔离时，才评估独立 schema、数据库或连接池。
- 使用连接池、事务边界和显式迁移版本。
- SQLite 保留为开发、单机兼容和迁移源，不能在升级时静默删除或覆盖。
- DSN 必须脱敏后才能进入日志、错误响应和插件日志。

### Redis

Redis 是加速层，不是数据源：

- 热点公开设置、节点摘要、权限/会话短状态、查询结果缓存。
- 所有缓存都必须有 TTL、命名空间和失效策略。
- Redis 不可用时，读请求回源 PostgreSQL；写请求不能只写 Redis。
- Cache Port 的读取契约只有两类可判定结果：命中必须返回 `(ok=true, err=nil)`；miss 必须返回 `(ok=false, err)` 且 `errors.Is(err, cache.ErrMiss)` 为真。`(false, nil)` 不是 miss，包括 Noop 实现；Redis 后端错误必须保持为与 `cache.ErrMiss` 区分的其他错误，调用方记录指标后回源 PostgreSQL；源库写入成功后才允许 best-effort 回填。
- 指标原始写入不经过 Redis，避免缓存层成为采集可靠性的瓶颈。
- 高频事件使用有界队列或批量写入，不能为每个采样点创建一个 Redis 请求。

## 性能优化顺序

1. 建立基线：CPU、内存、GC、goroutine、p50/p95/p99、数据库锁等待、指标写入吞吐和 Redis 命中率。
2. 先拆分传输层、业务层和存储层，避免在架构重构期间改变外部行为。
3. 指标写入使用有界队列、微批次、事务和预编译 SQL。
4. 热点查询增加 Redis read-through 缓存和明确失效事件。
5. 插件使用事件订阅优先，HTTP/WS Hook 只在明确声明路径时启用。
6. 最后再根据 pprof 和数据库 EXPLAIN 结果处理分配、锁粒度和序列化成本。

不以“减少文件数量”或“把所有东西改成异步”为优化目标；任何性能改动必须有基准和回归测试。

## 分阶段交付

### Phase 1：基础设施

- 模块注册和生命周期骨架。
- 主数据库 PostgreSQL 连接适配。
- Redis Cache Port、Redis 实现和当前服务端生命周期接入；外置 Redis 配置作为后续兼容性设计。
- Go/WASI 插件通过 Wazero、JSON Lines SDK、能力审批和受限宿主 API 运行；更多插件能力仍按业务需要逐步迁移。
- 文档、单元测试和兼容性说明。

### Phase 2：业务迁移

- 把通知、任务、节点和指标逐个迁移为 Go 模块。
- 移除传输层对 dbcore.GetDBInstance() 的直接依赖。
- 为每个模块增加 Repository、Service 和 RPC ownership。
- 提供显式的 SQLite 到 PostgreSQL 数据迁移与切换流程：分批可重试、校验通过后切换、保留源库备份并支持回切；不在启动时静默覆盖源数据。

### Phase 3：运行期接入

- 将 Module Registry 接入 internal/server.App。
- 启动和关闭过程统一由模块生命周期管理。
- 接入 Redis read-through 缓存和事件失效。
- 接入 Go 插件进程管理、健康检查和退避重启。

### Phase 4：实测优化

- 指标批写和查询基准。
- PostgreSQL 索引与连接池调优。
- Redis 命中率、网络开销和缓存失效风暴测试。
- 插件 IPC 延迟、吞吐和故障隔离测试。
- 只有 profiling 证明有必要时，才评估独立 Rust 外部服务；不将 Rust 动态库嵌入主进程。

## 验收标准

- 旧 JavaScript 插件可以继续安装、启停和卸载。
- PostgreSQL 连接失败时不会泄露 DSN 密码。
- Redis 关闭时核心读写仍能回源 PostgreSQL。
- Go 插件崩溃不会导致 komari-next 主进程退出。
- 模块启动顺序遵守依赖，停止顺序反向执行。
- 指标写入和查询的吞吐、延迟、内存指标有可重复基准。
- SQLite 数据可以通过显式迁移进入 PostgreSQL，迁移过程可重试、可回滚。
