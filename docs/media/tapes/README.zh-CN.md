[English](README.md) · 简体中文

# 真实的上线演示录屏

在录屏笔记本上从仓库根目录执行 `make demo`。前置条件：
`brew install vhs jq`、`brew install --cask font-ibm-plex-mono`、固定的网页端依赖（`cd web && pnpm install --frozen-lockfile`）、Playwright 的 Chromium，以及在 `http://127.0.0.1:18080` 响应的模型引擎。需要能访问 infercat.ai 及其中继。该命令不会对外发布任何内容。

`host.tape` 运行刚编译好的 CLI 并生成 Alice 的真实邀请码及其 QR 码。`launch-check.mjs --demo-dir` 在 390×720 分辨率下打开真实的 infercat.ai 链接，录制自动连接以及默认设置下的流式回答，并定格供阅读。`friend.tape` 在一个空闲的本地回环端口上运行原生桥接，并通过 `jq` 打印真实的非流式 curl 响应；`question.json` 的提问内容为“Say hello in one sentence.”。回答在屏幕上停留 2.5 秒，上方显示连接路径。渲染器会校验 curl/jq 管道是否成功执行。浏览器和原生路径的标签，都是各自实际观测到的结果。不替换任何传输方式、响应或屏幕文字。

`render.mjs` 将三段录制画面与六个步骤标签及最终的片尾卡组装在一起；没有片头卡。`overlays.mjs` 从 [CAPTIONS.md](CAPTIONS.md) 读取全部展示文案，使用自带的西文字体，以及缓存目录（已在 gitignore 中）里锁定版本的 Noto Sans SC，为每种语言渲染七张静态 PNG；每张步骤 PNG 都会校验 y=720 以上区域的 alpha 全为 0（即完全透明）。放大后的标签统一为 39 px，无需缩放即可排下。终端标签切换取自每次录制的两秒静止间隔；浏览器端则记录其实际发送时间。终端画面使用 IBM Plex Mono 字体与 `style.tape` 中的 ink/paper 配色；主机端使用稍小的字号以容纳真实的 QR 码。MP4 采用 H.264，1280×800，25 fps；GIF 为 960×600，10 fps。封面图选用带有步骤 04 且首次观测到流式渲染 token 的画面（包含思考过程）。QR 码/链接定格 3 秒，浏览器端回答后定格 2 秒，片尾卡定格 2.50 秒（按 25 fps 对齐到帧后为 2.52 秒）；字幕与卡片一律通过硬切变换。在替换仓库里已提交的产物之前，先过一道检查：总时长超过 60 秒、MP4 超过 8,000,000 字节或 GIF 超过 4,000,000 字节都会被拒。

单次录制会生成中英两版叠加层变体：位于 `docs/media/` 下的 `demo.mp4` / `demo.zh.mp4`、`demo.gif` / `demo.zh.gif` 以及 `demo-poster.png` / `demo-poster.zh.png`。浏览器端画面保持英文；两个版本都要通过同样的体积/时长检查。

每次运行都会使用全新的临时数据目录与新邀请码，在完成或失败时撤销密钥，并停止自身的主机与桥接进程。原始录屏与终端记录会保存在终端输出的 `/tmp/icdemo-*` 目录中供审查。命令、构图、字幕、分镜顺序和产物形态每次都一样；模型的实际用词、思考过程、用量、路径耗时和邀请码则可能不同（产品经理裁决，见工单 042）。
