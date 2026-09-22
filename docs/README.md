# Komari 文档

本目录记录当前仓库实现的部署、架构和接口约定。接口说明以当前分支源码为准，重点核对了 `web/router/router.go`、`web/rpc/jsonrpc/`、`protocol/v2/` 和 `web/api/`。

## 从这里开始

| 目标 | 文档 |
| --- | --- |
| 了解当前运行架构 | [技术架构](./technical-overview.md) |
| 配置监听地址、数据库和缓存 | [配置参考](./configuration.md) |
| 调用 HTTP、WebSocket 和 JSON-RPC API | [API 总览](./api/README.md) |
| 处理登录、Session、API Key 和 Agent Token | [认证与权限](./api/authentication.md) |
| 查找所有 HTTP 路由 | [HTTP 路由参考](./api/http-endpoints.md) |
| 查找 JSON-RPC 方法和参数 | [JSON-RPC 方法参考](./api/json-rpc.md) |
| 对接监控 Agent | [Agent v2 协议](./api/agent-v2.md) |

## 文档状态

- 当前实现文档与源码同步维护，不承诺与其他版本分支完全兼容。
- 当前仓库没有正式的 OpenAPI/Swagger schema；`api/` 下的文档是可直接使用的人工维护接口契约。
- `optimization-direction.md` 和 `technical-design-pgsql-redis-modular-plugin.md` 是演进设计文档，描述目标架构和阶段边界，不应当替代当前实现文档。
- 当路由、RPC 方法、认证方式或请求字段发生变化时，应同时更新对应 API 文档，并运行文档中的验证命令。

## 运行入口

默认监听地址是 `0.0.0.0:25775`。启动后可以先检查：

```bash
curl -fsS http://127.0.0.1:25775/ping
```

预期响应为纯文本 `pong`。首次启动或数据库迁移时，程序会进入临时的受限向导；详见[技术架构](./technical-overview.md#启动阶段与受限模式)。

## 源码索引

- 路由注册：[web/router/router.go](../web/router/router.go)
- JSON-RPC 传输：[web/rpc/jsonrpc/transport.go](../web/rpc/jsonrpc/transport.go)
- JSON-RPC 权限：[pkg/rpc/permission.go](../pkg/rpc/permission.go)
- Agent v2 入口：[web/api/client/report_v2.go](../web/api/client/report_v2.go)
- Agent v2 数据结构：[protocol/v2/jsonrpc.go](../protocol/v2/jsonrpc.go)
- 服务端生命周期：[cmd/server.go](../cmd/server.go) 和 [internal/server/runtime.go](../internal/server/runtime.go)
