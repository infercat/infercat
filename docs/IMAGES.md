English · [简体中文](IMAGES.zh-CN.md)

# Give your host image generation

A friend describes a picture; your host makes one image at a time. Chats keep going, a little slower while an image is being made. Use a current Infercat source build containing image jobs (main `3f9d440` or later, for 0.1.4), your usual chat engine, and a separate OpenAI-compatible images engine. Older hosts do not offer this capability.

This recipe runs native stable-diffusion.cpp, with no wrapper. Keep its port on loopback; friends reach the authenticated image routes through their existing invite. Downloads total **8.92 GB** before source and build files. The measured Mac had 64 GiB of unified memory; the image server's peak physical footprint was 9.90 GiB. That observation is not a minimum-memory specification.

## Build the pinned engine

Install Git, CMake, curl and Python 3, plus a C++ compiler. On macOS, use Xcode Command Line Tools and an installed CMake (for example Homebrew's `cmake`). On NVIDIA Linux, also install a working NVIDIA driver and CUDA Toolkit; `nvcc` must be on PATH. The Linux commands below are checked against the pinned [build instructions](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/docs/build.md), **not executed or timed on this Mac**.

Start in a directory where `infercat-images` does not already exist:

```sh
mkdir infercat-images
cd infercat-images
git clone --no-checkout https://github.com/leejet/stable-diffusion.cpp.git
cd stable-diffusion.cpp
git checkout 469fc49bb7ded7400c60fa6eb382b6e4bc02cc20
git submodule update --init --recursive
```

Choose **one** build for your machine, then continue in that same shell and checkout. The recursive submodule checkout pins ggml to `e20c3a14aa70ee84ca58499814206dd08d8026bc`. The engine is MIT-licensed.

**macOS, Apple silicon / Metal:**

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DSD_METAL=ON -DSD_SERVER_BUILD_FRONTEND=OFF
cmake --build build --target sd-server -j 6
IMAGE_BACKEND=metal
```

**Linux, NVIDIA / CUDA:**

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DSD_CUDA=ON -DSD_SERVER_BUILD_FRONTEND=OFF
cmake --build build --target sd-server -j 6
IMAGE_BACKEND=cuda0
```

`cuda0` selects the first CUDA device for all three components; it does not reserve that card against your chat engine. Use the pinned [backend guide](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/docs/backend.md) if you need another device. No workstation performance is implied here.

## Download the three model files

Use the distilled **FLUX.2 klein 4B**, not the base variant or the 9B model. Q8_0 is the quantisation measured here; the text encoder and VAE are required too. Each linked publisher card at this revision declares Apache-2.0. Qwen here encodes image prompts; it does not replace your chat engine.

| Component / pinned publisher | File | Bytes |
|---|---|---:|
| [FLUX.2 klein 4B, distilled Q8_0](https://huggingface.co/leejet/FLUX.2-klein-4B-GGUF/tree/3b1f5a9dc3abb32238b053aeb3d823c30afdacbd) | `flux-2-klein-4b-Q8_0.gguf` | 4,300,629,440 |
| [Qwen3-4B text encoder, Q8_0](https://huggingface.co/unsloth/Qwen3-4B-GGUF/tree/22c9fc8a8c7700b76a1789366280a6a5a1ad1120) | `Qwen3-4B-Q8_0.gguf` | 4,280,405,792 |
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

All three lines must say `OK`. Keep the revision URLs and checksums together when copying this recipe; do not substitute a moving `main` download.

## Start the engine, then Infercat

In the same checkout and shell where you selected `IMAGE_BACKEND`:

```sh
build/bin/sd-server \
  --diffusion-model models/flux-2-klein-4b-Q8_0.gguf \
  --llm models/Qwen3-4B-Q8_0.gguf --vae models/flux2-vae.safetensors \
  --backend "$IMAGE_BACKEND" --params-backend "$IMAGE_BACKEND" \
  --eager-load --diffusion-fa --steps 4 --cfg-scale 1 \
  --sampling-method euler --scheduler flux2 --seed 42 --threads 6 \
  --listen-ip 127.0.0.1 --listen-port 18155
```

Leave it running. In another terminal, check the native probe:

```sh
curl -fsS http://127.0.0.1:18155/v1/models
```

This pin reports the generic id `sd-cpp-local`. Infercat's explicit model flag below gives friends the descriptive id for these loaded weights. The pinned server supports the [OpenAI-shaped images subset](https://github.com/leejet/stable-diffusion.cpp/blob/469fc49bb7ded7400c60fa6eb382b6e4bc02cc20/examples/server/api.md); Infercat requests one 1024×1024 base64 image per job.

Keep your chat engine running. With Ollama on its usual port:

```sh
infercat serve --upstream http://127.0.0.1:11434 \
  --upstream-images http://127.0.0.1:18155 \
  --upstream-images-model FLUX.2-klein-4B-Q8_0 --web-url https://infercat.ai
```

The three image flags are `--upstream-images` (base URL, without `/v1`), `--upstream-images-model` (otherwise the first probed id), and `--upstream-images-key` (bearer for an authenticated upstream). This loopback sd.cpp recipe needs no upstream key. Flags are remembered in `config.json`; URL/key/model changes take effect on the next `serve`, and each configured engine is re-probed periodically as well as on reload. Keep using your existing data directory to retain invites; for a separate host, pass the same `--data-dir DIR` to every host/key/status command.

The host’s `--models` pin applies only to text. If a friend key has a model allowlist, include `FLUX.2-klein-4B-Q8_0` as well as the chat model. A healthy image engine whose model is not shared with this key does not appear in its `/me.host.images`.

```sh
infercat keys add alice --max-queued-images 8 --daily-images 20
infercat keys limits alice --max-queued-images 4 --daily-images 10
infercat keys list
infercat status
```

Defaults are **8 queued images** and **20 images per key per UTC day**; absent/zero fields use those defaults; any negative value means unlimited and is reported as `-1`. The example changes Alice to 4 queued and 10 a day. `keys list` shows IMAGES/DAY and IMAGE QUEUE. Image jobs use RPM and their own worker/day reservation; they do not occupy the key's text/audio concurrency counter or spend chat tokens. Interactive asks go before planted batches; a running image is never preempted.

The daily budget is reserved for the whole batch at admission: either every prompt is queued, or a 429 refuses the whole batch before any row exists. Pending reservations count across UTC midnight; dispatched work charges its settlement day. Restart interrupts unfinished jobs without replaying queued work.

A queued cancellation costs no image. Cancel during generation lets that image finish, keeps it and charges one. A definitive engine failure with no output releases the reservation; an ambiguous dispatched outcome charges one. A lost response never causes automatic resubmission. The host sends one image-generation request at a time and waits up to 15 minutes for its complete HTTP response. A sent-but-lost request returns `image_abandoned` and counts; the host waits for a fresh successful health probe before the next dispatch. A remote server may still be computing after the connection is lost; a health probe cannot prove it stopped.

Outputs are PNG/JPEG, at most **8 MiB each**, stored privately at `runs/<key-id>/images/<run-id>` under the [data directory](DATA-DIRECTORY.md). Retention is **7 days**, subject to a separate **256 MiB per-key budget for retained, servable images**, oldest evicted first. Files that the operating system refuses to unlink may remain outside that servable budget: the host logs them, retries each sweep, and reports the pending-cleanup count in status. Save downloads a copy; Discard removes the host's output. Gone markers remain until the run record expires. The existing run bounds also apply: 16 live per key, 64 live per host and 100 retained records per key.

## Check the complete flow

`infercat status` checks the running host, chat upstream and tunnel. It has no dedicated image-health line; a printed model pin is not a successful image probe. To inspect the invite's actual capability, replace the placeholder below with Alice's invite **code** and leave this bridge running. It supplies the invite's key; curl needs no additional bearer. Use another terminal for the second block.

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

The `/me` printout should include `{'model': 'FLUX.2-klein-4B-Q8_0', 'retention_days': 7, 'queue_cap': 4, 'queued': 0}` for Alice after the limits change, then her current daily count. Open `image.png`, and repeat the `/me` command to see the count increase. The synchronous route waits for the same durable job; closing curl does not cancel it.

In the image-enabled app, friends get a picture square beside Attach. Image mode treats each paragraph as a prompt, shows a run row per image, and keeps batch results together in Images. The queue check refuses an over-cap batch before sending; running rows become finished pictures, with Save, Edit prompt and Discard in the Images sheet. The host supplies the model and retention figure. This paragraph describes the image-jobs app; a newer host alone does not update an older app build.

Stop the foreground bridge, host and engine with Ctrl-C when finished. These commands install no background jobs.

## Measured on one Mac

Measured **2026-09-10** in spike 134: Apple M5 Max, 18 CPU / 40 GPU cores, 64 GiB, macOS 26.5.2; the engine, ggml and all three weight pins above, Metal, six threads, four Euler steps, CFG 1, seed 42, 1024×1024. The paired chat engine was Ollama 0.33.3 with `hf.co/unsloth/gemma-4-E2B-it-qat-GGUF:UD-Q4_K_XL`, model digest `6c125f6ef484859c8df0fa58e94a58c66bdcf922b52df7e588d1ec5a79b0d461`, context 8192, thinking off.

| Observation | Measured result |
|---|---|
| First completed image, after eager server startup | 16.21 s; tensor loading separately 7.73 s |
| Four warm sample images | 15.24–15.95 s |
| Three paired rounds: image alone / during continuing chat | 15.29–16.36 s / 18.70–21.87 s |
| Chat before / during active image / immediately after | 161.8–174.1 / 84.8–101.1 / 149.0–170.1 tokens/s |
| Chat after 126.7 idle seconds, both models still resident | 176.2 tokens/s |

These are complete-response times over native loopback, not first-preview times or relay/app latency. During-image token rates exclude the faster tail after generation ends; chat slowed about 42–48% in those paired rounds. Normal desktop activity continued. These measurements are neither a CUDA result nor a promise for another host.

## If something fails

- **Engine not answering or image control absent:** check `/v1/models`, the engine terminal, the base URL and both model allowlists. Restart `serve` after changing saved flags; the periodic probe recovers an engine that starts late. `/me.host.images` is absent until a healthy, shared model is available. A text-only host/app build cannot provide the control. List and output reads spend RPM too; coalesce refreshes and honor `Retry-After` on 429.
- **URL-only result refused:** the server must return `data[0].b64_json`; Infercat never fetches remote output URLs. Use the pinned native route above.
- **Output too large or malformed:** each decoded PNG/JPEG must fit 8 MiB and 4096 pixels per side; the route requests 1024×1024. URL-only, malformed and over-limit dispatched results are ambiguous and can still cost one daily image.
- **Day's budget exhausted (`image_budget_exhausted`):** wait for the next UTC day or change the friend's `--daily-images` limit. **Queue full (`image_queue_full`):** let queued work finish, cancel a queued row, submit fewer prompts or change `--max-queued-images`. A batch is admitted whole or refused whole; never retry an uncertain submission automatically.
