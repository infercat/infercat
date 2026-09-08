[English](DATA-DIRECTORY.md) · 简体中文

# 数据目录

`~/Library/Application Support/infercat`（macOS）、`~/.config/infercat`（Linux）、
`%AppData%\infercat`（Windows），或通过 `--data-dir` 指定：

| 文件 | 作用说明 |
|---|---|
| `host.key.json` | 你的主机身份。请备份好；不要放进云同步目录；删掉它，你之前发出去的邀请码就全都失效了。 |
| `keys.json` | 好友密钥的哈希值（绝不保存密钥本身）、状态和限额。 |
| `usage.jsonl` | 每次请求记一行：密钥、接口、状态、token 数、耗时。除非运行 `serve --log-prompts`，否则不含任何 prompt 内容。 |
| `config.json` | 已保存的 `serve` 参数。 |
| `tunnel.log` | 隧道引擎的日志（`serve --verbose` 会改为直接打印，不写入该文件）。 |

`infercat serve -h` 也会列出这些文件。详情请参阅[主机快速上手](../README.zh-CN.md#快速上手主机端)。
