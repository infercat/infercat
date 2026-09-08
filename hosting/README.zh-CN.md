[English](README.md) · 简体中文

# 托管网页版

网页版是一套静态资源包（执行 `make web` 后的 `web/dist`，或 release 中的 `web-<version>.zip`）。
任何静态托管平台都能直接提供服务。本仓库不绑定任何托管平台：某个平台需要的一切都放在 `hosting/<host>/` 下，绝不进入应用代码。

## Cloudflare Pages (infercat.ai)

`make deploy-web` 构建应用，将 `web/dist` 与 `hosting/cloudflare/` 合并到 `web/deploy/`，并通过 wrangler 发布（OAuth 登录；请在不含 `.env` 的目录下执行）。

- 部署时只发布 wasm 的 gzip 副本（`infercat.wasm.gz`）；Pages 会拒绝超过 25 MB 的文件，且网页版反正也会优先请求 gzip 文件。未压缩的 wasm 模块仍保留在 release 的 zip 包里，供自建托管的人使用。
- `_headers`：HSTS、nosniff、no referrer，对带哈希的静态资源配置长缓存。
- `_routes.json` + `functions/_middleware.js`：第一方计数器。每次页面访问（`/`，带来源站点与 `?from=` 标签）以及每次应用加载（`/infercat.wasm.gz`），都会按国家与浏览器家族向 Analytics Engine 写入一个数据点。页面上没有任何脚本，没有 cookie，不涉及第三方；URL 哈希段里的邀请码绝不会发送到服务器。在控制台中查询（Analytics Engine，数据集 `infercat_loads`）：`SELECT index1 AS kind, blob1 AS country, blob4 AS from, SUM(_sample_interval) AS n FROM infercat_loads WHERE timestamp > NOW() - INTERVAL '7' DAY GROUP BY kind, country, from`。
- infercat.dev 是一个独立的 Pages 项目，仅用于重定向到 infercat.ai（见 `hosting/cloudflare/redirect/`）。

## 路线图订阅名单

`POST /signup` 接收 `{email, lang, from, ts}`。该 Function 会将规范化后的邮箱、语言、固定来源 `landing-roadmap` 以及服务端接收时间写入 `SIGNUPS`；重复提交会保留首次记录并返回成功。它不会发送邮件。IP 哈希的冷却记录 60 秒后过期；KV 只做到最终一致，所以这只是按来源网络做的粗略限速，不是精确的并发配额。线上部署的 `SIGNUPS` 命名空间绑定在 `hosting/cloudflare/wrangler.toml` 中。独立部署需要自建命名空间并重新绑定。

导出订阅元数据（`email:` 前缀可排除冷却记录）：

```sh
wrangler kv key list --config hosting/cloudflare/wrangler.toml --binding SIGNUPS --prefix 'email:' --remote > signup-keys.json
jq '[.[].metadata]' signup-keys.json > signups.json
```

这些属于个人邮箱地址：导出文件请保存在公开仓库之外。`kv key list` 会自动处理分页；记录中自带元数据，因此导出时无需逐个地址查询。详见 [Wrangler KV 命令文档](https://developers.cloudflare.com/workers/wrangler/commands/kv/)。在本地运行 `cd web && pnpm test` 会使用模拟 KV 测试该 Function；无需任何云端资源。

### 重新构建中文字体子集

字体来源：Google Fonts 的 `ofl/notosanssc/NotoSansSC[wght].ttf` 及其附带的 `OFL.txt`。
使用现有的 Python fontTools 4.60.2 WOFF 扩展包（`pip install 'fonttools[woff]==4.60.2'`）：

```sh
python3 hack/subset-font.py 'NotoSansSC[wght].ttf'
python3 hack/subset-font.py --check
make notices
```

脚本将两份语言表的并集生成子集输出到 `web/public/fonts/noto-sans-sc.woff2` 并校验 CJK 覆盖率。常规的网页测试不需要 Python，会自己读打包字体的 cmap，以便及时发现文案和字体不同步。字体与 OFL 均为自托管；运行时不依赖任何字体 CDN。
