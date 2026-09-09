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

`admin.json` 保存远程控制台开关、管理码的 SHA-256 哈希及启用时间，权限为 0600，
不保存管理码明文。文件损坏或无法读取时，主机仍会启动，但远程访问关闭；启动提示和
控制台设置会指出文件路径及修复方法。原文件保留到你明确重新启用远程访问时，才被原子替换；
修复失败会明确报错。

无界面的主机可运行 `infercat remote on --data-dir DIR`，获取只返回一次的管理链接或管理码，
以及终端二维码。打开链接或在网页中粘贴管理码即可进入 `/console`。
`remote rotate` 更换管理码，`remote off` 关闭访问，`remote status` 只显示状态，不返回管理码。
这些命令通过经过认证的本地管理 API 操作，要求回环控制台监听器已开启。
脚本可用 `--json` 获取结果，用 `--no-qr` 省略二维码。请保护 on/rotate 的输出；
命令不会自动重试结果不确定的操作。若结果丢失，请先检查状态，再更换管理码。
