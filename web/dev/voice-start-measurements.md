# 099 startup measurement

Base: main `141a367`. Run `VOICE_CHECK_URL=http://127.0.0.1:49199 node dev/voice-start-check.mjs` against Vite. For the before run, serve the base's `voice.ts` as a temporary `src/voice-baseline.ts` and set `VOICE_START_MODULE=/src/voice-baseline.ts`; remove the temporary module afterwards. No host or transcription request is involved.

Milliseconds from the button's click handler, first take / repeat take in the same page. These are individual observations, not percentile guarantees. The harness asserts capture starts before recording UI and every track ends on cancellation. Chromium uses a generated continuous 440 Hz WAV from sample zero through its native fake capture device; Firefox uses its native fake device. WebKit's public Playwright permission API does not grant microphone access (the native attempt returned `blocked`), so its source is an injected oscillator MediaStream; MediaRecorder, AudioContext and analyser remain native. No physical microphone timing or iOS Safari claim; no simulator was booted.

| Engine / version | Revision | Stream resolves | Recorder starts | Context running | First analyser read | First nonzero sample | First frame callback | Nonzero drawn callback | First dataavailable |
|---|---|---|---|---|---|---|---|---|---|
| Chromium 151.0.7922.34 | Before | 111.7 / 22.6 | 112.6 / 22.8 | 113 / 23 | 163.1 / 73.4 | 163.1 / 73.4 | 112.9 / 22.9 | 186.2 / 73.5 | 412.8 / 314 |
| Chromium 151.0.7922.34 | After | 137.6 / 21.7 | 137.8 / 21.8 | 110.4 / 6.9 | 138.1 / 21.9 | 190.5 / 77 | 139.1 / 21.9 | 190.6 / 77.1 | 436.7 / 318.3 |
| Firefox 153.0 | Before | 56 / 1 | 57 / 1 | 1049 / 59 | 109 / 59 | 1070 / 112 | 63 / 14 | 1071 / 112 | 322 / 256 |
| Firefox 153.0 | After | 65 / 1 | 65 / 1 | 89 / 28 | 65 / 1 | 138 / 73 | 77 / 19 | 138 / 73 | 323 / 263 |
| WebKit 26.5, oscillator source | Before | 18 / 3 | 22 / 5 | 22 / 5 | 74 / 56 | 74 / 106 | 22 / 15 | 75 / 106 | 273 / 256 |
| WebKit 26.5, oscillator source | After | 23 / 5 | 24 / 5 | 24 / 5 | 24 / 5 | 126 / 65 | 24 / 13 | 126 / 65 | 276 / 260 |

The Firefox first-take delay was in the monitoring context: recording started at 57 ms and delivered a chunk at 322 ms, but the analyser did not receive signal until 1070 ms. Activating the context within the click removed that roughly one-second delay. Capture already preceded recording UI; that ordering is now tested. The first analyser read now happens immediately after starting capture, with another read on the first animation frame, then 20 Hz updates. A pending permission prompt shows “Waiting for the microphone…” / “正在等待麦克风…” with Cancel, no timer or waveform. Stop releases tracks immediately, before the asynchronous encoded clip callback.

`dataavailable` measures encoded chunk delivery (250 ms timeslice), not first capture. First nonzero analyser sample is an observable upper bound on signal availability, not a timestamp of the microphone's first physical sample. All three repeat-tap observed signal/draw times are below 150 ms; this is not a hardware/browser latency guarantee. Frame timestamps are requestAnimationFrame callbacks after the harness SVG update, not compositor presentation timestamps. Chromium's default fake source has initial silence, so earlier exploratory runs without the continuous WAV showed ~500 ms of silence despite ~22 ms recorder startup; those are not application input lag. The native WebKit permission test was blocked; oscillator timing does not measure its permission/device acquisition.

Screenshots from the real app state matrix: `/tmp/infercat-099-shots/waiting-390-en.png`, `waiting-390-zh.png`, and `recording-390-en.png`. The matrix checks pending permission, late permission after Cancel, and the existing recording/transcription/playback/error/offline states at 390 and 1280 in both languages.
