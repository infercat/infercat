# audio.cpp ASR proof shim (100)

One local model, one CLI process per request. Python standard library only; no ML Python packages. Bind is loopback-only. This is a try-out tool, not a production server. `/v1/models` advertises `FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0`; multipart `/v1/audio/transcriptions` accepts `file`, optional `language` (`auto`, `zh`, `en`, `ja`), and `response_format` (`json` or `verbose_json`). The latter includes duration measured from decoded PCM; ordinary JSON contains only `text`.

## Pinned build

Verified on macOS arm64 (M5 Max, 64 GiB), Python 3.9.6, CMake 3.31.6 and FFmpeg 9.0.1. CMake/FFmpeg are build/audio tools, not ML dependencies.

```sh
git clone https://github.com/0xShug0/audio.cpp.git
git -C audio.cpp checkout fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416
cmake -S audio.cpp -B audio.cpp/build/cpu -DCMAKE_BUILD_TYPE=Release \
  -DENGINE_ENABLE_METAL=OFF -DENGINE_ENABLE_OPENMP=OFF \
  -DAUDIOCPP_DEPLOYMENT_BUILD=ON -DAUDIOCPP_MODEL_SET=custom \
  -DAUDIOCPP_MODELS=fun_asr_nano
cmake --build audio.cpp/build/cpu --target audiocpp_cli -j 4
curl -fL https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-GGUF/resolve/ce72677f84900f0dc57f498ace253bfb3c9155b6/fun-asr-nano-2512-q8_0.gguf -o fun-asr-nano-2512-q8_0.gguf
# Verify before running: 1,045,334,432 bytes; SHA-256:
# 4d727357574b079b7f43336b2930f39da086ca02f5d8d50872090b4c1c3d5e0a
shasum -a 256 fun-asr-nano-2512-q8_0.gguf
python3 hack/asr-shim/server.py --cli "$PWD/audio.cpp/build/cpu/bin/audiocpp_cli" \
  --model "$PWD/fun-asr-nano-2512-q8_0.gguf" --port 18082
```

Point the existing isolated Infercat host at `--upstream-transcribe http://127.0.0.1:18082 --upstream-transcribe-model FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0`, and include that id in its host model allowlist. Preserve its data directory to preserve invites. No key or invite belongs in this directory.

FFmpeg decodes WebM/MP4/MP3 uploads to mono 16 kHz PCM WAV; audio.cpp consumes WAV. Each request has a private temporary directory, removed on completion/failure. Maximum upload is 25 MiB, decoded duration 300 seconds, one CLI process at a time; extra requests get 429. Decode/CLI timeouts are 30/300 seconds and failures return 502. No audio/transcript is retained and CLI stdout/stderr is discarded. No streaming, timestamps, resident-model cache, backend framework, or alternate decoder.

## Check

```sh
python3 hack/asr-shim/check.py http://127.0.0.1:18082 /path/to/english.wav /path/to/mandarin.webm
```

The test expects the spike's public Mandarin sample (5.616 seconds), encoded as WebM: [publisher sample](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512/resolve/main/example/zh.mp3). It checks discovery, JSON/verbose JSON, WAV/WebM decoding, unknown route, missing body/file and unsupported response format. Measured: 7 passed / 0 failed / 0 skipped. The private PM report is `docs/spikes/100-asr.md` in infercat-pm.

## Runtime and licence boundaries

[audio.cpp at the pin](https://github.com/0xShug0/audio.cpp/tree/fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416) has Metal support, but this build explicitly disables it; this spike verifies CPU only. Its [Fun-ASR guide](https://github.com/0xShug0/audio.cpp/blob/fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416/docs/models/fun_asr_nano.md) documents CPU/CUDA. No NVIDIA result is claimed.

The [GGUF model card](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-GGUF) assigns these weights the **FunASR Model Open Source License v1.1**; the [-hf export](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-hf) says Apache-2.0. Do not substitute the latter label for this GGUF. The runtime itself is Apache-2.0. No weights or runtime binaries are redistributed here.
