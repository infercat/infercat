# 对话中的搜索

[English](SEARCH.md) · 简体中文

主机可以为明确选择使用主机工具的对话提供 `web_search`，以及该密钥可用的图片引擎所提供的
`make_image`。搜索使用主机运营者的 [Exa](https://exa.ai/) 账户，只向 Exa 发送查询，
不发送对话、邀请码、朋友姓名或密钥 ID。Exa 的账户费用另行计算。

把 Exa API 密钥保存在仓库外、仅文件所有者可读的文件中。在主机现有的 `config.json`
中加入以下配置，保留其他设置：

```json
{"search":{"key_file":"/absolute/path/to/exa.key"}}
```

相对路径以主机的数据目录为基准。`infercat serve` 启动时只读取一次文件；更换密钥后需重启。
如果已配置的文件不可读、为空或无效，主机会拒绝启动，错误信息不会包含密钥内容。
删除 `search` 配置即可停用。密钥本身不会写入主机配置，也不会发送给朋友。
仅使用搜索时不需要图片引擎。

客户端须在 `POST /v1/chat/completions` 中明确请求工具：

```json
{"model":"your-model","messages":[{"role":"user","content":"llama.cpp 本周发布了什么？然后给我画一只狐狸。"}],"stream":true,"host_tools":["web_search","make_image"]}
```

主机只提供所请求且可用的工具；省略或留空 `host_tools` 时，普通对话行为不变。
`/me.host_tools` 列出可用工具，不受本次选择或剩余额度影响。非空的主机工具选择不能与
客户端自己的 `tools` 或 `tool_choice` 混用。不会向自行管理工具调用的 Codex、OpenCode
等客户端擅自添加工具。

每次对话最多执行三轮工具，其中最多两次实际搜索、四次独立计量的模型调用。
`web_search` 接受非空 `query` 和可选整数 `count`（1 至 5，默认 3）。最后一次模型调用
会被要求直接回答，不再调用工具。格式错误、未知工具或一轮多个调用都不会执行工具，
而是把工具错误结果交给模型，再让它回答。如果仍请求工具，主机会拒绝，已输出的文字保留。

每个密钥有独立的每日搜索额度，默认 50；设为零仍为 50，负数表示不限。例如：

```sh
infercat keys add alice --search-per-day 20
infercat keys limits alice --search-per-day 100
```

额度在 UTC 午夜重置。请求发给提供商后计一次搜索，包括无结果、提供商错误或超时；
发送前取消则不计费。额度耗尽返回 `budget_exhausted`（429），Retry-After 指向下一次
UTC 午夜。`/me` 提供 `limits.search_per_day` 和 `usage.today_searches`。
模型调用照常计算 token 和 RPM；搜索不增加 token 或 RPM。

每次提供商请求限时 10 秒，不重试、不跟随重定向。主机只请求搜索摘要，不抓取结果网页；
提供商响应体最多 128 KiB，交给模型的标题、网址和摘要纯文本最多 16 KiB。
无结果和提供商失败都作为工具结果交给模型，对话可以继续。搜索步骤所保存的输出与模型
收到的文字完全一致。`search` 用量行只记录计量与耗时，不记录查询或提供商响应；
搜索文本保存在运行记录中。运营者明确开启 `--log-prompts` 时，仍按已告知朋友的方式
记录消息：工具查询和结果会像其他消息文本一样写入提示词日志。

停止对话会取消尚在进行的搜索和模型调用。`make_image` 已提交的图片任务仍独立运行，
按各自的限额完成。
