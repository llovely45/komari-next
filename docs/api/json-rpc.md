# JSON-RPC 方法参考

本文描述当前分支通过 `/api/rpc2` 暴露的 JSON-RPC 2.0 方法，以及传统 HTTP 路由桥实际调用的同一组方法。方法名必须使用完整命名空间；未带命名空间的旧写法不属于稳定 API。

方法和字段以源码为准，主要实现位置是 `pkg/rpc/` 与 `web/rpc/jsonrpc/`。如果某个部署加载了插件，`rpc.methods` 还可能返回插件动态注册的方法。

## 调用方式

### HTTP 和 WebSocket

HTTP POST 支持单条请求和批量请求：

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer [REDACTED_API_KEY]' \
  --data '{"jsonrpc":"2.0","method":"public:getVersion","params":{},"id":1}' \
  http://127.0.0.1:25775/api/rpc2
```

`GET /api/rpc2` 必须完成 WebSocket Upgrade。连接建立后，每条文本消息都是一条 JSON-RPC 请求，服务端返回对应响应。HTTP 和 WebSocket 的身份识别、凭据优先级及 2FA 规则见[认证与权限](./authentication.md)。

### 请求和响应

标准请求：

```json
{
  "jsonrpc": "2.0",
  "method": "admin:listClients",
  "params": {},
  "id": 1
}
```

`params` 可以使用命名对象，也兼容位置数组；本文统一使用命名对象。成功响应和错误响应二选一：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "version": "1.0.0",
    "hash": "[REDACTED_BUILD_HASH]"
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32602,
    "message": "invalid params"
  }
}
```

省略 `id` 的请求是 Notification，服务端执行后不返回响应。批量请求的 body 是请求数组，返回响应数组；空数组会被拒绝。`result: null` 在 JSON 中可能表现为省略 `result` 字段。

### 错误码

| 错误码 | 含义 | 传统 HTTP 路由桥状态码 |
| ---: | --- | ---: |
| `-32700` | JSON 解析失败 | `400` |
| `-32600` | 请求结构无效或 `jsonrpc` 版本错误 | `400` |
| `-32601` | 方法不存在 | `500` |
| `-32602` | 参数无效 | `400` |
| `-32603` | 内部错误 | `500` |
| `-32040` | 未认证 | `401` |
| `-32041` | 无权限或敏感操作 2FA 失败 | `401` |
| `-32044` | 资源不存在 | `404` |
| `-32045` | 资源已存在 | `409` |
| `-32010` | 操作取消 | `408` |
| `-32011` | 依赖或操作超时 | `504` |
| `-32021` | 并发冲突或事务中断 | 通常 `500` |
| `-32022` | 数值或索引越界 | 通常 `500` |
| `-32050` | 未实现 | `501` |
| `-32051` | 依赖服务不可用 | `503` |
| `-32052` | 不可恢复的数据丢失 | 通常 `500` |

直接访问 `/api/rpc2` 时，业务错误仍放在 JSON-RPC `error` 中，HTTP POST 传输层通常返回 `200`；解析失败返回 `400`。传统路由桥会把错误转换成 `{ "status": "error", "message": "..." }`，并使用表中的 HTTP 状态码。

### 运行时发现方法

先调用 `rpc.methods` 或 `rpc.help` 可以获得当前构建和插件实际注册的方法。自动化客户端应把返回值视为能力发现结果，不要假设所有插件方法在每个实例都存在。

| 方法 | 参数 | 返回值 | 说明 |
| --- | --- | --- | --- |
| `rpc.methods` | `{ "internal"?: boolean }` | `string[]` | 默认列出非 `rpc.*` 方法；`internal=true` 时包含内部方法 |
| `rpc.version` | 无 | `"2.0"` | JSON-RPC 内核版本 |
| `rpc.ping` | 无 | `"pong"` | RPC 健康检查 |
| `rpc.help` | `{ "method"?: string }` | 方法元数据或元数据数组 | 指定方法时返回 `name`、`summary`、参数和返回值描述；不指定时返回全部元数据 |

