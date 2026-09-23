# Komari Next 命令剪贴板插件

在 Komari Next 的终端页管理常用命令片段，并将片段发送到当前活动标签对应的终端。发送时插件会像旧版前端一样在命令末尾追加回车。

## 兼容要求

- Komari Next 后端包含 `GET /api/admin/terminal/sessions` 和 `POST /api/admin/terminal/sessions/:request_id/input` 两个通用终端接口。
- 最低要求 Komari Next `1.0.12`；该版本开始包含终端输入接口。
- 已登录管理员至少打开一个已连接的终端会话。
- 插件继续使用现有 `/api/admin/clipboard` 接口管理片段。

## 权限

插件声明 `allowHTMLInject`，用于把终端页界面装入应用页面。启用时，Komari Next 会按插件权限流程要求管理员批准 HTML 注入权限。插件只通过同源管理员 API 读写片段和发送终端输入。

## 打包与安装

在仓库根目录运行：

```sh
python3 scripts/package-command-clipboard.py
```

生成的 ZIP 位于 `dist/command-clipboard-<version>.zip`。在 Komari Next 的插件管理页上传该 ZIP，批准 HTML 注入权限并启用插件。升级后端并安装插件后，打开终端页即可使用右下角的命令片段图标。
