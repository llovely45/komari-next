# Komari Next 节点备份恢复插件

导出服务器列表节点配置为 JSON，并在恢复时按 IPv4 将备份数据合并到当前节点。

## 行为

- 导出节点记录中的 UUID、RPC Token、名称、账单字段、流量限制、标签、备注、分组及其他节点字段。
- IPv4 与当前节点相同时，只更新名称、区域显示、备注、权重、价格、账单周期、自动续费、币种、到期时间、分组、标签、隐藏状态和流量限制字段。保留当前 UUID、RPC Token、IPv4、IPv6 和 Agent 上报的硬件/系统信息。
- 没有相同 IPv4 时，按备份 UUID 和 RPC Token 新增节点，以便已有 Agent 继续认证。
- 备份或当前列表中的 IPv4 有歧义，以及 UUID / RPC Token 已被不同节点占用时，该节点会跳过并列出原因。批次中其他无冲突节点仍会恢复。
- 历史监控记录不在节点数据备份内。

备份文件包含 RPC Token，应按凭据文件妥善保管。导入仅接受本插件生成的 `komari-client-backup` v1 JSON 文件。

## 安装

本插件需要 Komari Next `1.0.20` 或更新版本提供的 `admin:restoreClients` RPC（本仓库已添加该方法）。源码位于 `main`；安装包和市场目录发布在 `page` 分支的 `site/plugin-market/`。

```sh
python3 scripts/package-client-backup.py \
  --market-dir /path/to/komari-next-page-worktree/site/plugin-market
```

脚本会在目标目录生成 `client-backup-<version>.zip`，并更新同目录的 `v1.json`。检查变更后，在 `page` 工作树中只提交 ZIP 和 `v1.json` 并推送 `page` 分支。不要把安装包提交到 `main` 的 `dist/` 或仓库根目录的市场目录。推送完成后，Cloudflare Pages 会从 `site/plugin-market/` 提供市场目录和安装包。启用后从插件侧栏打开“节点备份恢复”。
