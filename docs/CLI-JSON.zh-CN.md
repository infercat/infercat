[English](CLI-JSON.md) · 简体中文

# CLI JSON 契约（schema 1）

请通过绝对路径调用应用随附的二进制文件，使用 argv 数组传递参数，不经过 shell。`--json` 永远表示 `--json=1`。普通机器命令只向 stdout 输出一个 JSON 对象；错误也写入 stdout，并以非零状态退出。机器模式不输出颜色或二维码，不提示确认，也不读取终端。`watch` 是下面说明的流式例外。

```sh
infercat version --json
infercat status --json
infercat keys add --json -- "Alice Chen"
infercat keys limits k_example --rpm 40 --agent --json
infercat settings set --json -- "name=我的主机" slots=2
infercat keys revoke k_example --yes --json
```

自由文本（新名称、设置赋值）必须放在 `--` 后面；所有选项，包括 `--data-dir`，放在它前面。名称不能以 `-` 开头。机器命令通过 ID 引用密钥，不通过显示名称；供人使用的命令仍可使用唯一名称。

## 封装与兼容性

```json
{"schema":1,"command":"status","host":{"version":"0.1.6","name":"我的主机"},"data":{}}
```

```json
{"schema":1,"command":"keys.pause","error":{"code":"key_not_found","message":"no key k_example"}}
```

除 JSON 值外围的空白外，`data` 与管理路由响应逐字节一致。CLI 不归一化数组、null、零值或省略字段。[生成的字段清单](../cmd/infercat/testdata/cli-schema-1.txt) 记录这些区别。CLI 自己定义的新载荷（`api`、`version`、封装）保留零值，空列表使用 `[]`。host 元数据描述操作开始时的主机；离线命令使用 CLI 构建版本和本地保存的名称（`version` 和 `api` 的名称为空）。

新增字段或操作兼容旧契约。错误代码，以及引擎类型、密钥状态、运行状态/种类等字符串枚举是开放的：保留或显示未知值，不要据此拒绝响应。删除、重命名或更改字段类型，把必有字段改为可省略，改变 null 的允许性、退出码含义或既有操作语义，都需要新 schema。schema 1 和裸 `--json` 必须继续有效，不能被新版本暗中替换。客户端忽略未知字段。

**脚本迁移：** 原先未封装的 `keys add --json`、`keys rotate --json`、`remote … --json` 结果现在位于 schema 1 的 `data` 中。读取路径改为 `.data`，新密钥名称移到 `--` 后。`remote.status` 和 `expose.status` 返回完整的 `/status` 响应，不做字段投影。

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 操作失败，查看 `error.code` 和 `error.message` |
| 2 | 参数错误、不支持的 schema，或缺少 `--yes`（`confirmation_required`） |
| 69 | 此数据目录没有正在运行的主机（`host_stopped`） |
| 75 | 主机未及时响应（`host_not_responding`） |

本地客户端对每个修改动作只发送一次修改请求（此前可能进行准备性读取），不跟随重定向，也不重试修改操作。调用超时为 5 秒，响应上限为 16 MiB。发生超时、连接中断或响应损坏时，操作可能已经提交：先检查状态，再决定下一步；不要自动重复创建、轮换或清除。HTTP 错误保留路由的原句；代码由状态和操作决定（`key_not_found`、`not_found`、`conflict`、`invalid_request`、`unauthorized`、`forbidden`，其余为 `admin_error`）。其他本地错误使用 `command_failed`；不完整或无效响应使用 `invalid_response`。

## 在线与离线行为

主机运行时，密钥修改和用量查询通过已认证的管理路由执行。在线修改失败后绝不退回文件写入。人类可读输出及主机停止时的文件操作仍然可用。人类命令 `usage --since` 保留滚动时间窗口；机器命令 `usage --window today|week` 使用 UTC 日历日，可用 `--key ID` 筛选。

`keys list`、`keys add`、`version`、`api` 可以离线使用。列表用于检查已停止的主机；添加操作可使用已创建的 v2 身份预先准备邀请。离线添加不会创建主机身份，也仍拒绝旧身份创建邀请。其余管理操作要求主机正在运行。另行实现的 `service` 操作调用本地服务管理器，在主机停止时也可使用。管理令牌只留在 CLI 内部，不进入封装结果。

密钥的 `force`、`agent` 修改只允许在本机进行。远程控制台遇到任一字段（包括显式 false 或 null）都会在修改前拒绝。既有远程控制台 API 和查询白名单保持不变。设置仅接受路由白名单：`name`、`web_url`、`slots`、`console`、`log_requests`。

