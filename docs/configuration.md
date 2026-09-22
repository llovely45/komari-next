# 配置参考

本页记录当前分支正常启动时的 CLI/环境变量，以及管理员 API 保存的运行时配置。完整的管理员设置对象由 `admin:getSettings` 返回，接口用法见 [JSON-RPC 方法参考](./api/json-rpc.md)。

> [!IMPORTANT]
> 当前正常启动只使用 PostgreSQL。`KOMARI_DB_DSN` 或 `--db-dsn` 必须提供可用的 PostgreSQL 连接串；不会自动回退到 SQLite。指标表不再使用独立的指标库 DSN，而是使用同一个 PostgreSQL 连接池。

## 启动参数

CLI 参数优先级高于对应环境变量；未设置监听地址时使用默认值。当前用户可用的启动配置如下：

| 参数 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen`, `-l` | `KOMARI_LISTEN` | `0.0.0.0:25775` | HTTP/WebSocket 监听地址 |
| `--db-dsn` | `KOMARI_DB_DSN` | 空 | PostgreSQL 连接串，同时供主库和指标表使用；运行时必填 |

示例：

~~~bash
KOMARI_LISTEN=127.0.0.1:25775 \
KOMARI_DB_DSN='postgres://komari:[REDACTED_PASSWORD]@127.0.0.1:5432/komari?sslmode=require' \
./komari server
~~~

也可以使用 CLI 参数覆盖环境变量：

~~~bash
./komari server \
  --listen 127.0.0.1:25775 \
  --db-dsn 'postgres://komari:[REDACTED_PASSWORD]@127.0.0.1:5432/komari?sslmode=require'
~~~

`--db-type`、`--database`、`KOMARI_DB_TYPE` 和 `KOMARI_REDIS_URL` 不属于当前正常启动合同；`--redis-url` 也没有注册。源码中保留的 SQLite/旧配置变量只服务于兼容测试或历史迁移辅助代码，不应作为新部署配置使用。

不要把真实密码、API Key、Token 或完整 DSN 提交到 shell 历史、日志或文档中。生产环境应通过受限权限的环境文件、Secret 注入或同等安全机制提供 DSN。

## PostgreSQL 和指标表

主数据库由 GORM 管理，当前 PostgreSQL 连接池默认设置为最多 25 个打开连接、5 个空闲连接。控制面表和指标表位于同一个 PostgreSQL 数据库，并通过 `metric_` 表名前缀区分。

指标库不再支持通过管理员设置切换独立 SQLite/MySQL/PostgreSQL 后端。三个独立 backend 配置键会在启动时清理，管理员设置请求中携带它们也会被忽略：

| 旧配置键 | 当前行为 |
| --- | --- |
| `metric_db_driver` | 已退役；不再决定运行时驱动 |
| `metric_db_dsn` | 已退役；不再保存或切换指标库连接 |
| `metric_migration_target` | 已退役；不再作为正常运行配置 |
| `metric_store_enabled` | 已废弃；指标存储始终启用，但该旧键不参与运行时判定 |

## 指标存储设置

以下设置仍由管理员 API 保存，并影响共享 PostgreSQL 指标表或 rollup 行为：

| 配置键 | 默认值 | 说明 |
| --- | ---: | --- |
| `metric_table_prefix` | `metric_` | 指标表名前缀 |
| `metric_rollup_minute_retention_minutes` | `600` | 1 分钟 rollup 保留窗口，单位为分钟 |
| `metric_rollup_five_minute_retention_minutes` | `3000` | 5 分钟 rollup 保留窗口，单位为分钟 |
| `metric_rollup_hour_retention_hours` | `600` | 1 小时 rollup 保留窗口，单位为小时 |

rollup 配置必须是正整数。原始采样的内存窗口固定为 10 分钟，日级 bucket 作为终端层保留；各指标自己的 `retention_days` 仍由指标定义控制。修改 `metric_table_prefix` 会切换到另一组表名，生产环境应先备份并确认迁移方案。

`metric_max_open_conns` 和 `metric_max_idle_conns` 仍可能出现在旧客户端的设置对象中，但当前共享 PostgreSQL 路径不会创建独立指标连接池，也不会使用这两个字段覆盖主库连接池参数。新客户端不应依赖它们调优连接数。

历史数据不会通过切换独立指标库完成，运行时也没有指标库 DSN 迁移 RPC。当前启动向导处理两类迁移：旧版主库中的 legacy monitoring 表导入 `metric_*` 表，以及旧指标表结构升级。向导只在受限启动阶段开放，接口见 [HTTP 路由参考](./api/http-endpoints.md#受限启动向导)。

## Redis 缓存

Redis 是当前正常启动的本地依赖，不是主数据源。应用固定连接：

~~~text
redis://127.0.0.1:6379/0
~~~

Docker 镜像的 [`docker-entrypoint.sh`](../docker-entrypoint.sh) 会在容器内启动 Redis，并只绑定回环地址，不需要也不应该把 Redis 端口暴露到宿主机。直接运行二进制或源码时，需要自行确保该地址已有 Redis 服务；Redis 不可达会使启动在缓存初始化阶段失败。

缓存模块只负责可丢失的热点数据。已运行的读路径在缓存后端错误时可以回源 PostgreSQL，但缓存不可用不代表可以跳过应用启动依赖检查。

## 首次安装

首次启动且主库中没有用户时，普通路由不会启动，服务会进入 `/install`。主库和 Redis 必须先可用；安装提交至少需要：

- 用户名：1 至 64 个字符。
- 密码：8 至 256 个字符，且同时包含大写字母、小写字母和数字。
- 站点名称：1 至 100 个字符。
- 站点描述：最多 1000 个字符。

安装接口不会再接收或保存指标库 DSN。安装完成后服务自动切换到普通路由。安装向导的完整接口见 [HTTP 路由参考](./api/http-endpoints.md#受限启动向导)。

## 运行时安全配置

这些配置通常通过管理员设置 API 保存：

| 配置键 | 作用 |
| --- | --- |
| `api_key` | 管理 API Key。有效值至少 12 个字符，调用时使用 `Authorization: Bearer <key>` |
| `auto_discovery_key` | Agent 自动注册使用的独立密钥，不等同于 `api_key` |
| `private_site` | 是否要求访客先登录 |
| `cors_origin_check_enabled` | 是否启用 API Origin 校验 |
| `cors_allowed_origins` | 允许的 Origin 列表，逗号或换行分隔 |
| `ws_origin_check_enabled` | 是否校验 WebSocket Origin |
| `ws_allowed_origins` | WebSocket 允许列表 |
| `disable_password_login` | 是否关闭密码登录 |
| `o_auth_enabled` / `o_auth_provider` | OAuth 登录开关和提供商 |
| `visitor_audit_enabled` | 是否写入公开访客事件审计日志 |

变更 CORS 或 WebSocket Origin 配置前，应确认管理端来源仍在允许范围内，否则浏览器请求会收到 `403`。

## Docker

镜像默认监听容器内 `25775`，需要外部 PostgreSQL；Redis 由镜像入口脚本在容器内启动。生产部署至少持久化 `/app/data`，该目录用于主题、插件、插件数据和备份归档，主库与指标表在 PostgreSQL 中：

~~~bash
docker run -d \
  --name komari \
  --restart unless-stopped \
  -p 25775:25775 \
  -e KOMARI_DB_DSN='postgres://komari:[REDACTED_PASSWORD]@postgres.example:5432/komari?sslmode=require' \
  -v "$PWD/komari-data:/app/data" \
  ghcr.io/komari-monitor/komari:<version>
~~~

镜像的启动命令是 `/app/docker-entrypoint.sh`，它再启动 `/app/komari server`。若使用自建二进制而不是该镜像，需要单独运行 Redis，并提供 `KOMARI_DB_DSN`。

## 数据目录

相对路径以进程工作目录为基准。正常 PostgreSQL 部署不会创建本地 `komari.db` 或 `metrics.db`：

| 路径 | 内容 |
| --- | --- |
| `data/theme/` | 用户主题数据 |
| `data/plugin/` | 插件包和插件运行数据 |
| `data/plugin-data/` | 插件持久化目录 |
| `data/backup/` | 版本升级或恢复过程中生成的备份归档 |
| `data/metrics.db` | 旧版本可能遗留的文件；当前正常运行不会自动读取或写入 |

生产环境仍应持久化整个 `data/` 目录，以保留主题、插件和备份；PostgreSQL 需要单独执行备份。