## 公开方法

`public:*` 要求 `guest` 即可调用。私有站点模式下，未登录访客仍只允许登录页所需的 `public:getMe`、`public:getPublicSettings`、`public:getVersion` 和访客审计方法；隐藏节点和敏感字段会按调用身份过滤。

| 方法 | 参数 | 返回值 | 说明 |
| --- | --- | --- | --- |
| `public:getMe` | 无 | `{ username, logged_in, uuid?, sso_type?, sso_id?, 2fa_enabled? }` | 未登录返回 Guest 占位信息 |
| `public:getNodesInformation` | 无 | `Client[]` | 返回可见节点基本信息；公开结果不含 Token、版本、备注和 IP |
| `public:getPublicSettings` | 无 | `object` | 返回公开站点设置；有效临时分享许可会将 `private_site` 视为 `false` |
| `public:getVersion` | 无 | `{ version, hash }` | 返回服务版本和构建 hash |
| `public:getClientRecentRecords` | `{ "uuid": string }` | `Report[]` | 返回内存中约 1 分钟的近期原始上报；隐藏节点对匿名调用不可见 |
| `public:getRecordsByUUID` | `{ "uuid": string, "load_type"?: string, "hours"?: string }` | `{ records, count, load_type?, gpu_devices?, has_gpu_data? }` | `load_type` 可为 `cpu`、`gpu`、`ram`、`swap`、`load`、`temp`、`disk`、`network`、`process`、`connections` 或 `all`；默认最近 4 小时 |
| `public:getPingRecords` | `{ "uuid"?: string, "task_id"?: string, "hours"?: string }` | `{ count, basic_info?, records, tasks? }` | `uuid` 和 `task_id` 至少提供一个；默认最近 4 小时；丢包值为负数 |
| `public:getPublicPingTasks` | 无 | `PingTask[]` | 返回公开 Ping 任务摘要，不含目标地址 |
| `public:recordVisitorEvent` | `{ "event": string, "action"?: string, "operation"?: string, "path"?: string, "route"?: string, "target"?: string, "detail"?: object }` | `{ "status": "success" | "disabled" | "rate_limited" }` | 访客审计开关关闭时不写库；按来源 IP 限流，事件和 detail 有长度上限 |

### 公开指标方法

这些方法读取共享 PostgreSQL 指标表，并自动隐藏匿名访客不可见节点。较长时间范围会返回 rollup 数据；调用方应根据 `downsampled`、`interval_seconds`、`count` 和点中的 `count` 判断结果是否为聚合点。

| 方法 | 参数 | 返回值 | 说明 |
| --- | --- | --- | --- |
| `public:listMetricDefinitions` | 无 | `MetricDefinition[]` | 列出公开指标名称、类型、单位、保留天数和 metadata |
| `public:queryMetrics` | 见下表 | `{ start, end, series, count, default_points, server_downsample_default }` | 查询一个或多个指标，可按节点、标签、时间范围、聚合算法和最大点数筛选 |
| `public:getPingMetricStats` | 见下表 | `{ start, end, interval_seconds, stats, count }` | 返回 Ping 延迟/丢包的按节点和任务统计 |

`public:queryMetrics` 的参数：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `metric_key` / `metric_keys` / `metrics` | `string` / `string[]` | 至少提供一个指标名；数组字段会合并去重 |
| `entity_id` / `entity_ids` | `string` / `string[]` | 节点 UUID；省略时查询所有可见节点 |
| `start`、`end` / `start_time`、`end_time` | RFC3339 | 时间范围；省略时默认最近 4 小时 |
| `hours` | `number` | 未提供明确时间时的回溯小时数，默认 4 |
| `tags` | `object` | 标签过滤 |
| `max_points` | `number` | 每个指标的最大点数，默认 500；也可用 `max_points_by_metric` 或 `points_by_metric` 覆盖 |
| `aggregation` / `algorithm` | `string` | rollup 聚合算法，默认 `avg`；也可按指标指定映射 |
| `fill_empty` | `boolean` | 是否插入缺口的 `value: null` 点 |

