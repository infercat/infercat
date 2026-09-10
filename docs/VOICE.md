# Give your host voice

Run an audio-enabled Infercat host (0.1.2 or a current source build), your usual chat engine, and two OpenAI-compatible audio services. A 0.1.1 host cannot expose these routes. Transcription needs `POST /v1/audio/transcriptions` with multipart audio and `verbose_json` duration; speech needs `POST /v1/audio/speech` returning audio. Each service must answer `GET /v1/models` or `/health`. The services may share a server, but you configure each route explicitly.

The app then offers the microphone and Listen. Friends never choose an audio model: the host supplies its configured id, or the first probed id. Settings shows those ids. Keep the engine ports on loopback; Infercat exposes the authenticated audio routes through the same invite as chat.

## Mac: Speaches and Kokoro

Measured on an Apple M5 Max with 64 GiB, macOS 26.5.2, Python 3.12.13 and uv 0.12.5. Both engines below use CPU; this Whisper runtime does not use Metal. Install [uv](https://docs.astral.sh/uv/getting-started/installation/) and FFmpeg (`brew install ffmpeg`), then use separate terminals. Downloads require disk space and an internet connection; later runs reuse the caches.

**Transcription.** This is [Speaches](https://github.com/speaches-ai/speaches/tree/993994f7984bf3fe9655b267448328cf66fccb42) with a multilingual Whisper turbo checkpoint, not the tiny English-only model.

```sh
git clone https://github.com/speaches-ai/speaches.git
cd speaches
git checkout 993994f7984bf3fe9655b267448328cf66fccb42
uv python install 3.12.13
uv sync --frozen --python 3.12.13 --no-dev
export HF_HOME="$PWD/.models"
ENABLE_UI=false LOG_LEVEL=info WHISPER__INFERENCE_DEVICE=cpu \
  WHISPER__COMPUTE_TYPE=int8 WHISPER__CPU_THREADS=4 STT_MODEL_TTL=-1 \
  uv run --no-sync uvicorn --factory speaches.main:create_app \
  --host 127.0.0.1 --port 18085
```

While that server runs, install the model using its native management route in another terminal:

```sh
curl -fsS -X POST http://127.0.0.1:18085/v1/models/deepdml/faster-whisper-large-v3-turbo-ct2
```

The actual model id is `deepdml/faster-whisper-large-v3-turbo-ct2`. Speaches lists locally installed models: download it before starting Infercat to avoid a successful health probe followed by a model-not-installed refusal. The engine versions above are pinned; Speaches' model download follows the model repository's current revision. Our cached revision was `4df90f75321148c3a29a9e2351b7ddf8f5b115a8`; a changed checkpoint can change these results. The pinned [model card](https://huggingface.co/deepdml/faster-whisper-large-v3-turbo-ct2/tree/4df90f75321148c3a29a9e2351b7ddf8f5b115a8) labels the checkpoint MIT. Speaches is MIT. `STT_MODEL_TTL=-1` keeps the loaded model resident until this process stops.

**Speech.** [Kokoro-FastAPI](https://github.com/remsky/Kokoro-FastAPI/tree/b37ad0c80728c32ad4673ea063194fcd1f551451) serves Kokoro 82M v1.0 directly on the OpenAI route. The pinned checkout includes the voices and checksum-verifies its model download.

```sh
git clone https://github.com/remsky/Kokoro-FastAPI.git
cd Kokoro-FastAPI
git checkout b37ad0c80728c32ad4673ea063194fcd1f551451
uv sync --frozen --python 3.12.13 --extra cpu --no-dev
uv run --no-sync python docker/scripts/download_model.py --output api/src/models/v1_0
USE_GPU=false DEVICE_TYPE=cpu DEFAULT_VOICE=af_heart \
  MODEL_DIR=src/models VOICES_DIR=src/voices/v1_0 \
  PYTHONPATH="$PWD:$PWD/api" uv run --no-sync uvicorn api.src.main:app \
  --host 127.0.0.1 --port 18079
```

`af_heart` is English. For a Mandarin default, restart that command with `DEFAULT_VOICE=zf_xiaobei`; its `z` prefix selects the Mandarin pipeline. This pin includes `misaki[zh]`, the required pronunciation frontend. The no-voice-field app request uses that default. A previously played reply may remain in the app's memory cache; test a new reply after changing voices. Kokoro-FastAPI and the [Kokoro weights](https://huggingface.co/hexgrad/Kokoro-82M) are Apache-2.0. Mandarin output was generated successfully on this Mac; this is not a claim that it matches a larger model's quality.

In another terminal, check both servers before starting Infercat:

```sh
curl -fsS http://127.0.0.1:18085/v1/models
curl -fsS http://127.0.0.1:18079/v1/models
curl -fsS http://127.0.0.1:18079/v1/audio/voices
```

## Connect the engines to your host

Keep your chat engine running. For Ollama on its usual port:

```sh
infercat serve --upstream http://127.0.0.1:11434 \
  --upstream-transcribe http://127.0.0.1:18085 \
  --upstream-transcribe-model deepdml/faster-whisper-large-v3-turbo-ct2 \
  --upstream-speech http://127.0.0.1:18079 --upstream-speech-model kokoro \
  --max-transcription-seconds 300 --web-url https://infercat.ai
```

Use your existing data directory to retain invites; for a separate host, use `--data-dir` consistently on `serve` and `keys` commands. These audio flags are remembered. URL, key and model changes take effect on the next `serve`; reload re-probes the configured engines. Authenticated upstreams use `--upstream-transcribe-key` and `--upstream-speech-key`. Supply service base URLs without `/v1`.

If you use `--models` on the host or a friend key, include the two audio ids as well as the chat model. An omitted or unknown client audio model is replaced with the host's chosen id, then the allowlist still applies. `/me.host.audio` contains that id for an available route and `null` for an unavailable one. A healthy chat server alone does not enable audio.

```sh
infercat keys add alice --daily-audio-seconds 3600 --daily-speech-chars 200000
infercat keys limits alice --daily-audio-seconds 1800 --daily-speech-chars 50000
```

Defaults are 3600 transcription seconds and 200000 speech characters per key per UTC day. Speech characters are Unicode code points. Audio shares the key's request/concurrency limits and the engine queue, but spends no chat tokens. The console's friend limits editor has **Audio seconds per day** and **Speech characters per day**; usage shows seconds/characters when nonzero, including each friend's Now line. The app shows its own recording duration and spoken-character count, not an audio balance in the daily-token meter.

Transcription reserves before dispatch: a measurable WAV/FLAC reserves its duration; other formats reserve the host ceiling (300 s here). A measured upload over that ceiling is refused. A normal JSON request is sent upstream as verbose JSON, then returned as `{text}`, so the host can settle to the engine's measured duration. Unknown duration or an interrupted call charges the reservation; `text`/`srt`/`vtt` cannot supply duration and also charge the reservation. An engine-reported overrun is charged in full and can cross the day's budget; later requests are refused. See [the exact limits and accounting](LIMITS.md#audio-budgets-078). Uploads are capped at 25 MiB.

## Check the complete flow

First test each engine locally with a recording you are willing to transcribe:

```sh
curl -fsS http://127.0.0.1:18085/v1/audio/transcriptions \
  -F model=deepdml/faster-whisper-large-v3-turbo-ct2 -F file=@note.wav \
  -F response_format=verbose_json -F language=en
curl -fsS http://127.0.0.1:18079/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"kokoro","input":"Hello from Infercat.","response_format":"mp3"}' \
  -o speech.mp3
afplay speech.mp3
```

Then open the invite, allow microphone access, tap the mic, wait for the recording line, speak and tap Stop. The transcript remains editable until Send. Send it and press Listen on the reply; confirm audio counts in the console. A denied microphone needs a browser permission change; an unavailable capability needs the host/engine checks above. Closing a chat or hiding its page releases the retained microphone stream. Stop each foreground engine with Ctrl-C when finished; these commands install no background jobs.

### Measurements on this Mac

Measured 2026-09-10 using synthetic/public test audio, with model files already downloaded. These are individual observations, not throughput promises; other host workloads and relay/network time add latency.

| Path | Input | Observed elapsed time |
|---|---|---|
| Speaches CPU 4/int8, direct HTTP |10.0 s English WAV|5.422 s first model-load request;4.112 and4.152 s warm|
| Kokoro CPU `af_heart`, local Infercat gateway → Chrome 152 MSE player |67 English characters|1.419 s to first playback on the first request;0.425 s on the warm request|
| Kokoro CPU `zf_xiaobei`, direct HTTP |28 Mandarin characters/punctuation|1.26 s explicit voice;1.55 s default voice, for 6.298 s of WAV|

First playback means the player's native `playing` event, not the HTTP headers; the warm English response's first bytes arrived at 0.425 s. This measurement used loopback-network permission in a disposable browser, not a production browser-setting change. Earlier real-tunnel proof also completed recorder → Speaches transcript → edited chat turn → Kokoro MP3 playback; its latency is not inferred from the local numbers above.

### Faster path measured, not a supported recipe

The experimental adapter under [hack/asr-shim](../hack/asr-shim/README.md) runs Fun-ASR-Nano Q8_0 in pinned audio.cpp's resident Metal server. Same-clip local medians were 2286/1891 ms EN/ZH with per-request CPU exec,917/557 ms resident CPU 4, and214/154 ms resident Metal 4; actual browser-tunnel medians improved 2653/2123→519/355 ms. These are different clips from the 10 s Whisper row. The founder found Q8_0 quality excellent; F16 comparison was dropped. The GGUF is under **FunASR Model Open Source License v1.1**, while the `-hf` export says Apache-2.0; audio.cpp is Apache-2.0. This measured proof adapter is not the native supported Speaches recipe above.

## NVIDIA Linux: unmeasured here

Use the same pinned Speaches checkout and its [CUDA compose file](https://github.com/speaches-ai/speaches/blob/993994f7984bf3fe9655b267448328cf66fccb42/compose.cuda.yaml), with Docker and NVIDIA Container Toolkit already configured:

```sh
# From the pinned Speaches checkout:
docker build -t infercat-speaches-voice \
  --build-arg BASE_IMAGE=nvidia/cuda:12.6.3-base-ubuntu24.04 .
docker run --rm --gpus all -p 127.0.0.1:18085:8000 \
  -v infercat-speech-cache:/home/ubuntu/.cache/huggingface/hub infercat-speaches-voice
# In another terminal, install the model through Speaches' native management route:
curl -fsS -X POST http://127.0.0.1:18085/v1/models/deepdml/faster-whisper-large-v3-turbo-ct2
```

The build uses the same CUDA 12.6.3 base as that compose file; the command publishes only loopback port 18085. Point `--upstream-transcribe` at `http://127.0.0.1:18085`; the same Kokoro CPU recipe can run alongside it. This Linux path and its latency were **not measured on this Mac**. The Mac row above is CPU evidence, not CUDA evidence.
