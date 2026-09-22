# 认证与权限

Komari 当前支持匿名访客、管理员 Session、管理员 API Key 和 Agent Token 四种身份。身份识别顺序是 API Key、Session Cookie、Agent Token、匿名；同一个请求同时携带多种凭据时，优先级更高的身份生效。

## 管理员 Session

### 登录

```bash
curl -i -c cookies.txt \
  -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"[REDACTED_PASSWORD]"}' \
  http://127.0.0.1:25775/api/login
```

成功时服务端设置 `HttpOnly` 的 `session_token` Cookie，并返回：

```json
{
  "status": "success",
  "message": "",
  "data": {
    "set-cookie": {
      "session_token": "[REDACTED_SESSION_TOKEN]"
    }
  }
}
```

启用 2FA 的账户必须额外提交 `2fa_code`：

```json
{
  "username": "admin",
  "password": "[REDACTED_PASSWORD]",
  "2fa_code": "123456"
}
```

常见失败状态：密码登录关闭为 `403`，请求字段缺失为 `400`，凭据或 2FA 错误为 `401`。

### 注销

```bash
curl -i -b cookies.txt http://127.0.0.1:25775/api/logout
```

该接口删除当前 Session、清除 Cookie，并重定向到 `/`。

## API Key

API Key 在管理员设置中的 `api_key` 字段保存，至少需要 12 个字符。调用时使用：

```bash
curl -fsS \
  -H 'Authorization: Bearer [REDACTED_API_KEY]' \
  http://127.0.0.1:25775/api/admin/client/list
```

API Key 是 `admin` 主体，适合自动化管理调用；它不是 Agent Token，也不能用于自动注册客户端。API Key 请求可绕过 WebSocket Origin 检查，但仍应只通过 HTTPS/WSS 传输。

## Agent Token

每个客户端有独立 Token。v2 接口推荐使用查询参数：

```text
GET /api/clients/v2/rpc?token=[REDACTED_CLIENT_TOKEN]
POST /api/clients/v2/rpc?token=[REDACTED_CLIENT_TOKEN]
```

POST 请求也可以在 JSON body 中传 `token`；`Authorization` 查询参数是兼容别名。不要把 Agent Token 当作 `Authorization: Bearer <api_key>` 使用。

自动注册使用另一把 `auto_discovery_key`：

```bash
curl -fsS \
  -H 'Authorization: Bearer [REDACTED_AUTO_DISCOVERY_KEY]' \
  'http://127.0.0.1:25775/api/clients/register?name=edge-01'
```

返回的 `uuid` 和 `token` 应只交给对应 Agent 保存。

## 角色与命名空间

| 命名空间/接口 | 允许主体 | 说明 |
| --- | --- | --- |
| `public:*`、`common:*` | guest 及更高角色 | 公开读接口；仍会过滤隐藏节点和敏感字段 |
| `admin:*` | admin | 管理员 RPC；API Key 和管理员 Session 均可用 |
| Agent v2 | client | 使用客户端 Token，面向 Agent 上报/控制 |
| `/api/admin/...` | admin | 路由层先做管理员角色检查 |

管理员角色和 Agent 角色是正交主体。管理员身份不会自动变成 Agent 身份，Agent Token 也不能调用管理员接口。

## 敏感操作和 2FA

当管理员账户启用 2FA 时，以下操作会要求当前请求附带一次性代码：

- `admin:exec`，以及兼容路由 `POST /api/admin/task/exec`。
- 新建管理员终端会话 `GET /api/admin/client/:uuid/terminal`。
- 修改管理员密码 `POST /api/admin/update/user`。
- 关闭 2FA `POST /api/admin/2fa/disable`。

API Key 调用不要求用户 TOTP，但仍必须保护 API Key 本身。终端重连已有会话时，会校验原会话所有者，不要求重新提交当前 TOTP。

## 私有站点和临时分享

启用 `private_site` 后，匿名用户访问普通 `api` 资源会收到 `401`。登录页需要的版本、公开设置和当前登录态接口仍然保留。带有效 `temp_key` Cookie 的临时分享访问可以继续读取允许的公开数据，但不会获得管理员权限。

## CORS 与 WebSocket Origin

API CORS 和 WebSocket Origin 校验是两套设置：

- API：`cors_origin_check_enabled`、`cors_allowed_origins`。
- WebSocket：`ws_origin_check_enabled`、`ws_allowed_origins`。

浏览器客户端应使用与服务 Host 匹配的 Origin，或把完整 Origin/Host 加入允许列表。跨域带 `Authorization` 的预检请求必须确保服务端配置允许该来源；否则会返回 `403`。
