# Resident audio.cpp ASR proof adapter (100)

One checkpoint in the runtime's own resident server; a small Python standard-library adapter handles browser audio and the gateway response contract. No ML Python packages or model/backend framework. This is a loopback try-out tool, not a production service.

`/v1/models` probes the resident runtime. Multipart `/v1/audio/transcriptions` accepts `file`, optional `language` (`auto`, `zh`, `en`, `ja`), and `response_format` (`json` or `verbose_json`). FFmpeg decodes to mono 16 kHz PCM WAV. The adapter passes its private temporary WAV path to the local resident server, returns only `text`, and includes decoded duration for verbose JSON. Model id remains `FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0`.

## Reproduce

Verified on M5 Max / 64 GiB, Python 3.9.6, CMake 3.31.6, FFmpeg 9.0.1. In a scratch directory:

```sh
git clone https://github.com/0xShug0/audio.cpp.git
git -C audio.cpp checkout fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416
cmake -S audio.cpp -B audio.cpp/build/metal -DCMAKE_BUILD_TYPE=Release \
  -DENGINE_ENABLE_METAL=ON -DENGINE_ENABLE_OPENMP=OFF \
  -DAUDIOCPP_DEPLOYMENT_BUILD=ON -DAUDIOCPP_MODEL_SET=custom \
  -DAUDIOCPP_MODELS=fun_asr_nano
cmake --build audio.cpp/build/metal --target audiocpp_server -j 4
curl -fL https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-GGUF/resolve/ce72677f84900f0dc57f498ace253bfb3c9155b6/fun-asr-nano-2512-q8_0.gguf -o fun-asr-nano-2512-q8_0.gguf
shasum -a 256 fun-asr-nano-2512-q8_0.gguf
# Expected 1,045,334,432 bytes; SHA-256:
# 4d727357574b079b7f43336b2930f39da086ca02f5d8d50872090b4c1c3d5e0a
# Copy this directory's runtime.example.json beside the GGUF as runtime.json.
audio.cpp/build/metal/bin/audiocpp_server --config runtime.json
# In another terminal, from the Infercat repo:
python3 hack/asr-shim/server.py --runtime-port 18084 --port 18082
```

The example binds the runtime to 127.0.0.1:18084, disables its UI, loads eagerly, disables idle unloading, uses Metal with 4 threads, and retains ITN=true / max_tokens=512 / auto 30-second chunks. A CPU comparison uses `ENGINE_ENABLE_METAL=OFF` and `backend: cpu`. GPU/library warmup and model load occur at server startup/first use, not on every take; warm it on representative clips before handing over the invite.

Point the existing isolated Infercat host at `--upstream-transcribe http://127.0.0.1:18082 --upstream-transcribe-model FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0`, include that id in its host allowlist, and preserve its data directory/invite. The runtime accepts server-local paths and must remain loopback-only; the adapter generates those paths itself and does not accept one from clients.

Limits: 25 MiB upload, 300-second decoded audio, one adapter request at a time (extra calls get429), 30-second decode/300-second runtime timeout. Private temporary uploads/WAVs are removed on handled completion/failure. No recording/transcript is logged by the adapter. Server-Timing reports upload+multipart parsing, decode, runtime round trip, and the native preparation/inference timer; inference is a subset of runtime, not additive. No streaming or new persistent audio state.

## Proof and settings

```sh
python3 hack/asr-shim/check.py http://127.0.0.1:18082 /path/to/english.wav /path/to/mandarin.webm
python3 hack/asr-shim/profile.py /path/to/english.wav /path/to/mandarin.webm http://127.0.0.1:18082
```

The Mandarin check uses the publisher's [5.616-second sample](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512/resolve/main/example/zh.mp3), encoded as WebM. Seven live route/format checks passed. Same-payload local medians: per-request CPU exec 2286/1891 ms EN/ZH; resident CPU (4 threads) 917/557; resident Metal (4 threads) 214/154. Actual browser tunnel medians (two runs) changed from2653/2123 to519/355 ms; transcripts matched. Initial Metal startup was8.58 s, first inference slower than warm runs, resident RSS about 2.54 GiB. These are short-clip observations, not throughput guarantees.

Language follows the request (the app sends UI language; absent means auto). Decoding is greedy argmax; generic CLI beam/temperature/top-k/top-p switches are rejected by this model. No separate punctuation switch exists. ITN=false removed punctuation/capitalization on these clips, so true stays. Lowering max_tokens or disabling chunking did not establish a useful speed gain and risks longer input; defaults remain. Exact settings, raw stage results, licensing, founder verdict and launch-job cleanup are in private `infercat-pm/docs/spikes/100-asr.md`.

## Pins and licence

[audio.cpp pin](https://github.com/0xShug0/audio.cpp/tree/fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416) is Apache-2.0. The [GGUF card](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-GGUF) assigns these weights **FunASR Model Open Source License v1.1**, while the [-hf export](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512-hf) says Apache-2.0; do not substitute that label for this GGUF. No weights/binaries are redistributed. CPU and Metal were measured; NVIDIA was not. F16 was dropped at the founder's direction; Q8_0 stays.
