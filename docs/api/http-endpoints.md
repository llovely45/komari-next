# HTTP 路由参考

以下路由来自 [web/router/router.go](../../web/router/router.go)。除特别说明外，`/api/admin/...` 需要管理员 Session 或 API Key；公开 JSON 路由仍会按私有站点和隐藏节点设置过滤结果。

## 基础和公开接口

| 方法 | 路径 | 处理方式 | 说明 |
| --- | --- | --- | --- |
| `GET`、其他 | `/ping` | 纯文本 | 返回 `pong`，不使用 JSON 包装 |
| `POST` | `/api/login` | REST | 用户名、密码和可选 2FA 登录，设置 `session_token` Cookie |
| `GET` | `/api/logout` | REST | 删除当前 Session，302 到 `/` |
| `GET` | `/api/oauth` | 重定向 | 跳转到已配置的 OAuth 提供商 |
| `GET` | `/api/oauth_callback` | 重定向/JSON | OAuth 回调并创建 Session |
| `GET` | `/api/plugin/:short/*filepath` | 文件 | 公开插件页面；仅允许 manifest 声明的 public 页面 |
| `GET`、`HEAD` | `/api/preview/client/:uuid/file/download` | 文件流 | 使用短期 `preview_token` 下载文件 |
| `GET` | `/api/clients` | WebSocket | 发送 `get` 或 `get <uuid>`，返回在线节点和最近报告 |
| `GET` | `/api/me` | `public:getMe` + raw | 返回当前登录态，不带 REST 包装 |
| `GET` | `/api/nodes` | `public:getNodesInformation` | 返回公开可见节点列表 |
| `GET` | `/api/public` | `public:getPublicSettings` | 返回公开站点设置 |
| `GET` | `/api/version` | `public:getVersion` | 返回版本和构建 hash |
| `GET` | `/api/recent/:uuid` | `public:getClientRecentRecords` | 返回指定节点的内存中最近报告 |
| `GET` | `/api/records/load` | `public:getRecordsByUUID` | 查询负载历史，查询参数为 `uuid`、`load_type`、`hours` |
| `GET` | `/api/records/ping` | `public:getPingRecords` | 查询 Ping 历史，查询参数为 `uuid`、`task_id`、`hours` |
| `GET` | `/api/task/ping` | `public:getPublicPingTasks` | 返回公开 Ping 任务摘要 |
| `GET`、`POST` | `/api/rpc2` | JSON-RPC 2.0 | GET 升级 WebSocket，POST 支持单条/批量请求 |

