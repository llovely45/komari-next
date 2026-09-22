# Agent v2 协议

Agent v2 是 Agent 与 Komari 服务端之间的 JSON-RPC 2.0 协议。它与面向浏览器/管理员的 `/api/rpc2` 方法表不同：Agent 使用客户端 Token 鉴权，并在同一条 HTTP 或 WebSocket 通道上完成监控上报、任务结果、Ping 结果和服务端事件收发。

协议类型和字段定义以 [`protocol/v2/jsonrpc.go`](../../protocol/v2/jsonrpc.go) 为准，入口实现位于 [`web/api/client/report_v2.go`](../../web/api/client/report_v2.go)。当前版本没有单独的 OpenAPI/AsyncAPI schema。

## 连接和鉴权

| 用途 | 方法和路径 | 认证 |
| --- | --- | --- |
| 单次上报/拉取 | `POST /api/clients/v2/rpc?token=[REDACTED_CLIENT_TOKEN]` | Agent Token |
| 长连接 | `GET /api/clients/v2/rpc?token=[REDACTED_CLIENT_TOKEN]` | Agent Token + WebSocket Upgrade |
| Agent 终端接入 | `GET /api/clients/terminal?id=<request_id>&token=[REDACTED_CLIENT_TOKEN]` | Agent Token + WebSocket Upgrade |
| 文件数据面 | `GET/POST /api/clients/transfer/<transfer_id>` | Agent Token + transfer token |

`Authorization=<token>` 查询参数是 v2 兼容写法；POST body 中也可携带 `token`，但推荐把 Token 放在查询参数并全程使用 HTTPS/WSS。不要把 Agent Token 当作管理员 API Key。

Agent WebSocket 必须使用无 Origin 的客户端连接，或满足服务端 WebSocket Origin allowlist。服务端为 v2 WebSocket 启用 WebSocket 压缩协商；HTTP POST 还支持 `Content-Encoding: gzip`。

## 消息格式

请求和响应都使用 JSON-RPC 2.0：

```json
{
  "jsonrpc": "2.0",
  "method": "agent.report",
  "params": {},
  "id": 1
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "status": "success"
  }
}
```

服务端主动推送的事件通常不带 `id`，Agent 不需要为事件发送 JSON-RPC response；需要回传结果的事件使用对应的 Agent 方法，例如 `agent.exec` 对应 `agent.taskResult`，`agent.file` 对应 `agent.file.result`。Agent 主动发起的请求若带 `id`，服务端会返回同一个 `id`。

## Agent 发往服务端的方法

### `agent.report`

上报一次完整监控数据。服务端会以认证 Token 对应的 UUID 覆盖 `report.uuid`，并由服务端写入当前 `updated_at`；Agent 不应依赖客户端自填的这两个字段。

请求：

```json
{
  "jsonrpc": "2.0",
  "method": "agent.report",
  "params": {
    "report": {
      "cpu": { "name": "[REDACTED_CPU]", "cores": 4, "arch": "amd64", "usage": 12.5 },
      "ram": { "total": 8589934592, "used": 2147483648 },
      "swap": { "total": 0, "used": 0 },
      "load": { "load1": 0.2, "load5": 0.1, "load15": 0.1 },
      "disk": { "total": 107374182400, "used": 42949672960 },
      "network": { "up": 1024, "down": 2048, "totalUp": 100000, "totalDown": 200000 },
      "connections": { "tcp": 12, "udp": 3 },
      "uptime": 86400,
      "process": 120,
      "message": "",
      "updated_at": "2026-09-22T03:00:00Z"
    },
    "ack_event_ids": ["[REDACTED_EVENT_ID]"]
  },
  "id": 1
}
```

