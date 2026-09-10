[English](IMAGES.md) · 简体中文

# 让主机生成图片

朋友描述一张图，主机一次生成一张。聊天照常继续，生成图片时会稍慢一些。你需要包含图片任务的 Infercat 当前源码构建（main `3f9d440` 或更新版本，面向 0.1.4）、原有的聊天引擎，以及一个独立的 OpenAI 兼容图片引擎。旧主机不提供这项能力。

这份配方直接运行 stable-diffusion.cpp，不加中间适配服务。引擎端口只监听回环地址，朋友通过原有邀请访问经过认证的图片接口。模型下载合计 **8.92 GB**，另需源码和构建空间。实测 Mac 有 64 GiB 统一内存；图片进程的物理内存占用峰值为 9.90 GiB。这是一次观察，不是最低内存要求。

## 构建固定版本的引擎

准备 Git、CMake、curl、Python 3 和 C++ 编译器。macOS 使用 Xcode Command Line Tools 和已安装的 CMake（例如 Homebrew 的 `cmake`）。NVIDIA Linux 还需要可用的 NVIDIA 驱动和 CUDA Toolkit，且 `nvcc` 在 PATH 中。下面的 Linux 命令已对照固定版本的[构建说明](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/docs/build.md)核对，**没有在这台 Mac 上执行或测速**。

从尚不存在 `infercat-images` 子目录的位置开始：

```sh
mkdir infercat-images
cd infercat-images
git clone --no-checkout https://github.com/leejet/stable-diffusion.cpp.git
cd stable-diffusion.cpp
git checkout 469fc49bb7ded7400c60fa6eb382b6e4bc02cc20
git submodule update --init --recursive
```

按机器选择**一种**构建，然后在同一个终端、同一个源码目录继续。递归子模块检出会将 ggml 固定在 `e20c3a14aa70ee84ca58499814206dd08d8026bc`。引擎采用 MIT 许可证。

**macOS，Apple silicon / Metal：**

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DSD_METAL=ON -DSD_SERVER_BUILD_FRONTEND=OFF
cmake --build build --target sd-server -j 6
IMAGE_BACKEND=metal
```

**Linux，NVIDIA / CUDA：**

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DSD_CUDA=ON -DSD_SERVER_BUILD_FRONTEND=OFF
cmake --build build --target sd-server -j 6
IMAGE_BACKEND=cuda0
```

`cuda0` 将三个组件都放到第一块 CUDA 设备上，不会阻止聊天引擎使用那块卡。需要选择其他设备时，请参阅固定版本的[后端说明](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/docs/backend.md)。这里不推断工作站性能。

## 下载三个模型文件

使用蒸馏版 **FLUX.2 klein 4B**，不要换成 base 版或 9B。实测使用 Q8_0 量化；文本编码器和 VAE 也不可缺少。表中各发布方在链接指向的固定版本模型卡里均声明 Apache-2.0。这里的 Qwen 负责编码图片提示词，不替代聊天引擎。

