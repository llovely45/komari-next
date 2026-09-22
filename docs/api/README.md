# API 总览

默认 API 基址为 `http://<host>:25775`。生产环境应通过 HTTPS/WSS 暴露服务，并将下面示例中的地址替换为实际域名。

## 接口类型

| 类型 | 入口 | 用途 |
| --- | --- | --- |
| 健康检查 | `GET /ping` | 返回纯文本 `pong` |
| REST/JSON | `/api/...` | 登录、主题、上传、下载、管理页面兼容接口 |
| JSON-RPC 2.0 over HTTP | `POST /api/rpc2` | 单条或批量 RPC 请求 |
| JSON-RPC 2.0 over WebSocket | `GET /api/rpc2` | 浏览器长连接和通知 |
| Agent v2 | `POST/GET /api/clients/v2/rpc` | Agent 上报和事件通道 |
| 原始 WebSocket | `GET /api/clients`、终端等 | 在线节点推送和交互式流 |

完整路由见 [HTTP 路由参考](./http-endpoints.md)，JSON-RPC 方法见 [JSON-RPC 方法参考](./json-rpc.md)，Agent 协议见 [Agent v2 协议](./agent-v2.md)。

## REST 响应格式

普通 REST handler 使用以下包装：

```json
{
  "status": "success",
  "message": "",
  "data": {}
}
```

失败时通常为：

```json
{
  "status": "error",
  "message": "Invalid credentials"
}
```

`data` 在结果为 `null` 时可能被省略。部分兼容路由使用特殊渲染：

- `WithRaw`：直接返回 RPC result，例如 `GET /api/me`。
- `WithFlat`：将 result 对象平铺到顶层，并保留 `status`，例如新增客户端和 Session 列表。
- 文件、图片、下载和 WebSocket 路由不使用上述 JSON 包装。

## JSON-RPC 请求和响应

`POST /api/rpc2` 接收 JSON-RPC 2.0 单条或批量请求：

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer [REDACTED_API_KEY]' \
  --data '{"jsonrpc":"2.0","method":"public:getVersion","params":{},"id":1}' \
  http://127.0.0.1:25775/api/rpc2
```

成功响应：

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

失败响应：

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

批量请求返回响应数组。省略 `id` 的 Notification 不会收到响应；WebSocket 上也不会为 Notification 发送回包。

`GET /api/rpc2` 必须完成 WebSocket Upgrade，之后每条文本消息都应是 JSON-RPC 请求。服务端会按请求顺序写回响应；连接关闭通常表示网络、Origin 校验或服务端错误。

## 错误码

| Code | 含义 | 典型 HTTP 映射 |
| ---: | --- | ---: |
| `-32700` | Parse error | 400 |
| `-32600` | Invalid request | 400 |
| `-32601` | Method not found | 500 |
| `-32602` | Invalid params | 400 |
| `-32603` | Internal error | 500 |
| `-32040` | Unauthenticated | 401 |
| `-32041` | Permission denied | 401 |
| `-32044` | Not found | 404 |
| `-32045` | Already exists | 409 |
| `-32010` | Cancelled | 408 |
| `-32011` | Deadline exceeded | 504 |
| `-32050` | Unimplemented | 501 |
| `-32051` | Unavailable | 503 |

HTTP 路由桥会把 RPC 错误转换为 REST 形状；直接访问 `/api/rpc2` 时保留 JSON-RPC `error` 对象。

## 通用约定

- 时间字段使用 RFC3339/JSON 时间格式，服务端内部通常按 UTC 处理。
- UUID 指节点或用户的 UUID；客户端 Token、Session Token 和 API Key 是不同凭据，不能互换。
- 公开接口会隐藏 `hidden` 节点，并清除公开响应中的 Token、版本、备注和 IP 等字段。
- 所有 API 响应由服务端设置 `Cache-Control: no-store`；调用方不应缓存包含凭据或实时状态的响应。
- 敏感操作需要 2FA 时，可把代码放到 `X-2FA-Code`、`X-Two-Factor-Code`、查询参数 `2fa_code`/`two_factor_code`/`otp`，或 JSON/RPC params 的同名字段中。