示例：查询某节点最近 1 小时的 CPU 使用率，最多返回 300 个点：

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","method":"public:queryMetrics","params":{"metric_keys":["cpu.usage"],"entity_id":"[REDACTED_CLIENT_UUID]","hours":1,"max_points":300},"id":1}' \
  https://<host>/api/rpc2
```

每个 `series` 元素包含 `metric_key`、`entity_id`、`points`、`downsampled`、`interval_seconds`、`count`，以及可用时的 `type`、`unit`、`retention_days` 和 `tags`。每个点的 `value` 可能为 `null`，表示缺失或按 `fill_empty` 转换的 Ping 丢包值。

`public:getPingMetricStats` 支持 `uuid`/`entity_id`、`entity_ids`、`task_id`/`task_ids`、`start`/`end`（或 `start_time`/`end_time`）、`hours` 和 `max_points`。每个 `stats` 元素包含 `entity_id`、`task_id`、`total`、`valid`、`loss`、`min`、`max`、`avg`、`latest`、`p50`、`p99`、`stddev` 和 `p99_p50_ratio`；部分字段在没有有效数据时省略。

## common 兼容方法

`common:*` 是历史 RPC 或内部页面使用的兼容方法，与新版 `public:*` 的返回形状不完全相同。它们同样允许 `guest`，但仍执行隐藏节点过滤。建议新客户端优先使用上面的 `public:*` 方法。

| 方法 | 参数 | 返回值 | 说明 |
| --- | --- | --- | --- |
| `common:getNodes` | `{ "uuid"?: string }` | 单个 `Client` 或 `{ [uuid]: Client }` | 管理员可看到完整节点；其他身份按公开设置脱敏 IP 和字段 |
| `common:getNodesLatestStatus` | `{ "uuid"?: string, "uuids"?: string[] }` | 单个状态对象或 `{ [uuid]: status }` | 返回运行时最新报告、在线状态和 Ping 摘要 |
| `common:getMe` | 无 | 用户、API Key、Agent 或匿名身份对象 | 历史 `/api/me` 语义；优先使用 `public:getMe` |
| `common:getPublicInfo` | 无 | `object` | 历史公开设置方法 |
| `common:getVersion` | 无 | `{ version, hash }` | 历史版本方法 |
| `common:getNodeRecentStatus` | `{ "uuid": string }` | `{ count, records }` | 将近期运行时报告投影为旧版扁平记录 |
| `common:getRecords` | `{ "type"?: "load" | "ping", "uuid"?: string, "hours"?: number, "start"?: RFC3339, "end"?: RFC3339, "load_type"?: string, "task_id"?: number, "maxCount"?: number }` | 负载或 Ping 历史对象 | 默认负载、最近 1 小时、最多 4000 点；`maxCount=-1` 禁止下采样 |

## 管理员方法

以下方法都要求 `admin`。管理员 Session 和 API Key 均可用；Agent Token 不能调用这些方法。带“敏感”标记的方法还需当前请求的 2FA，API Key 调用免 TOTP 但必须保护 API Key。字段为 `object` 或模型数组时，字段名沿用对应 JSON 模型，不应把 Token、密码或 DSN 原文写入日志。

### 客户端和记录

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:addClient` | `{ "name"?: string }` | `{ uuid, token }` | 新 Token 只在创建或取 Token 时返回，应立即交给对应 Agent |
| `admin:editClient` | `{ "uuid": string, ...ClientFields }` | `null` | 部分更新客户端字段 |
| `admin:removeClient` | `{ "uuid": string }` | `null` | 删除客户端 |
| `admin:getClient` | `{ "uuid": string }` | `Client` | 查询单个客户端 |
| `admin:listClients` | 无 | `Client[]` | 返回完整客户端基本信息，使用 `WithRaw` 传统路由也直接返回数组 |
| `admin:getClientToken` | `{ "uuid": string }` | `{ token }` | 敏感凭据读取 |
| `admin:clearRecords` | 无 | `null` | 删除负载记录，不删除 Ping 记录 |
| `admin:clearAllRecords` | 无 | `null` | 删除负载和 Ping 记录 |
| `admin:orderClients` | `{ "<uuid>": number }` | `null` | 将客户端 UUID 映射到排序权重 |