`ack_event_ids` 可选，用于确认之前收到的服务端事件。成功响应为：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "status": "success",
    "events": []
  }
}
```

`events` 最多返回 8 个排队事件。成功上报会写入指标库、更新运行时最新报告，并刷新 HTTP Agent 的在线状态。

### `agent.basicInfo`

保存 Agent 基本信息。`info` 是对象，字段沿用客户端模型，常用字段包括 `name`、`cpu_name`、`virtualization`、`arch`、`os`、`kernel_version`、`gpu_name`、`ipv4`、`ipv6`、`region`、`mem_total`、`swap_total`、`disk_total` 和 `version`。服务端会补写认证 UUID；未提供 IP 时可使用请求来源地址作为兜底。

```json
{
  "jsonrpc": "2.0",
  "method": "agent.basicInfo",
  "params": {
    "info": {
      "name": "edge-01",
      "os": "linux",
      "arch": "amd64",
      "cpu_cores": 4,
      "mem_total": 8589934592
    }
  },
  "id": 2
}
```

成功返回 `{ "status": "success" }`。字段名应使用 JSON 模型的 snake_case；不要上传 API Key、Agent Token 或密码。

### `agent.pingResult`

回传服务端下发的 Ping 任务结果。`value` 使用毫秒整数；负数表示丢包。`finished_at` 当前服务端只用于协议绑定，落库时间由服务端生成。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `task_id` | `uint` | Ping 任务 ID |
| `ping_type` | `string` | 探测类型 |
| `value` | `int` | 延迟毫秒；负数表示失败/丢包 |
| `finished_at` | RFC3339 时间 | Agent 完成时间 |

成功返回 `{ "status": "success" }`。

### `agent.taskResult`

回传 `agent.exec` 的命令执行结果：

```json
{
  "jsonrpc": "2.0",
  "method": "agent.taskResult",
  "params": {
    "task_id": "[REDACTED_TASK_ID]",
    "result": "command output",
    "exit_code": 0,
    "finished_at": "2026-09-22T03:00:02Z"
  },
  "id": 3
}
```

`finished_at` 为零值时服务端使用当前 UTC 时间。成功返回 `{ "status": "success" }`。命令输出可能包含敏感信息，Agent 和服务端日志都应按最小暴露原则处理。

### `agent.pull`

用于只上报事件确认并拉取离线期间排队的服务端事件。参数可省略：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `capabilities` | `string[]` | Agent 能力声明，当前服务端只接收并不强制解释 |
| `ack_event_ids` | `string[]` | 确认已处理事件 |
| `last_event_id` | `string` | Agent 保存的最后事件 ID，当前实现不以此替代 ack |

成功返回 `{ "events": Event[] }`。HTTP POST 最长等待约 25 秒；WebSocket 上调用时立即读取队列。建议 Agent 在收到事件并完成处理后，在下一次 `report` 或 `pull` 中携带 `ack_event_ids`。

### `agent.file.result`

回传 `agent.file` 文件控制操作的结果。`uuid` 必须与当前 Token 对应的 UUID 一致，`request_id` 必须原样回传：

```json
{
  "jsonrpc": "2.0",
  "method": "agent.file.result",
  "params": {
    "uuid": "[REDACTED_CLIENT_UUID]",
    "request_id": "[REDACTED_REQUEST_ID]",
    "ok": true,
    "result": { "name": "file.txt", "size": 123 }
  },
  "id": 4
}
```

失败时设置 `ok=false` 和 `error` 字符串。文件控制请求默认等待约 30 秒，搜索请求约 90 秒；未知或已过期的 `request_id` 返回 `-32004`。

## 服务端发往 Agent 的事件

服务端事件的基础形状是：

```json
{
  "jsonrpc": "2.0",
  "method": "agent.exec",
  "params": {}
}
```

| 方法 | params | Agent 行为 |
| --- | --- | --- |
| `agent.exec` | `{ "task_id": string, "command": string }` | 执行命令并回传 `agent.taskResult` |
| `agent.ping` | `{ "ping_task_id": uint, "ping_type": string, "ping_target": string }` | 执行探测并回传 `agent.pingResult` |
| `agent.terminal.request` | `{ "request_id": string }` | 通过 Agent Token 连接 `/api/clients/terminal?id=<request_id>`，之后转发终端 WebSocket 字节 |
| `agent.file` | `{ "uuid": string, "request_id": string, "op": string, "args": object }` | 执行元数据文件操作，并回传 `agent.file.result` |
| `agent.message` | `{ "type": string, "message": string, "data"?: any }` | 协议保留的消息事件；当前服务端核心未主动生成 |
| `agent.event` | `{ "type": string, "data"?: any }` | 协议保留的扩展事件；当前服务端核心未主动生成 |

`agent.message` 和 `agent.event` 的参数类型已经在协议包中定义，但当前 `handleV2RPC` 不把它们当作 Agent 上行方法处理；实现方不要把它们当成可向服务端提交的通用接口，除非部署的插件或未来版本明确声明支持。

## 事件队列和在线状态

事件投递优先走当前 WebSocket；没有长连接但 Agent 被识别为 v2 客户端时，会暂存到内存队列，等待下一次 `agent.pull`、`agent.report` 或重新建立 WebSocket。

| 项目 | 当前行为 |
| --- | --- |
| 队列容量 | 每个 UUID 最多 128 个事件，超出时保留较新的事件 |
| 普通事件 TTL | 5 分钟 |
| `agent.ping` TTL | 3 秒；过期 Ping 不应在恢复连接后执行 |
| `agent.file` TTL | 2 分钟 |
| HTTP POST 在线 TTL | 收到 `agent.report` 或 `agent.pull` 后约 35 秒 |
| WebSocket 在线状态 | 由连接生命周期维护 |
| 事件确认 | `ack_event_ids` 按事件 ID 删除队列中的对应事件 |
| 合并策略 | 同一 Ping 任务、同一终端请求的重复排队事件会合并 |

事件队列是进程内状态，不是持久队列。服务重启、进程崩溃或超过 TTL 后，Agent 应依赖下一次上报重新同步，而不能假设事件一定送达。

## 文件数据面

`agent.file` 只负责发起控制操作，文件内容通过一次性的 raw HTTP relay 传输。服务端生成 `transfer_id` 和 `transfer_token`，并把它们放入 `agent.file.args`：

- Agent 从服务端下载到 Agent 时，Agent 访问 `GET` 或无 body 的 `POST /api/clients/transfer/<transfer_id>`，读取响应字节并写入本地目标文件。
- Agent 从 Agent 上传到服务端时，Agent 以 `POST` 请求把准确长度的文件字节写入 `/api/clients/transfer/<transfer_id>` 请求体。
- 请求必须带 `X-Komari-Transfer-Token: [REDACTED_TRANSFER_TOKEN]`，也兼容 `transfer_token` 查询参数；服务端还会核对 Agent Token 对应的 UUID。

当前限制：

- 默认中继分块大小 25 MiB，可在 1–128 MiB 内协商。
- 单个远程上传上限 8 GiB；上传会话默认保留 15 分钟。
- 单次 raw transfer 令牌约 2 分钟有效，单个服务进程最多 32 个并发中继。
- 浏览器下载支持单一 `Range`、`If-Range`、`ETag` 和 `Last-Modified`；Agent 端必须严格遵守 `offset`、`length` 和 `Content-Length`。
- raw transfer 不返回 JSON；控制 RPC 结束和数据流结束是两个阶段，客户端应分别处理。

## 终端数据面

管理员创建 `/api/admin/client/:uuid/terminal` WebSocket 会话后，服务端通过 `agent.terminal.request` 通知 Agent。Agent 连接：

```text
wss://<host>/api/clients/terminal?id=<request_id>&token=[REDACTED_CLIENT_TOKEN]
```

Agent 端连接建立后不再发送 v2 JSON-RPC 消息，而是与浏览器端之间转发文本/二进制终端帧。已有会话可以在约 5 分钟保留窗口内重连；新建管理员终端会话需要敏感操作 2FA。

## 错误处理和重试

| 错误码 | 场景 |
| ---: | --- |
| `-32600` | `jsonrpc` 不是 `2.0` |
| `-32601` | 服务端不认识该 v2 方法 |
| `-32602` | `params` 无法绑定到协议结构 |
| `-32000` | 上报、指标写入或任务结果保存失败 |
| `-32001` | Agent Token 无效 |
| `-32004` | 文件操作未知或已过期 |

POST 在协议错误时通常返回 HTTP `400`；Token 无效返回 `401`。对 `agent.report`、`agent.basicInfo` 和结果上报可以使用带退避的重试，但要用新的 `id`，并避免重复执行 `agent.exec`、文件删除或文件移动等不可幂等操作。对于文件传输，优先根据 `request_id`、transfer 过期时间和 Agent 返回的错误判断是否能安全重试。
