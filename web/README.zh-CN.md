[English](README.md) · 简体中文

# 网页版

Vite + React + TypeScript；Go 隧道客户端在 [wasm/](wasm/) 下编译为 WebAssembly。使用方法请参阅[根目录快速上手](../README.zh-CN.md#快速上手朋友端)。

## 代码结构

- [src/api.ts](src/api.ts)：网关请求、用量与错误；[src/stream.ts](src/stream.ts)：流式响应。
- [src/session.ts](src/session.ts)：连接与会话状态；[src/storage.ts](src/storage.ts)：浏览器持久化与键名注册表。
- [src/invite.ts](src/invite.ts)：邀请码解析；[src/transport/](src/transport/)：原生 fetch 以及基于 wasm 隧道的 HTTP 传输。
- [src/i18n/](src/i18n/)：中英文文案表与命名占位符替换；[src/ui/](src/ui/)：连接、聊天与落地页组件。

## 常用命令

在仓库根目录下，`make wasm` 构建浏览器端桥接模块；`make web` 构建 wasm 并把网页版打包到 `web/dist`。完整的构建/发布命令请参阅[贡献指南](../CONTRIBUTING.md#building)。
在 `web/` 下运行 `pnpm install --frozen-lockfile` 后即可执行以下命令（浏览器检查还需要运行 `pnpm exec playwright install chromium`）：

| 命令 | 用途 |
|---|---|
| `pnpm dev` | Vite 开发服务器。 |
| `pnpm build` | 打包生产环境产物至 `dist/`；需先构建 wasm。 |
| `pnpm test`、`pnpm lint`、`pnpm typecheck` | 单元测试、代码检查与 TypeScript 类型检查。 |
| `pnpm screenshots` | 模拟主机的浏览器场景与截图。 |
| `pnpm launch-check` | 在本地预览中检查打包好的网页版；传入 `INVITE=… APP=…` 还会测试真实主机。 |
| `pnpm tunnel-check` | 针对本地隧道 demo 测试真实 wasm/隧道；需要网络访问。 |
| `pnpm brand` | 基于共享的标志与字体，重新生成图标、扁平标志和两张社交卡片。 |

[dev/](dev/) 包含模拟网关/后端和模拟隧道，以及上线检查、截图、隧道和品牌四套工具。截图工具会自行启动模拟服务；真实主机检查则需要你自己的主机和邀请码。

## 文案与字体

修改文案时需同时更新 [en.ts](src/i18n/en.ts) 与 [zh.ts](src/i18n/zh.ts)；请保留命名占位符。存储键请统一维护在[注册表](src/storage.ts)中。
文案变动后，在仓库根目录运行 `python3 hack/subset-font.py /path/to/NotoSansSC[wght].ttf` 重新构建共享的 CJK 子集。该脚本取两份语言表的字符并集；网页测试会逐个比对 CJK 字符是否都在打包字体里。字体源文件与 fontTools 的说明见[托管网页版](../hosting/README.zh-CN.md#重新构建中文字体子集)。请保留 OFL 并运行 `make notices-check`；切勿引入运行时的字体 CDN。