| 组件 / 固定版本的发布方 | 文件 | 字节数 |
|---|---|---:|
| [FLUX.2 klein 4B，蒸馏版 Q8_0](https://huggingface.co/leejet/FLUX.2-klein-4B-GGUF/tree/3b1f5a9dc3abb32238b053aeb3d823c30afdacbd) | `flux-2-klein-4b-Q8_0.gguf` | 4,300,629,440 |
| [Qwen3-4B 文本编码器，Q8_0](https://huggingface.co/unsloth/Qwen3-4B-GGUF/tree/22c9fc8a8c7700b76a1789366280a6a5a1ad1120) | `Qwen3-4B-Q8_0.gguf` | 4,280,405,792 |
| [FLUX2 VAE](https://huggingface.co/Comfy-Org/flux2-klein-4B/tree/5f526678002e43af5551dadb73ce2e8c91b43afe) | `flux2-vae.safetensors` | 336,211,292 |

```sh
mkdir -p models
curl -fL --retry 3 \
  https://huggingface.co/leejet/FLUX.2-klein-4B-GGUF/resolve/3b1f5a9dc3abb32238b053aeb3d823c30afdacbd/flux-2-klein-4b-Q8_0.gguf \
  -o models/flux-2-klein-4b-Q8_0.gguf
curl -fL --retry 3 \
  https://huggingface.co/unsloth/Qwen3-4B-GGUF/resolve/22c9fc8a8c7700b76a1789366280a6a5a1ad1120/Qwen3-4B-Q8_0.gguf \
  -o models/Qwen3-4B-Q8_0.gguf
curl -fL --retry 3 \
  https://huggingface.co/Comfy-Org/flux2-klein-4B/resolve/5f526678002e43af5551dadb73ce2e8c91b43afe/split_files/vae/flux2-vae.safetensors \
  -o models/flux2-vae.safetensors
cat > models/SHA256SUMS <<'SHA256'
0bba6951258ec8f92d51114a8fa13e66828297bfff58a738f52729b3ef66fa28  models/flux-2-klein-4b-Q8_0.gguf
eed555233267a33c7e8ee31682762cc7751b3f6d224039086e0e846f05fffa5d  models/Qwen3-4B-Q8_0.gguf
868fe7b343cc8f3a19dbcfcafbc3d5f888802be3f89bd81b65b3621a066ce8f3  models/flux2-vae.safetensors
SHA256
shasum -a 256 -c models/SHA256SUMS
```

三行都必须显示 `OK`。复制配方时保留版本 URL 和校验值，不要改用会变化的 `main` 下载链接。

## 先启动引擎，再启动 Infercat

在刚才选择 `IMAGE_BACKEND` 的终端和源码目录中运行：

```sh
build/bin/sd-server \
  --diffusion-model models/flux-2-klein-4b-Q8_0.gguf \
  --llm models/Qwen3-4B-Q8_0.gguf --vae models/flux2-vae.safetensors \
  --backend "$IMAGE_BACKEND" --params-backend "$IMAGE_BACKEND" \
  --eager-load --diffusion-fa --steps 4 --cfg-scale 1 \
  --sampling-method euler --scheduler flux2 --seed 42 --threads 6 \
  --listen-ip 127.0.0.1 --listen-port 18155
```

让它继续运行。在另一个终端检查引擎的原生探测接口：

```sh
curl -fsS http://127.0.0.1:18155/v1/models
```

这个固定版本返回通用名称 `sd-cpp-local`。下面通过 Infercat 的模型参数，为已加载的权重指定朋友能认出的名称。引擎提供的是 [OpenAI 形式的图片接口子集](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/examples/server/api.md)；Infercat 的每个任务请求一张 1024×1024 的 base64 图片。

保持聊天引擎运行。以默认端口上的 Ollama 为例：

```sh
infercat serve --upstream http://127.0.0.1:11434 \
  --upstream-images http://127.0.0.1:18155 \
  --upstream-images-model FLUX.2-klein-4B-Q8_0 --web-url https://infercat.ai
```

三个图片参数分别是 `--upstream-images`（基础 URL，不带 `/v1`）、`--upstream-images-model`（不填则取探测到的第一个模型 ID）和 `--upstream-images-key`（需要认证的上游 bearer）。这份回环 sd.cpp 配方不需要上游密钥。参数会保存在 `config.json` 中；URL、密钥或模型变更在下次 `serve` 时生效，每个已配置的引擎都有定期探测，重载时也会重新探测。继续使用原数据目录就能保留邀请；如果另开一台独立主机，请在所有主机、密钥和状态命令中传入同一个 `--data-dir DIR`。

主机的 `--models` 只约束文字模型。若朋友密钥设置了模型白名单，请在聊天模型之外加入 `FLUX.2-klein-4B-Q8_0`。即使图片引擎健康，没有向该密钥开放的模型也不会出现在它的 `/me.host.images` 中。

```sh
infercat keys add alice --max-queued-images 8 --daily-images 20
infercat keys limits alice --max-queued-images 4 --daily-images 10
infercat keys list
infercat status
```

默认每把密钥最多 **8 张图片排队**、每个 UTC 日 **20 张图片**；缺失或为零的字段采用这两个默认值；任意负值表示不限，返回值统一为 `-1`。示例将 Alice 改为排队 4 张、每天 10 张。`keys list` 显示 IMAGES/DAY 和 IMAGE QUEUE。图片任务消耗 RPM，并有自己的执行容量和每日额度预留；不占用密钥的文字/语音并发计数，也不消耗聊天 token。单次交互请求排在批量种植任务前面；已经开始生成的图片不会被抢占。

每天的额度在批次接收时整批预留：要么所有提示词排队，要么以 429 整批拒绝，不创建任何任务行。尚未结算的预留跨 UTC 午夜继续有效，已发出的任务计入结算当天。重启会中断未结束任务，不会重放排队工作。

取消排队任务不扣图片额度。生成中取消会等这张图完成，保留图片并扣一张。引擎明确失败且没有输出时释放预留；已发出但结果不确定时扣一张。响应丢失不会触发自动重发。主机一次只发送一个图片生成请求，等待完整 HTTP 响应的时间最多为 15 分钟。已发送但丢失结果的请求返回 `image_abandoned` 并计数；发出下一个请求前，主机会等待一次新的成功健康探测。远程服务可能在断线后继续计算，健康探测不能证明它已停止。

输出仅为 PNG/JPEG，**每张最多 8 MiB**，私密保存在[数据目录](DATA-DIRECTORY.zh-CN.md)下的 `runs/<key-id>/images/<run-id>`。保留 **7 天**，同时受独立的**每把密钥 256 MiB 可读取图片空间**限制，满时先淘汰最旧的输出。操作系统拒绝删除的旧文件可能暂时超出可读取图片的空间上限；主机会记录错误、在下次清理时重试，并在 status 中显示待清理文件数。Save 下载副本，Discard 删除主机上的输出。图片已丢失的标记保留到任务记录过期。原有任务上限仍适用：每把密钥 16 个未结束任务、每台主机 64 个，每把密钥保留 100 条记录。

## 检查完整流程

`infercat status` 检查运行中的主机、聊天上游和隧道。它没有专门的图片健康状态行；显示模型白名单不代表图片探测成功。要查看邀请真正可用的能力，将下面的占位符替换为 Alice 的邀请**码**，并让这个本地桥接进程保持运行。它会附上邀请的密钥，curl 无需另加 bearer。第二段命令在另一个终端执行。

```sh
infercat connect 'PASTE_INVITE_HERE' --listen 127.0.0.1:11435
```

```sh
curl -fsS http://127.0.0.1:11435/me | python3 -c \
  'import json,sys; m=json.load(sys.stdin); print(m["host"].get("images")); print(m["usage"].get("today_images",0))'
curl -fsS http://127.0.0.1:11435/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"A red fox beside a forest puddle at dawn, no text.","n":1,"size":"1024x1024","response_format":"b64_json"}' \
  -o generated.json
python3 -c 'import base64,json; r=json.load(open("generated.json")); open("image.png","wb").write(base64.b64decode(r["data"][0]["b64_json"]))'
```

修改 Alice 的限额后，`/me` 应打印包含 `{'model': 'FLUX.2-klein-4B-Q8_0', 'retention_days': 7, 'queue_cap': 4, 'queued': 0}` 的内容，再打印当天图片计数。打开 `image.png`，重新运行 `/me` 命令，确认计数增加。同步接口等待的也是同一个持久化任务；关闭 curl 不会取消它。

支持图片任务的应用会在附件按钮旁显示图片方形按钮。进入图片模式后，每段文字是一条提示词，每张图有自己的任务行，批次结果集中在 Images 中。发送前会检查排队上限，超限整批拒绝；生成中的任务行完成后显示图片，Images 面板提供 Save、Edit prompt 和 Discard。模型名称和保留天数由主机提供。这一段描述的是支持图片任务的应用；只升级主机不会更新旧版应用。

用完后在桥接、主机和引擎所在的终端分别按 Ctrl-C 停止。这些命令不安装后台任务。

## 一台 Mac 上的实测

实测日期为 **2026-09-10**，来自 spike 134：Apple M5 Max，18 核 CPU / 40 核 GPU，64 GiB，macOS 26.5.2；引擎、ggml 和三个权重采用上文固定版本，Metal、6 线程、Euler 4 步、CFG 1、seed 42、1024×1024。并行聊天使用 Ollama 0.33.3、`hf.co/unsloth/gemma-4-E2B-it-qat-GGUF:UD-Q4_K_XL`，模型 digest 为 `6c125f6ef484859c8df0fa58e94a58c66bdcf922b52df7e588d1ec5a79b0d461`，上下文 8192，关闭 thinking。

| 观察项 | 实测结果 |
|---|---|
| 预加载服务启动后的首张完整图片 | 16.21 秒；另有 7.73 秒的权重加载 |
| 四张热启动样图 | 15.24–15.95 秒 |
| 三轮配对：仅图片 / 聊天持续生成期间的图片 | 15.29–16.36 秒 / 18.70–21.87 秒 |
| 聊天：图片前 / 图片生成期间 / 图片刚完成后 | 161.8–174.1 / 84.8–101.1 / 149.0–170.1 token/秒 |
| 两模型仍驻留、空闲 126.7 秒后的聊天 | 176.2 token/秒 |

这些是原生回环接口返回完整结果的时间，不是预览首帧，也不含中继或应用延迟。图片生成期间的 token 速率不含生成结束后的较快尾段；配对轮次中聊天约慢了 42–48%。正常桌面活动仍在进行。这些数字不是 CUDA 实测，也不是对其他主机的承诺。

## 遇到问题时

- **引擎不应答或看不到图片按钮：** 检查 `/v1/models`、引擎终端、基础 URL 和两层模型白名单。修改已保存参数后重启 `serve`，定期探测会发现稍后启动的引擎。健康且已共享的图片模型可用之前，`/me.host.images` 不存在。仅支持文字的旧主机或应用不会提供该按钮。列表和图片读取也消耗 RPM；合并刷新请求，并在 429 时遵守 `Retry-After`。
- **只返回 URL 被拒绝：** 服务必须返回 `data[0].b64_json`；Infercat 不会抓取远程图片 URL。使用上文固定版本的原生接口。
- **输出过大或损坏：** 解码后的 PNG/JPEG 每张不得超过 8 MiB、每边不超过 4096 像素；接口请求的是 1024×1024。仅 URL、损坏或超限的已发出请求都属于结果不确定，仍可能扣一张每日额度。
- **当天额度用完（`image_budget_exhausted`）：** 等到下一个 UTC 日，或修改朋友的 `--daily-images`。**队列已满（`image_queue_full`）：** 等排队任务完成、取消一条排队任务、减少提示词数量，或修改 `--max-queued-images`。批次要么整批接受，要么整批拒绝；不要自动重试结果不确定的提交。