`stored clear ID --expect CURSOR --yes` 先读取当前记录、核对游标，再把同一快照的计数发送给 DELETE。冲突时拒绝，不重试。`expose --on|--off` 保留本地桥接配置写入，并返回 reload 的 `{ "ok": true }`；注册仍是人类操作。

## Watch

```sh
infercat watch --json --interval 2s
```

每行一个对象：`hello`（schema、host、interval_ms、events）、`status`（schema、at、data）、`event`（schema、data）、`dropped`（schema、count），连接结束时输出 `gone`（schema、reason）。事件订阅建立后才输出 `hello`；按间隔查询状态。响应压缩为单行，绝不包含提示词或回答正文。

CLI 最多缓存 256 个事件。stdout 读取缓慢不会阻塞事件读取；`dropped` 只报告该转发队列确知的丢失量。主机既有事件广播也可能丢失事件，且不报告丢失量，因此这不是持久审计流。当前总量请查询 status/usage。

启动时没有主机，watch 只输出一行 `gone`，退出码为 69，不输出 `hello`。超时退出码为 75。中断 watch 会取消连接并等待读取协程退出，不会停止主机。远程主机模式属于另行实施的工作。服务生命周期命令由服务层（196）实现，下方操作表和字段清单也包含它们。

## 操作

由 `infercat api --json` 生成；`{id}` 是管理路由路径参数。所有命令都支持 `--data-dir DIR`。

<!-- cli-operations:start -->
| 操作 | `infercat` 后的 argv | 管理路由 | Schema |
|---|---|---|---|
| `status` | `status --json` | GET /status | 1 |
| `watch` | `watch --json [--interval 2s]` | GET /status; GET /events | 1 |
| `keys.list` | `keys list --json` | GET /keys | 1 |
| `keys.get` | `keys show ID --json` | GET /keys/{id} | 1 |
| `keys.add` | `keys add --json [limits] -- NAME` | POST /keys | 1 |
| `keys.limits` | `keys limits ID --json [limits]` | PATCH /keys/{id} | 1 |
| `keys.pause` | `keys pause ID --json` | POST /keys/{id}/pause | 1 |
| `keys.resume` | `keys resume ID --json` | POST /keys/{id}/resume | 1 |
| `keys.revoke` | `keys revoke ID --yes --json` | POST /keys/{id}/revoke | 1 |
| `keys.rotate` | `keys rotate ID --json` | POST /keys/{id}/rotate | 1 |
| `usage` | `usage --json --window today\|week [--key ID]` | GET /usage | 1 |
| `engine` | `engine --json` | GET /engine | 1 |
| `settings.get` | `settings --json` | GET /settings | 1 |
| `settings.set` | `settings set --json -- k=v…` | PATCH /settings | 1 |
| `runs.list` | `runs list --json [--key ID]` | GET /runs | 1 |
| `stored.get` | `stored ID --json` | GET /stored | 1 |
| `stored.clear` | `stored clear ID --expect CURSOR --yes --json` | GET /stored; DELETE /stored | 1 |
| `remote.status` | `remote status --json` | GET /status | 1 |
| `remote.on` | `remote on --json` | POST /remote/enable | 1 |
| `remote.off` | `remote off --json` | POST /remote/off | 1 |
| `remote.rotate` | `remote rotate --json` | POST /remote/rotate | 1 |
| `expose.status` | `expose --json` | GET /status | 1 |
| `expose.on` | `expose --on --json` | POST /reload | 1 |
| `expose.off` | `expose --off --json` | POST /reload | 1 |
| `service.status` | `service status --json` | — | 1 |
| `service.install` | `service install --json [--start-at-login=false]` | — | 1 |
| `service.uninstall` | `service uninstall --json` | — | 1 |
| `service.start` | `service start --json` | — | 1 |
| `service.stop` | `service stop --json` | — | 1 |
| `service.restart` | `service restart --json` | — | 1 |
| `service.login` | `service login on\|off --json` | — | 1 |
| `version` | `version --json` | — | 1 |
| `api` | `api --json` | — | 1 |
<!-- cli-operations:end -->

## 维护契约

`make cli-contract` 重新生成字段清单和操作表，并要求 `git diff --exit-code` 通过。常规 Go 测试检查字段清单、非 TTY 子进程的黄金样本，以及实际管理路由注册表。等价性测试将 CLI 的原始 `data` 与真实处理器的响应逐字节比较。清单检查会拒绝字段删除、重命名、类型或存在性变化，也会阻止通过重新生成清单掩盖相对上一提交的 schema 1 破坏性变更。
