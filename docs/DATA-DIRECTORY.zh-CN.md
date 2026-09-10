[English](DATA-DIRECTORY.md) · 简体中文

# 数据目录

`host.lock` 是保留在磁盘上的锁文件，不是 PID 文件。`serve` 在运行期间持有操作系统独占锁；同一目录上的第二个 `serve` 或身份升级会被拒绝。进程退出或崩溃时，操作系统会释放锁；主机运行期间不要删除这个文件。旧版本没有这把锁，也必须先停止；升级命令还会检查其管理监听端口。`infercat identity upgrade` 要求先停止主机，只创建一次 `host.key.json.pre-ic2` 备份且不覆盖已有备份，然后原子替换为第二版身份。不要用旧版本的程序运行已升级的身份：旧版本会忽略这个标记，用同一把密钥提供旧格式的地址，`ic2` 邀请会在握手时静默失败。

`~/Library/Application Support/infercat`（macOS）、`~/.config/infercat`（Linux）、
`%AppData%\infercat`（Windows），或通过 `--data-dir` 指定：

| 文件 | 作用说明 |
|---|---|
| `host.key.json` | 你的主机身份。请备份好；不要放进云同步目录；删掉它，你之前发出去的邀请码就全都失效了。 |
| `keys.json` | 好友密钥的哈希值（绝不保存密钥本身）、状态和限额。 |
| `usage.jsonl` | 每次请求记一行：密钥、接口、状态、token 数、耗时。除非运行 `serve --log-prompts`，否则不含任何 prompt 内容。 |
| `runs/<key-id>/images/<run-id>` | 生成的 PNG/JPEG：每张最多 8 MiB，7 天过期，每把密钥另有 256 MiB 图片空间，满时先淘汰最旧输出。参阅[图片主机配方](IMAGES.zh-CN.md)。 |
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

图片文件权限为 0600，目录为 0700。任务快照 `runs/<key-id>/state.json` 只保存图片的元数据；图片空间独立于每把密钥的 64 MiB 状态上限。Discard 立即删除输出；过期、淘汰或删除后的标记保留到任务记录过期。启动清理会移除孤立图片文件，中断的任务不会自动重放。
