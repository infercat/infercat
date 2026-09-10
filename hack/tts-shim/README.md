# Local Qwen speech proof adapter (117)

The pinned audio.cpp Qwen route returns WAV even when a client requests MP3. Infercat's current voice player expects MP3. This Python standard-library adapter calls the resident native route, then encodes once with FFmpeg; it adds no ML package, framework, voice cloning or product route.

Runtime: [audio.cpp fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416](https://github.com/0xShug0/audio.cpp/tree/fa5aaac9266a98c68f8a5c9fcd1ba6ff65875416), Apache-2.0. Build with Metal and only `qwen3_tts`:

```sh
cmake -S audio.cpp -B build/tts-metal -DCMAKE_BUILD_TYPE=Release \
  -DENGINE_ENABLE_METAL=ON -DENGINE_ENABLE_OPENMP=OFF \
  -DAUDIOCPP_DEPLOYMENT_BUILD=ON -DAUDIOCPP_MODEL_SET=custom -DAUDIOCPP_MODELS=qwen3_tts
cmake --build build/tts-metal --target audiocpp_server -j 4
```

Checkpoint: [publisher's Qwen3-TTS 1.7B CustomVoice Q8_0](https://huggingface.co/audio-cpp/audio.cpp-gguf/tree/056144d2744697c9439bd32647279674dba0c964/Qwen3-TTS-12Hz-1.7B-CustomVoice-GGUF), Apache-2.0 for this model. File `qwen3-tts-12hz-1.7b-customvoice-q8_0.gguf`, 2,817,044,064 bytes, SHA256 `3cfaac8e9f13554f6daea3c5e0c53fede71ef5500cbaae7445e5fc3a5bb12e72`. No weights or binaries are redistributed.

Save the following as `runtime.json`, adjusting only the absolute model path:

```json
{
  "host": "127.0.0.1", "port": 18087, "backend": "metal", "threads": 4,
  "lazy_load": false, "idle_unload_ms": 0, "ui": false, "ui_management": false,
  "log_request_body": false, "max_request_body_bytes": 16384, "busy_timeout_ms": 1,
  "models": [{
    "id": "Qwen3-TTS-12Hz-1.7B-CustomVoice-Q8_0", "family": "qwen3_tts",
    "path": "/absolute/path/qwen3-tts-12hz-1.7b-customvoice-q8_0.gguf",
    "task": "tts", "mode": "offline", "default_voice_preset": {"voice_id": "Vivian"},
    "default_request_options": {"max_tokens": "2048"}
  }]
}
```

With Python 3.9+ and FFmpeg on PATH:

```sh
python3 hack/tts-shim/server.py --config runtime.json \
  --runtime /absolute/path/build/tts-metal/bin/audiocpp_server --port 18079
python3 hack/tts-shim/check.py http://127.0.0.1:18079
```

The adapter owns the runtime child and terminates it on exit; omit `--runtime` only when that configured server already runs. Both bind loopback. `/v1/models` checks native health. `/v1/audio/speech` accepts nonempty `input`, optional native built-in `voice`, and `response_format` of `mp3` (default) or `wav`; the single configured model is used. No language field means native Auto. Only these fields are forwarded, so reference-audio/cloning inputs are not exposed.

Limits: 16 KiB JSON, no chunked request body, 10-second body timeout, 300-second native request timeout, 32 MiB native audio cap, 30-second encode timeout; one synthesis at a time, overlapping starts get429. The native configuration serializes model use and caps generated tokens. This is offline synthesis: first audio waits for the whole WAV plus encoding. Cancellation disconnects the client; a dispatched native inference may finish before the adapter releases its slot. No audio/text is logged or persisted by the adapter.

Point the host's `--upstream-speech` at the adapter and `--upstream-speech-model` at `Qwen3-TTS-12Hz-1.7B-CustomVoice-Q8_0`; update a host model allowlist if set. Preserve the host data directory/invite. A model change requires the next serve. The app sends no model or voice field.

Measured M5 Max /64 GiB: native 100-character Mandarin/English RTF 0.25–0.27, peak process RSS 6.28 GiB. With MP3 adapter, first audio6.235/1.919 s for24.048/7.176 s outputs. Real local gateway→Chrome Mandarin playback started5.865 s after the gesture and ended cleanly. These are observations on one Mac, not streaming or general performance claims; native and adapter trials use different sampling seeds. Full passages, raw timings, founder verdict and six-job cleanup are in private `infercat-pm/docs/spikes/117-tts.md`. The 1.7B met real time, so 0.6B was not tested.
