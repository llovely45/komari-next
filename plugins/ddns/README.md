# Komari Next 动态 DNS 插件

Go 编写并编译为 Go 1.25 `wasip1/wasm` 模块，由 Komari 的 Wazero 宿主运行。插件把 Komari 节点保存或手动指定的 IPv4 / IPv6 地址同步到 Cloudflare 或华为云国际站公网 DNS Zone。

## 构建与安装

在仓库根目录运行：

```sh
python3 scripts/package-ddns.py
```

最低要求 Komari Next `1.0.20`。脚本编译 `plugins/ddns` 为 WASI 模块、更新 manifest 的 `entrySha256`，并生成对应版本的 ZIP 和 SHA-256 文件。上传 ZIP 后，在插件管理页批准插件申请的 Go RPC、插件 RPC 路由和 HTTPS 网络能力，再启用插件并打开「动态 DNS」管理页。插件 ID 沿用 `cloudflare-ddns`，升级会替换旧插件文件并保留插件数据。

Go 插件没有宿主文件系统挂载；配置和运行状态通过宿主受限存储接口写入 `data/plugin-data/cloudflare-ddns`。宿主对每个插件限制单文件 2 MiB、存储总量 128 MiB，并拒绝路径穿越和符号链接。网络接口只允许访问公网 HTTPS 443，且不跟随 HTTP 重定向。读取节点时宿主只向插件提供 UUID、名称、IPv4、IPv6 和分组字段。

## 配置凭据

- **Cloudflare**：推荐创建 API Token，只授予目标 Zone 的 Zone Read、DNS Read 和 DNS Edit。也可使用账户邮箱和 Global API Key。
- **华为云国际站**：填写 Access Key、Secret Key 和公网 DNS Zone 所在区域代码，例如 `ap-southeast-1`。IAM 用户需要能够查询、创建和更新该区域公网 DNS 记录集。签名使用华为云国际站 AK/SK 协议，不支持华为云中国站账号。

凭据和规则保存在插件私有存储的 `config.json`。管理页只会收到凭据是否已保存的标记，不会回传 Token、Key 或 Secret；凭据输入框留空会保留已保存值。备份 Komari 数据目录时会包含这些配置。

点「验证凭据」只会读取 Zone 列表，不会修改 DNS。保存规则后，插件每分钟检查一次到期任务。

## 解析规则

每条规则设置服务商、完整域名、A / AAAA 类型、一个或多个来源节点或手动指定 IP、检查间隔和 TTL。Cloudflare 同一域名和类型只能有一条规则；华为云按解析线路区分规则，所以同域名、同类型可以为不同线路分别配置。

- A 使用节点保存的 IPv4 或手动输入的 IPv4；AAAA 使用保存的 IPv6 或手动输入的 IPv6。插件不访问外部 IP 查询网站。
- Cloudflare 为每个不同来源节点维护一条带 `komari-ddns` 备注的记录。开启橙云时使用自动 TTL。
- 华为云将多个来源节点地址写入同一线路的 DNS 记录集；编辑规则时可按域名读取该公网 Zone 当前可用的解析线路，并按全网默认、运营商或地域层级选择。
- 插件每分钟检查启用规则；达到设定间隔并且处于时段内时才访问 DNS API。
- 时段可以选择星期、跨午夜起止时间和固定 UTC 偏移。例如 UTC+08:00 填 `480`。起止时间相同表示所选星期全天生效。固定偏移不会自动切换夏令时。
- 手动同步不受间隔和时段限制。
- 管理页面先显示配置，再单独读取节点列表；读取失败时页面仍可用，并可从规则编辑器重试。

## DNS 安全行为

插件默认不覆盖未标记的同名记录。勾选「接管唯一且无备注的已有记录」后，只会接管一条无备注的同类型记录；Cloudflare 多节点规则不能接管单条旧记录。若域名已有 CNAME 或 NS，插件会停止该规则并保留现状。

插件不会删除云端 DNS 记录对象。Cloudflare 规则不再选择的节点记录会留在 Cloudflare；华为云活动规则会把记录集地址更新为当前所选节点。删除规则、停用或卸载插件后记录仍保留，需管理员自行检查和清理。Cloudflare 限流时插件等待 `Retry-After` 后重试，错误状态会显示在规则卡片中。

## 数据迁移

首次加载时会读取旧版 Cloudflare DDNS 的凭据、规则和同步状态；旧规则 ID 与 Cloudflare 归属备注保留。旧规则默认全天同步并使用自动 TTL，已有华为云规则的线路默认为 `default`。升级不会清除 `plugin-data/cloudflare-ddns`；卸载插件会按 Komari 插件管理器行为删除该目录。
