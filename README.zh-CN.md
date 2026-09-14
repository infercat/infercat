[English](README.md) · 简体中文

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/media/mark-paper.svg">
  <img src="docs/media/mark-ink.svg" alt="" width="52" align="left">
</picture>

# Infercat

**让朋友用你的 GPU 聊天。**

[![License: MIT](https://img.shields.io/badge/license-MIT-1F3BFF?style=flat-square)](LICENSE)
[![CI](https://github.com/infercat/infercat/actions/workflows/ci.yml/badge.svg)](https://github.com/infercat/infercat/actions/workflows/ci.yml)

---

把你电脑上的模型分享给朋友。在你现有的推理服务（llama.cpp、llama-swap、vLLM、Ollama 或 LM Studio）前面跑一个二进制程序，给每个朋友发一个邀请码。他们把邀请码粘贴到网页版就能和你的模型聊天：免账号、免 VPN、什么都不用装。连接全程端到端加密，中间的中继只能看到密文。你可以给每个朋友单独设置限额。聊天日志默认只记数字，除非你开启文本日志；图片任务会在朋友的密钥下保存提示词和图片，最多 7 天。

![Share a local model with a friend in a browser or terminal](docs/media/demo.zh.gif)

*真实运行录屏：朋友的浏览器通过中继连上一台运行 llama.cpp 的笔记本电脑。*

## 运行原理

- **主机**运行 `infercat serve`。它会自动找到推理服务，向中继建立一条 WireGuard 隧道，并在隧道内跑一个轻量网关：兼容 OpenAI 接口，一人一个密钥。
- **朋友**打开网页版，粘贴邀请码。网页版自带编译成 WebAssembly 的隧道客户端，浏览器可以直接连上你的主机——经由中继，全程端到端加密。
- **隧道**基于 [tailcat](https://github.com/tailscale/tailcat)，即 Tailscale 开源的数据面（去掉了控制面）。两端均无需账号。中继采用 DERP 服务器：默认使用公共中继，也可以自建（`--derpmap-url`）。Infercat 与 Tailscale Inc. 无关联，亦未获得其背书。

**先试试：**打开 https://infercat.ai/try 即可与我们的演示主机（Qwen3.8 27B）对话，无需安装。

## 快速上手（主机端）

你需要先启动一个推理服务（llama.cpp、llama-swap、vLLM、Ollama 或 LM Studio 均可；任何兼容 OpenAI `/v1/chat/completions` 的服务都能用）。朋友那边只要有个浏览器就行。

**可选——还没有推理引擎？** [安装 Ollama](https://ollama.com/download)，然后：

```sh
ollama serve &      # Ollama 应用已在运行的话可跳过
ollama run gemma4
```

会下载 Gemma 4（约 10 GB，需要 16 GB 内存，支持图片）并打开对话。保持运行或输入 `/bye`，引擎会继续运行。`infercat serve` 会自动找到它。

<a id="no-engine-yet"></a>
<details>
<summary><b>进阶引擎配置</b></summary>

同一个小模型，分别为 llama.cpp、Ollama、LM Studio 和 vLLM 调整上下文与并行处理位。

任选一个。这条命令会下载并启动一个小模型（Gemma 4 E2B，约 4 GB，8 GB 内存的笔记本也能跑，支持图片）：

**llama.cpp**

`brew install llama.cpp`

```sh
llama-server -hf unsloth/gemma-4-E2B-it-qat-GGUF:UD-Q4_K_XL -c 65536 -np 2 --host 127.0.0.1
```

**Ollama** — 下方启动 Infercat 时使用 `infercat serve --slots 2`。

`brew install ollama`

```sh
(OLLAMA_CONTEXT_LENGTH=32768 OLLAMA_NUM_PARALLEL=2 ollama serve & until ollama list >/dev/null 2>&1; do sleep 1; done; ollama run hf.co/unsloth/gemma-4-E2B-it-qat-GGUF:UD-Q4_K_XL "" && wait)
```

**LM Studio** — 下方启动 Infercat 时使用 `infercat serve --slots 2`。

[安装 LM Studio](https://lmstudio.ai/download)，并打开一次。

```sh
~/.lmstudio/bin/lms get https://huggingface.co/unsloth/gemma-4-E2B-it-qat-GGUF/resolve/main/gemma-4-E2B-it-qat-UD-Q4_K_XL.gguf --yes && ~/.lmstudio/bin/lms load gemma-4-e2b-it-qat --context-length 32768 --parallel 2 --yes && ~/.lmstudio/bin/lms server start --port 1234
```

**vLLM** — 未在 macOS 上验证；需要 CUDA 显卡

[安装 vLLM](https://docs.vllm.ai/en/latest/getting_started/installation/gpu/)

```sh
vllm serve google/gemma-4-E2B-it-qat-w4a16-ct --host 127.0.0.1 --max-model-len 32768 --max-num-seqs 2
```

</details>

```sh
curl -fsSL https://infercat.ai/install.sh | sh    # macOS 或 Linux
brew install infercat/tap/infercat                # 或用 Homebrew
```

- [安装脚本备用链接](https://raw.githubusercontent.com/infercat/infercat/main/hack/install.sh)。
- Docker：[用容器长期运行主机](docs/DOCKER.zh-CN.md)。
- Linux 安装包：[安装 .deb 或 .rpm 并运行用户级服务](docs/LINUX.zh-CN.md)。
- 其他平台请前往 [Releases](https://github.com/infercat/infercat/releases)；使用 `shasum -a 256 --ignore-missing -c infercat_<version>_checksums.txt` 校验。

如果解析不到最新版本（触发限流、暂无正式发布或网络问题），可以下载安装脚本后运行 `INFERCAT_VERSION=vX.Y.Z sh install.sh`。

接着：

```
infercat serve --name "Max's laptop"     # finds llama.cpp, llama-swap, Ollama, LM Studio or vLLM
infercat keys add alice                  # prints alice's invite once (and a QR code)
```

`serve` 会输出检测到的引擎、隧道地址、中继节点，以及朋友具体能访问哪些接口。`keys add` 会打印邀请——`serve` 时加了 `--web-url` 就打印成链接，否则是一串待粘贴的邀请码。通过任何方式发给 alice 即可；邀请码只显示一次，本地仅保存哈希值。

如果你的引擎运行在其他端口或另一台机器上：

```
infercat serve --upstream http://127.0.0.1:18080
```

`serve` 的持久设置会保存在 `config.json` 中。`--log-requests`、`--log-prompts`、`--agent`、`--ephemeral` 和 `--verbose` 等单次运行选项需要每次指定。`--log-requests` 在终端为每个已完成的请求打印一行，不含提示词内容。

用[语音主机配方](docs/VOICE.md)添加麦克风和朗读回复。
用[图片生成主机配方](docs/IMAGES.zh-CN.md)让朋友生成图片。
用[搜索主机配方](docs/SEARCH.zh-CN.md)为明确选择主机工具的对话提供网页搜索。

用 `infercat console` 打开本地**控制台**，查看用量、管理密钥和修改支持的设置。`serve --console IP:PORT` 指定回环监听地址（默认 `127.0.0.1:9101`；`off` 关闭）。详见[控制台与管理 API](docs/ARCHITECTURE.md#local-admin-api)。

需要**远程管理**时，在运行中的主机上使用 `infercat remote on|off|rotate|status`。开启或轮换时，管理码只显示一次；本地控制台监听必须开启。管理码可管理密钥、设置和存储数据，与朋友的邀请码分开授权。详见[远程控制台限额](docs/LIMITS.md#remote-console)。

**智能体接口**让已获授权的朋友提交运行、查看步骤和捕获的输出、回答审批并取消工作。先用 `infercat agent install` 安装，再用 `serve --agent` 启用运行环境、`keys limits alice --agent=true` 授权朋友；每次运行使用独立的受限工作区。安装方法与平台边界见[智能体运行环境](docs/AGENT-RUNTIME.md)。

<details>
<summary><b>macOS 提示无法验证开发者</b></summary>

二进制文件目前尚未签名。macOS 第一次运行下载来的未签名程序时会拦住它。可以在终端中清除隔离标记后再运行：

```
xattr -d com.apple.quarantine ./infercat
```

或者在访达（Finder）中按住 Control 键点击该文件，选择**打开**并确认一次。通过 Homebrew 安装则不会遇到此问题。
</details>

<details>
<summary><b><code>serve</code> 常用参数</b></summary>

| `serve` 参数 | 说明 |
|---|---|
| `--upstream URL` / `--upstream-key TOKEN` | 你的推理服务地址（未指定时自动探测；`--upstream auto` 会清除已保存的地址） |
| `--name NAME` | 朋友看到的主机名称（默认：本机的主机名） |
| `--web-url URL` | 朋友打开网页版的地址；配置后邀请信息会直接打印为链接 |
| `--slots N` | 引擎可并发处理的请求数（0 = 自动询问引擎） |
| `--models a,b` | 为所有密钥固定文字模型，与各密钥的模型列表取交集；图片和语音接口只受密钥白名单约束；`--models all` 清除固定列表 |
| `--region NAME` / `--derpmap-url URL` | 偏好的中继区域 / 自建中继映射表地址 |
| `--dev-listen ADDR` | 额外在本地回环地址监听并放宽 CORS，用于前端开发 |
| `--log-prompts`、`--ephemeral`、`--verbose` | 单次运行选项：记录消息内容；临时主机身份；在终端打印隧道日志 |
| `--data-dir DIR` | 密钥、用量、配置和主机密钥的存放目录 |

运行 `infercat serve -h` 查看完整参数列表及数据目录下的具体文件。
</details>

`infercat setup --profile apple-64g` 会复用哈希匹配的模型缓存，把缺少的已发布引擎和模型下载到数据目录。它逐个启动自己管理的成员，检查模型身份（主模型还会执行一条短提示词），停止后才保存兼容的设置；已有引擎保持运行。可重复使用 `--model-path anchor=/path/to/model.gguf` 覆盖路径（也接受资源 id），或用 `--custom profile.json` 只检查兼容性，不下载、不启动。主模型失败、取消或设置冲突时，原有主机配置保持不变。可选成员检查失败会标为不可用，仍可配置聊天。

接着运行 `infercat serve`：它启动并常驻已安装的主模型，按请求启动其他成员，并在配置的空闲时间后停止它们。`infercat status` 显示各成员状态；嵌入请求使用配置中的独立嵌入模型。安装时已运行的引擎仍由外部管理，主机不会停止它们。Setup 会打印并记录托管成员的实际启动命令；内置配置更新后需重新运行 setup。没有已发布引擎构建的成员会标为不可用。原生语音使用已验证的 helpers-v0.1.0 及单独固定版本的 sherpa 运行环境。ASR 尚未选定配置资源；16 GB 档使用支持视觉的 Q4 E4B，内存已在 16 GB macOS 虚拟机验证，真实硬件性能尚未验证。NVIDIA 尚未测量。详见[配置格式](docs/ARCHITECTURE.md#loadout-profiles-and-setup)和[原生 Kokoro 辅助程序](packaging/helpers/README.md)。

**已有主机？** 先停止 `serve`，运行一次 `infercat identity upgrade`，再启动 `serve`，轮换或新增密钥，把新的 `ic2` 邀请码发给每位朋友。旧身份备份为 `host.key.json.pre-ic2`；升级后，原有邀请码全部失效。在你明确升级之前，旧身份仍按原样提供服务。这个临时升级命令计划在 2026-09-25 之后移除。

内置三种配置：

| 配置 | 目标主机 |
|---|---|
| `apple-64g` | 64 GB Apple 芯片主机，使用 Metal 模型组合。 |
| `apple-16g` | 16 GB Apple 芯片主机；内存已在 macOS 虚拟机验证，真实硬件性能尚未验证。 |
| `nvidia-12g` | 12 GB 显存的 Linux NVIDIA 主机；尚未测量。 |

## 快速上手（朋友端）

打开 **[infercat.ai](https://infercat.ai)**，粘贴邀请码，点击 Connect。搞定。主机的 `keys add` 会把邀请打印成一个链接（还有二维码），点开就会打开网页版，邀请码已经填在输入框里，通常你只要点一下。

想自己托管网页版？它就是一个静态包：Releases 里的 `web-<version>.zip`，任意静态文件服务器，根目录下放 `index.html`：

```
unzip web-<version>.zip -d web && python3 -m http.server 8080 --directory web --bind 127.0.0.1
```

网页顶部会显示当前连接路径（`relayed via nyc · 64 ms`）、模型名称以及对应主机限额的已用额度。所有聊天记录都只留在你的浏览器本地。

## 朋友能访问什么

[网关接口表](docs/ARCHITECTURE.md#gateway-http-api)列出了可访问的接口：模型、聊天、Responses、嵌入，已配置的语音和图片接口，以及按密钥隔离的运行、事件和捕获输出。远程控制台需明确开启，并使用独立的管理凭证；朋友的密钥不能授权。网关不开放任意主机端口或文件系统路径。

## 隐私

- **聊天日志默认只记数字和耗时**；主机开启 `serve --log-prompts` 后会记录文本，应用会向朋友说明。图片任务会在朋友的密钥下保留提示词和图片 7 天（受保留期和空间上限约束），不受该日志开关控制。开启 `--log-prompts` 后，图片提示词也会写入 `usage.jsonl`。工具查询和结果也会像其他消息文本一样写入提示词日志。
- 中继端**只看密文**：从朋友的浏览器到主机设备之间全程采用 WireGuard 加密。目前浏览器流量一律经由中继转发（浏览器本身无法进行 NAT 打洞）；直连链路将随隧道库的 WebRTC 传输层一同推出。
- 邀请密钥只展示一次，本地仅保存**哈希值**。邀请码一旦泄露，执行一次 `keys rotate` 就能让旧码立刻失效。

## 每个朋友的限额

每个密钥从创建起就自带限额——除非主机主动放开，否则没有无限制这一说：

```
infercat keys add bob --rpm 6 --daily-tokens 50000          # tight, for a stranger
infercat keys limits alice --rpm 60 --daily-tokens 1000000 --max-output-tokens 8192
```

`keys add` 的默认值：

| 字段 / 参数 | 默认值 |
|---|---|
| `rpm` / `--rpm` | 每分钟 20 次计量请求 |
| `tpm` / `--tpm` | 每分钟 20,000 token |
| `max_concurrent` / `--max-concurrent` | 同时 1 个请求 |
| `max_output_tokens` / `--max-output-tokens` | 4,096 token |
| `max_context` / `--max-context` | 0：使用引擎的上下文长度 |
| `daily_tokens` / `--daily-tokens` | 每天 200,000 token |
| `daily_audio_seconds` / `--daily-audio-seconds` | 每天 3,600 秒 |
| `daily_speech_chars` / `--daily-speech-chars` | 每天 200,000 字符 |
| `daily_images` / `--daily-images` | 每天 20 张图片 |
| `max_queued_images` / `--max-queued-images` | 8 张排队图片 |
| `search_per_day` / `--search-per-day` | 每天 50 次搜索 |
| `models` / `--models` | 空列表：所有共享模型 |

超额时返回带 `Retry-After` 的 `429`；突发请求超出引擎处理槽位时会短暂排队，随后返回 `503`——绝不会把引擎卡死。网页版会向每位朋友展示各自的用量仪表盘。

管理朋友：`keys list` · `keys pause alice`（在 `keys resume` 之前一律返回 403）· `keys revoke alice`（永久撤销，会先问你一次）· `keys rotate alice`（换一个新邀请码，旧的失效）。查看状态：`status`（实时状态）与 `usage`（历史用量）。所有调整在运行中的主机上即时生效。

## 常见问题

**主机端需要注册账号吗？** 不需要。`serve` 会在数据目录生成一个主机身份标识，这就是全部的注册流程。**朋友端需要吗？** 也不需要——邀请码本身就是凭证。

**有哪些数据会经过你们的服务器？** 只有中继流量，而且全都是密文：DERP 服务器只负责在朋友的浏览器与你的主机之间转发加密数据包。默认使用隧道库映射表中的公共中继；通过 `--derpmap-url` 可以指定你自己的自建中继。

**除了网页版，能用其他客户端吗？** 网关兼容 OpenAI 接口，因此任何能带 bearer token 访问 `/v1/chat/completions` 的客户端，都能在隧道内使用。或者干脆不用浏览器：`infercat connect` 能把邀请码直接变成本地的 `http://127.0.0.1:11435/v1` 接口，任何应用都能调用（见下文的快速上手）。

**两个人共用一个邀请码行不行？** 能用，受那个密钥的限额约束，在 `usage` 里也看得见。建议一人一个密钥，反正不花钱。

**电脑休眠了怎么办？** 朋友端会提示“Max's laptop didn't answer”并说明原因；主机恢复后，网页版会自动重试。

**支持哪些模型？** 取决于你本地引擎跑什么模型；给密钥加上 `--models` 参数可以限制该朋友能选择的模型范围。

## 项目状态：Beta

核心功能均已跑通并经过测速（`docs/MEASURE.md`：经中继传输约 160 tokens/s，浏览器首字延迟 110–160 ms）。坦白说，目前还有这些已知限制：浏览器流量目前一律走中继；macOS 二进制文件尚未签名（见上文）；一台主机对应一个引擎；默认的公共中继有限流，而且随时可能被撤销，所以超出演示用途就该自建中继。各层的负载上限正在测，结果记在 `docs/MEASURE.md`，并会在 `docs/LIMITS.md` 里用大白话讲清楚。

## 快速上手（朋友端 · 用自己的应用）

无需浏览器：同一个二进制文件可以直接把邀请码变成一个本地的 OpenAI 兼容接口。这样一来，Open WebUI、Cursor、Claude Code、OpenAI SDK 乃至直接用 `curl` 都能像调用本地模型一样使用朋友的模型。两台电脑一旦找到彼此，链路就会转成直连；中继只是它们碰头的地方。

```
bin/infercat connect ic2.…          # paste the invite
```
```
host      Max's laptop  ·  gemma-4-E2B-it-Q4_K_M.gguf
path      relayed via New York City · 27 ms       # re-checked every 30 s, printed when it changes
local     http://127.0.0.1:11435
          set your app's base URL to http://127.0.0.1:11435/v1, any API key
```

邀请码中自带的密钥会自动附加到每次请求中；调用应用自身填写的 API key 会被忽略。只转发 `/v1/*` 和 `/me`，其他一律不转发。报错信息会保留主机的状态与错误码，并用通俗语言提示应对方法（已暂停、已撤销、主机休眠、触发限流并带 `Retry-After`、主机繁忙）；当主机失去响应时，`connect` 会给出明确提示并自动尝试重连。

编码智能体可用 `infercat connect <invite> --configure opencode,dsh,codex`（也可只选其中几个）。它在实际本地端口注册 `/me` 返回的模型，并打印选择方法：OpenCode 使用打印出的 `OPENCODE_CONFIG=… opencode --model …`；Harness 使用 `INFERCAT_API_KEY=unused dsh`，再用 `/model` 选择；Codex 使用 `codex --profile infercat`。原有默认选择保持不变。

Codex 0.154.0 从 `$CODEX_HOME/infercat.config.toml`（通常为 `~/.codex/infercat.config.toml`）读取生成的配置，选择 `/me` 中第一个模型，并关闭该配置的托管网页搜索，因为网关不提供那类托管工具。不会修改基础 `config.toml`；已有的非托管配置或旧 `[profiles.infercat]` 配置会导致拒绝。配置文件不含 API key。若 `OPENCODE_CONFIG` 已指向其他文件，会拒绝写入；请在本次运行中取消该变量，或把 provider 合并到自己的文件。OpenCode 只使用一个自定义路径。

OpenCode 的附加文件会与你的配置合并；Harness 在 `$DSH_HOME/settings.yaml`（通常为 `~/.dsh/settings.yaml`）插入带标记的 provider。正常退出会移除未改动的托管块；`infercat connect --unconfigure` 也能清理，无需邀请码或停止桥接。已编辑的块会保留并报错。即使桥接未运行，`infercat status` 也会列出本地注册。原始备份与所有权记录位于系统用户配置目录下的 `infercat/agents`（Unix 也可用 `XDG_CONFIG_HOME`）。YAML 行内映射、别名和自定义 Harness 设置位置需要手动配置。Windows 使用原生用户配置目录和 `DSH_HOME`；打印的启动命令为 POSIX shell 语法。Windows：可编译，尚未测试。

```
OPENAI_BASE_URL=http://127.0.0.1:11435/v1 OPENAI_API_KEY=x python3 -c '
from openai import OpenAI; c = OpenAI()
for e in c.chat.completions.create(model=c.models.list().data[0].id, messages=[{"role":"user","content":"hi"}], stream=True):
    print(e.choices[0].delta.content or "", end="", flush=True)'
```

## 数据目录

详见[数据目录](docs/DATA-DIRECTORY.zh-CN.md)：存储路径、文件用途与备份说明。

## 编译构建

构建与发布检查见[贡献指南](CONTRIBUTING.md#building)，前端说明见 [web README](web/README.zh-CN.md)。

## 开源协议

MIT — 详见 [LICENSE](LICENSE)。基于 Tailscale 的开源库 [tailcat](https://github.com/tailscale/tailcat) 构建（Infercat 与 Tailscale Inc. 无关联，亦未获得其背书；见上文）。tailcat 采用 BSD-3 协议；所有依赖项的开源协议均列在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 中。安全漏洞提报：[SECURITY.md](SECURITY.md)。贡献指南：[CONTRIBUTING.md](CONTRIBUTING.md)。

Made by [2185 Lab](https://2185lab.com). MIT.