### 会话、设置和数据库

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:getSessions` | 无 | `{ current: string, data: Session[] }` | `current` 是当前 Session Token；不要回显或持久化到日志 |
| `admin:deleteSession` | `{ "session": string }` | `null` | 删除指定登录 Session |
| `admin:deleteAllSessions` | 无 | `null` | 删除所有登录 Session，会使其他管理员全部退出 |
| `admin:getSettings` | 无 | `object` | 返回全部运行时设置；其中可能包含密钥类配置，客户端应脱敏显示 |
| `admin:editSettings` | `{ ...settings }` | `null` 或 `{ restart_required: true, guide_path: string }` | 部分更新；指标表前缀和 rollup 设置会先校验并热重载，旧的独立指标库 driver/DSN 设置会被忽略 |
| `admin:getDatabaseSize` | 无 | `{ type, size, main, monitoring, local_total }` | 查看主库和指标表逻辑目标的占用；正常部署两者都使用 PostgreSQL |
| `admin:vacuumDatabase` | 无 | `{ before, after, size, all_succeeded, main, monitoring }` | 串行执行主库和指标表逻辑目标的空间回收；已有维护任务时返回错误 |
| `admin:dbTables` | `{ "database"?: "main" | "metrics" }` | `{ database, driver, tables }` | 列出数据库表，默认主库 |
| `admin:dbQuery` | `{ "database"?: "main" | "metrics", "sql": string, "args"?: any[], "limit"?: number }` | `{ database, driver, columns, rows, row_count, truncated }` | 只读查询默认最多 1000 行，最多 10000 行；高敏感操作 |
| `admin:dbExec` | `{ "database"?: "main" | "metrics", "sql": string, "args"?: any[] }` | `{ database, driver, rows_affected, last_insert_id }` | 直接执行 SQL，可能破坏账号、节点或指标数据；高敏感操作 |

`database` 只能是 `main` 或 `metrics`；当前两者都位于同一个 PostgreSQL 数据库和连接池，`metrics` 不是第二个物理数据库。指标数据迁移和结构升级不会在服务启动时偷偷搬运，应按[配置参考](../configuration.md)和迁移向导执行。

### 剪贴板

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:getClipboard` | `{ "id": string | number }` | `Clipboard` | 查询单条剪贴板记录 |
| `admin:listClipboard` | 无 | `Clipboard[]` | 列出剪贴板记录 |
| `admin:createClipboard` | `Clipboard` | `Clipboard` | 创建记录并返回保存后的对象 |
| `admin:updateClipboard` | `{ "id": string, ...fields }` | `null` | 更新指定记录；`id` 由路由桥注入或放在参数对象中 |
| `admin:deleteClipboard` | `{ "id": string | number }` | `null` | 删除单条记录 |
| `admin:batchDeleteClipboard` | `{ "ids": number[] }` | `null` | 批量删除，ids 不能为空 |