公开路径的详细参数和响应见 [JSON-RPC 方法参考](./json-rpc.md#公开方法)；传统路径只是同一 RPC 方法的兼容映射。

## Agent 接口

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/clients/register` | `Authorization: Bearer <auto_discovery_key>` | 自动创建客户端，查询参数 `name` 可选 |
| `POST` | `/api/clients/v2/rpc` | Agent Token | 接收一条 v2 JSON-RPC 请求，可 gzip |
| `GET` | `/api/clients/v2/rpc` | Agent Token | 升级 WebSocket，收发 v2 JSON-RPC 消息 |
| `GET`、`POST` | `/api/clients/transfer/:id` | Agent Token + transfer token | Agent 文件数据面；由文件控制 RPC 创建 transfer |
| `GET` | `/api/clients/terminal` | Agent Token | Agent 端连接终端转发会话，查询参数 `id` 必填 |

Agent v2 消息和文件数据面说明见 [Agent v2 协议](./agent-v2.md)。

## 管理员通用接口

### 系统、认证和上传

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/admin/download/backup` | 下载当前数据备份 |
| `POST` | `/api/admin/upload/init` | 初始化备份、插件或主题分片上传；body 为 `purpose`、`size`、`filename` |
| `POST` | `/api/admin/upload/chunk` | `multipart/form-data` 上传分片；字段为 `upload_id`、`chunk_index`、`chunk_data` |
| `POST` | `/api/admin/upload/merge` | 合并上传并执行对应 finalizer；body 为 `upload_id` |
| `POST` | `/api/admin/upload/cancel` | 取消上传；body 为 `upload_id` |
| `GET` | `/api/admin/test/geoip` | 测试 GeoIP；查询参数 `ip` 可选 |
| `POST` | `/api/admin/test/sendMessage` | 发送测试通知 |
| `POST` | `/api/admin/update/mmdb` | 更新 GeoIP 数据库 |
| `POST` | `/api/admin/update/user` | 修改用户；修改密码时需要敏感操作 2FA |
| `PUT` | `/api/admin/update/favicon` | 上传 favicon 原始二进制，最大 5 MiB |
| `POST` | `/api/admin/update/favicon` | 删除 favicon |
| `GET` | `/api/admin/2fa/generate` | 返回二维码 PNG，并设置短期 `2fa_secret` Cookie |
| `POST` | `/api/admin/2fa/enable` | 查询参数 `code` 校验并启用 2FA |
| `POST` | `/api/admin/2fa/disable` | 关闭 2FA，需要敏感操作 2FA |
| `GET` | `/api/admin/oauth2/bind` | 设置绑定 Cookie 并跳转 OAuth |
| `POST` | `/api/admin/oauth2/unbind` | 解绑当前用户外部账号 |

分片上传的固定分片大小为 5 MiB，支持的 `purpose` 是 `backup`、`plugin`、`theme`。备份合并后会安排重启并在启动时恢复，调用方应等待服务重新上线。

### 主题和主题市场

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/admin/theme/list` | 列出已安装主题 |
| `POST` | `/api/admin/theme/delete` | 删除主题 |
| `GET` | `/api/admin/theme/set` | 设置当前主题 |
| `POST` | `/api/admin/theme/update` | 更新主题信息/配置 |
| `POST` | `/api/admin/theme/import` | 导入主题配置 |
| `POST` | `/api/admin/theme/settings` | 更新主题管理设置 |
| `GET` | `/api/admin/theme/market/sources` | 列出主题市场源 |
| `POST` | `/api/admin/theme/market/sources` | 创建主题市场源 |
| `PUT` | `/api/admin/theme/market/sources/:id` | 更新主题市场源 |
| `DELETE` | `/api/admin/theme/market/sources/:id` | 删除主题市场源 |
| `GET` | `/api/admin/theme/market/catalog` | 获取主题市场目录 |
| `POST` | `/api/admin/theme/market/install` | 从市场安装主题 |

主题的压缩包安装使用通用 `/api/admin/upload/*`，不要把完整二进制放进 JSON-RPC body。

### 任务、设置和数据库

| 方法 | 路径 | RPC/说明 |
| --- | --- | --- |
| `GET` | `/api/admin/task/all` | `admin:getTasks` |
| `POST` | `/api/admin/task/exec` | `admin:exec`，需要敏感操作 2FA |
| `GET` | `/api/admin/task/:task_id` | `admin:getTaskById` |
| `GET` | `/api/admin/task/:task_id/result` | `admin:getTaskResultsByTaskId` |
| `GET` | `/api/admin/task/:task_id/result/:uuid` | `admin:getSpecificTaskResult` |
| `GET` | `/api/admin/task/client/:uuid` | `admin:getTasksByClientId` |
| `GET` | `/api/admin/settings/` | `admin:getSettings` |
| `POST` | `/api/admin/settings/` | `admin:editSettings`，支持部分更新 |
| `GET` | `/api/admin/settings/xtermjs` | `admin:getXtermjsSettings` |
| `POST` | `/api/admin/settings/xtermjs` | `admin:setXtermjsSettings` |
| `GET` | `/api/admin/settings/oidc` | `admin:getOidcProvider`，查询参数 `provider` 可选 |
| `POST` | `/api/admin/settings/oidc` | `admin:setOidcProvider` |
| `GET` | `/api/admin/settings/message-sender` | `admin:getMessageSenderProvider`，查询参数 `provider` 可选 |
| `POST` | `/api/admin/settings/message-sender` | `admin:setMessageSenderProvider` |
| `GET` | `/api/admin/database/size` | `admin:getDatabaseSize` |
| `POST` | `/api/admin/database/vacuum` | `admin:vacuumDatabase`，串行执行空间回收 |
| `GET` | `/api/admin/logs` | `admin:getLogs`，查询参数 `limit`、`page`、`msg_type` |

### 客户端、文件和终端

| 方法 | 路径 | RPC/说明 |
| --- | --- | --- |
| `POST` | `/api/admin/client/add` | `admin:addClient`，成功结果平铺为 `status`、`uuid`、`token` |
| `GET` | `/api/admin/client/list` | `admin:listClients`，raw 客户端列表 |
| `GET` | `/api/admin/client/:uuid` | `admin:getClient`，raw 客户端对象 |
| `POST` | `/api/admin/client/:uuid/edit` | `admin:editClient`，body 是部分客户端字段 |
| `POST` | `/api/admin/client/:uuid/remove` | `admin:removeClient` |
| `GET` | `/api/admin/client/:uuid/token` | `admin:getClientToken`，结果平铺 |
| `POST` | `/api/admin/client/order` | `admin:orderClients`，body 为 `uuid -> weight` 映射 |
| `GET` | `/api/admin/client/:uuid/terminal` | 浏览器端终端 WebSocket；新建会话时需要 2FA |
| `GET` | `/api/admin/terminal/sessions` | 返回当前管理员拥有且在线的终端会话；`data` 项包含 `request_id`、`uuid`、`client_name` |
| `POST` | `/api/admin/terminal/sessions/:request_id/input` | body 为 `{"data":"..."}`；向当前管理员拥有的在线会话写入原始 UTF-8 输入，解码后单帧最大 1 MiB，不自动追加回车；不存在会话返回 `404`，会话暂不可写返回 `409` |
| `POST` | `/api/admin/client/:uuid/file/upload` | 远程文件上传控制面，查询 `operation=init|chunk|merge|cancel` |
| `GET`、`HEAD` | `/api/admin/client/:uuid/file/download` | 远程文件下载；查询 `path`，支持单 Range |
| `GET` | `/api/admin/client/:uuid/file/preview-token` | 创建 10 分钟文件预览 Token；查询 `path` |
| `POST` | `/api/admin/record/clear` | `admin:clearRecords`，删除负载记录 |
| `POST` | `/api/admin/record/clear/all` | `admin:clearAllRecords`，删除负载和 Ping 记录 |

文件下载响应支持 `Range`、`If-Range`、`ETag`、`Last-Modified` 和 `inline=1`。实际文件字节通过受限的 transfer relay 流式转发，不会包装成 JSON。

### Session、剪贴板、插件和通知

| 方法 | 路径 | RPC/说明 |
| --- | --- | --- |
| `GET` | `/api/admin/session/get` | `admin:getSessions`，返回当前 Session 和列表 |
| `POST` | `/api/admin/session/remove` | `admin:deleteSession` |
| `POST` | `/api/admin/session/remove/all` | `admin:deleteAllSessions` |
| `GET` | `/api/admin/clipboard/:id` | `admin:getClipboard` |
| `GET` | `/api/admin/clipboard` | `admin:listClipboard` |
| `POST` | `/api/admin/clipboard` | `admin:createClipboard` |
| `POST` | `/api/admin/clipboard/:id` | `admin:updateClipboard` |
| `POST` | `/api/admin/clipboard/remove` | `admin:batchDeleteClipboard` |
| `POST` | `/api/admin/clipboard/:id/remove` | `admin:deleteClipboard` |
| `GET` | `/api/admin/plugin/list` | `admin:listPlugins` |
| `POST` | `/api/admin/plugin/enabled` | `admin:setPluginEnabled`；权限变化可能先返回 `requires_approval` |
| `GET` | `/api/admin/plugin/logs` | `admin:getPluginLogs`，查询 `short` |
| `POST` | `/api/admin/plugin/delete` | `admin:deletePlugin` |
| `GET` | `/api/admin/plugin/configuration` | `admin:getPluginConfiguration`，查询 `short` |
| `POST` | `/api/admin/plugin/configuration` | `admin:setPluginConfiguration` |
| `GET` | `/api/admin/plugin/market/sources` | 列出插件市场源 |
| `POST` | `/api/admin/plugin/market/sources` | 创建插件市场源 |
| `PUT` | `/api/admin/plugin/market/sources/:id` | 更新插件市场源 |
| `DELETE` | `/api/admin/plugin/market/sources/:id` | 删除插件市场源 |
| `GET` | `/api/admin/plugin/market/catalog` | 获取插件市场目录 |
| `POST` | `/api/admin/plugin/market/install` | 从市场安装插件 |
| `GET` | `/api/admin/plugin/:short/*filepath` | 插件管理页静态文件 |
| `GET` | `/api/admin/notification/offline` | `admin:listOfflineNotifications` |
| `POST` | `/api/admin/notification/offline/edit` | `admin:editOfflineNotification` |
| `POST` | `/api/admin/notification/offline/enable` | `admin:enableOfflineNotification` |
| `POST` | `/api/admin/notification/offline/disable` | `admin:disableOfflineNotification` |
| `GET` | `/api/admin/notification/load/` | `admin:getAllLoadNotifications` |
| `POST` | `/api/admin/notification/load/add` | `admin:addLoadNotification` |
| `POST` | `/api/admin/notification/load/delete` | `admin:deleteLoadNotification` |
| `POST` | `/api/admin/notification/load/edit` | `admin:editLoadNotification` |

### Ping 任务

| 方法 | 路径 | RPC/说明 |
| --- | --- | --- |
| `GET` | `/api/admin/ping/` | `admin:getAllPingTasks` |
| `POST` | `/api/admin/ping/add` | `admin:addPingTask` |
| `POST` | `/api/admin/ping/delete` | `admin:deletePingTask` |
| `POST` | `/api/admin/ping/edit` | `admin:editPingTask` |
| `POST` | `/api/admin/ping/order` | `admin:orderPingTask` |

## pprof 诊断接口

pprof 路由挂在管理员保护组下，不会注册未认证的 `/debug/pprof`：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/admin/pprof/summary` | 运行时内存、goroutine 和 profile 列表 |
| `GET` | `/api/admin/pprof/profile` | 有界 CPU profile，默认 10 秒，允许 1–30 秒 |
| `GET` | `/api/admin/pprof/trace` | 有界 runtime trace |
| `GET` | `/api/admin/pprof/allocs` | allocations profile |
| `GET` | `/api/admin/pprof/block` | block profile |
| `GET` | `/api/admin/pprof/goroutine` | goroutine profile |
| `GET` | `/api/admin/pprof/heap` | heap profile |
| `GET` | `/api/admin/pprof/mutex` | mutex profile |
| `GET` | `/api/admin/pprof/threadcreate` | threadcreate profile |

诊断接口可能返回二进制 profile 或文本预览，生产环境应限制管理员 API 的网络暴露。

## 受限启动向导

以下路由只在对应启动阶段临时注册，普通运行时访问会返回 404。

| 模式 | 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- | --- |
| 安装 | `GET` | `/api/install/status` | 无 | 查看是否仍需安装 |
| 安装 | `POST` | `/api/install/complete` | 无 | 创建首个账户并保存站点设置；主库 DSN 已在启动参数中提供 |
| 安装 | `POST` | `/api/install/upload/init` | 无 | 初始化备份上传 |
| 安装 | `POST` | `/api/install/upload/chunk` | 无 | 上传备份分片 |
| 安装 | `POST` | `/api/install/upload/merge` | 无 | 合并备份并安排恢复重启 |
| 安装 | `POST` | `/api/install/upload/cancel` | 无 | 取消备份上传 |
| 迁移 | `GET` | `/api/admin/database-migration/auth` | 无 | 返回登录方式和迁移模式 |
| 迁移 | `GET` | `/api/admin/database-migration/status` | admin | 查看迁移进度 |
| 迁移 | `POST` | `/api/admin/database-migration/start` | admin | 结构升级模式无需 body；旧监控表导入模式可提交 `confirm_large_dataset` |
| 迁移 | `POST` | `/api/admin/database-migration/discard` | admin | 仅结构升级模式可用；放弃历史指标并重建结构 |

安装和迁移请求都有严格 body 大小/字段校验；不要在脚本中假设这些路由永久存在。当前 cmd.RunServer 不挂载 /database-recovery 恢复向导，指标 store 初始化失败时服务会在启动阶段退出，而不是开放一个 DSN 修改页面。