### 任务和诊断

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:exec` | `{ "command": string, "clients": string[] }` | `{ task_id, clients, queued_clients }` | 敏感；向在线或已知 Agent 下发命令，离线节点记录失败结果 |
| `admin:getTasks` | 无 | `Task[]` | 返回所有远程执行任务及结果 |
| `admin:getTaskById` | `{ "task_id": string }` | `Task` | 查询任务和结果 |
| `admin:getTasksByClientId` | `{ "uuid": string }` | `Task[]` | 查询分配给某节点的任务 |
| `admin:getSpecificTaskResult` | `{ "task_id": string, "uuid": string }` | `TaskResult` | 查询一个任务在一个节点上的结果 |
| `admin:getTaskResultsByTaskId` | `{ "task_id": string }` | `TaskResult[]` | 查询任务的全部结果 |
| `admin:getLogs` | `{ "limit"?: string, "page"?: string, "msg_type"?: string }` | `{ logs, total }` | 默认每页 100 条，按时间倒序 |
| `admin:testGeoip` | `{ "ip"?: string }` | GeoIP 记录 | 未提供 IP 时使用请求来源 IP；需启用 GeoIP |
| `admin:testSendMessage` | 无 | `null` | 发送一条测试通知 |

### 文件控制

这些方法只传文件元数据和控制指令；大文件内容必须使用 [HTTP 路由参考](./http-endpoints.md) 中的上传/下载流，不要塞入 JSON-RPC。所有方法都需要 `uuid`，普通路径方法还需要 `path`。

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:fileListRoots` | `{ "uuid": string }` | Agent `list_roots` 结果 | 列出 Agent 暴露的文件系统根 |
| `admin:fileList` | `{ "uuid": string, "path": string }` | Agent `list` 结果 | 列目录 |
| `admin:fileStat` | `{ "uuid": string, "path": string }` | Agent `stat` 结果 | 读文件元数据 |
| `admin:fileMkdir` | `{ "uuid": string, "path": string, "mode"?: string }` | Agent 结果 | 创建目录，写操作 |
| `admin:fileDelete` | `{ "uuid": string, "path": string }` | Agent 结果 | 删除路径，写操作 |
| `admin:fileMove` | `{ "uuid": string, "source": string, "destination": string }` | Agent 结果 | 移动/重命名，写操作 |
| `admin:fileCopy` | `{ "uuid": string, "source": string, "destination": string }` | Agent 结果 | 复制文件，写操作 |
| `admin:fileChmod` | `{ "uuid": string, "path": string, "mode": string }` | Agent 结果 | 修改权限，写操作 |
| `admin:fileChown` | `{ "uuid": string, "path": string, "uid"?: number, "gid"?: number, "owner"?: string, "group"?: string }` | Agent 结果 | Unix 所有权修改，至少提供一项，写操作 |
| `admin:fileSearch` | `{ "uuid": string, "path": string, "query": string, "content"?: boolean }` | Agent 搜索结果 | 远端搜索，服务端等待时间最长约 90 秒 |

### Ping 任务和通知

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:addPingTask` | `{ "clients": string[], "default_on": boolean, "name": string, "target": string, "type": string, "interval": number }` | `{ task_id: number }` | `default_on=false` 时必须提供 clients；interval 为秒 |
| `admin:deletePingTask` | `{ "id": number[] }` | `null` | 批量删除 |
| `admin:editPingTask` | `{ "tasks": PingTask[] }` | `null` | 批量更新 |
| `admin:getAllPingTasks` | 无 | `PingTask[]` | 列出全部 Ping 任务 |
| `admin:orderPingTask` | `{ "<task_id>": number }` | `null` | 设置任务权重 |
| `admin:addLoadNotification` | `{ "clients": string[], "name": string, "metric": string, "threshold": number, "ratio": number, "interval": number }` | `{ task_id: number }` | `ratio` 为 `0 < ratio <= 1`，interval 为 1–240 分钟 |
| `admin:deleteLoadNotification` | `{ "id": number[] }` | `null` | 批量删除负载告警 |
| `admin:editLoadNotification` | `{ "notifications": LoadNotification[] }` | `null` | 批量更新负载告警 |
| `admin:getAllLoadNotifications` | 无 | `LoadNotification[]` | 列出负载告警 |
| `admin:listOfflineNotifications` | 无 | `OfflineNotification[]` | 列出离线通知 |
| `admin:editOfflineNotification` | `OfflineNotification[]` | `null` | 每项必须有 client 和正数 grace period |
| `admin:enableOfflineNotification` | `string[]` | `null` | 为节点启用离线通知 |
| `admin:disableOfflineNotification` | `string[]` | `null` | 为节点禁用离线通知 |
| `admin:sendNotification` | `{ "event": EventMessage }` | `null` | 向已配置的通知提供商发送事件；主要供插件/脚本使用 |

### 插件和外部提供商

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:listPlugins` | 无 | `Plugin[]` | 列出安装、启用和运行状态 |
| `admin:setPluginEnabled` | `{ "short": string, "enabled": boolean, "approved"?: boolean }` | `null` 或 `{ requires_approval: true }` | 权限声明变化时需要先获批准，再用 `approved=true` 重试 |
| `admin:getPluginLogs` | `{ "short": string }` | `{ logs: string }` | 返回有界日志缓冲 |
| `admin:deletePlugin` | `{ "short": string }` | `null` | 删除插件及其持久化状态 |
| `admin:getPluginConfiguration` | `{ "short": string }` | `{ configuration: object, data: object }` | 返回 manifest 声明和已保存值 |
| `admin:setPluginConfiguration` | `{ "short": string, "data": object }` | `null` | 保存后立即尝试重载插件 |
| `admin:getMessageSenderProvider` | `{ "provider"?: string }` | 提供商列表或配置对象 | 指定 provider 时返回已保存配置 |
| `admin:setMessageSenderProvider` | `MessageSenderProvider` | `{ message: string }` | `name` 必须是已注册提供商 |
| `admin:getOidcProvider` | `{ "provider"?: string }` | OIDC 提供商列表或配置对象 | 指定 provider 时返回已保存配置 |
| `admin:setOidcProvider` | `OidcProvider` | `{ message: string }` | `name` 必须是已注册提供商；配置中的 client secret 必须脱敏 |

### 指标存储和终端设置

| 方法 | 参数 | 返回值 | 备注 |
| --- | --- | --- | --- |
| `admin:listMetricDefinitions` | 无 | `MetricDefinition[]` | 列出指标类型、单位、保留天数和 metadata |
| `admin:updateMetricDefinition` | `{ "name": string, "retention_days": number }` | `MetricDefinition` | `retention_days=0` 会异步删除该指标数据 |
| `admin:getXtermjsSettings` | 无 | `XtermJSSettings` | 返回终端字体、滚动、主题和自定义 CSS |
| `admin:setXtermjsSettings` | `XtermJSSettings` | `XtermJSSettings` | 保存并归一化终端设置，成功消息由 HTTP 兼容路由补充 |

## 敏感操作清单

除管理员角色外，当前代码明确标记或在路由层额外校验的敏感操作如下：

- `admin:exec` / `POST /api/admin/task/exec`：远程执行命令。
- `GET /api/admin/client/:uuid/terminal`：新建管理员终端会话；携带已有 `request_id` 的重连按原会话所有者校验。
- `POST /api/admin/update/user`：修改管理员密码时需要 2FA。
- `POST /api/admin/2fa/disable`：关闭 2FA。
- `admin:dbExec`、文件修改方法、清理记录、删除 Session、插件删除等虽未统一标记为 TOTP 敏感方法，但都会产生破坏性或权限影响，调用方仍应纳入管理员变更审批和审计。

## 数据类型约定

- 时间字段使用 JSON 时间字符串，通常为 UTC RFC3339。
- `uuid` 指节点 UUID；`token`、Session Token、API Key、AutoDiscovery Key 是四类不同凭据。
- 公开方法会隐藏节点 Token，并按 `hidden` 和公开 IP 设置过滤节点。
- Agent v2 的 `Report`、事件和文件传输控制字段见 [Agent v2 协议](./agent-v2.md)。
- `/api/admin/...` 传统路由可能将 RPC 结果包装为 `status/message/data`，`WithRaw` 和 `WithFlat` 路由除外；直接 RPC 调用只遵循本文 JSON-RPC 响应。
