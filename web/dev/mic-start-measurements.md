# 101 — real microphone, cold and warm

Measured 2026-09-10 on this Mac's **MacBook Pro Microphone**, with installed Chrome 152 and Playwright Firefox 153 using real capture devices (no oscillator, prerecorded capture input or fake device). Audio was not uploaded or retained. Native permission was auto-approved in the isolated browser profiles. Safari WebDriver refused the session because Allow remote automation is disabled; CUA access by Safari name and verified bundle id both returned `cgWindowNotFound`. Safari real-mic measurements remain **unavailable**, not replaced by synthetic WebKit numbers.

Run Vite at 127.0.0.1:49101, then `node dev/mic-real-check.mjs`. The page `dev/mic-start.html` is also manually usable in Safari. `MIC_PRODUCT=1 node dev/mic-real-check.mjs` measures the revised production VoiceRecorder rather than the initial device experiment. Each mode makes two takes in the same page; the keep-warm mode follows the stopped-stream mode, so its first take has already-granted permission but reacquires a closed stream. Only the first stopped-stream take is a fresh page/device run.

Milliseconds from pointerdown. Individual observations, not percentiles or guarantees. `signal` is the first nonzero analyser read. In a warm stream it can be prefilled audio, so **it is not the timestamp of the first newly captured physical sample**. `recorderStarted` is the synchronous MediaRecorder start call. `encodedChunk` is packet delivery, not capture: the device experiment uses 100 ms chunks, the product 250 ms. No spoken-word accuracy claim is inferred from these timestamps.

| Implementation | Browser | Case | Stream resolves | Recorder starts | First analyser read | Signal observed | First encoded chunk |
|---|---|---|---:|---:|---:|---:|---:|
| Device experiment, before edits | Chrome 152 | Fresh, stop tracks | 389.2 | 390.3 | 390.4 | 600.7 | 505.2 |
| Device experiment | Chrome 152 | Repeat, stopped tracks | 77.2 | 77.3 | 77.3 | 170.9 | 184.4 |
| Device experiment | Chrome 152 | First keep-warm take | 79.9 | 79.9 | 79.9 | 173.8 | 190.4 |
| Device experiment | Chrome 152 | Warm repeat | 0 | 0 | 0 | 0 | 112.1 |
| Device experiment | Firefox 153 | Fresh, stop tracks | 104 | 104 | 104 | 1405 | 209 |
| Device experiment | Firefox 153 | Repeat, stopped tracks | 1 | 1 | 1 | 178 | 108 |
| Device experiment | Firefox 153 | First keep-warm take | 0 | 0 | 0 | 165 | 108 |
| Device experiment | Firefox 153 | Warm repeat | 0 | 0 | 0 | 0 | 96 |
| Revised VoiceRecorder | Chrome 152 | Fresh, stop tracks | 261.4 | 261.8 | 262 | 513 | 557.5 |
| Revised VoiceRecorder | Chrome 152 | Repeat, stopped tracks | 82.5 | 82.6 | 82.7 | 193.2 | 372.2 |
| Revised VoiceRecorder | Chrome 152 | First keep-warm take | 83.8 | 83.9 | 84 | 199.4 | 375.1 |
| Revised VoiceRecorder | Chrome 152 | Warm repeat | reused | 0.1 | 0.2 | 0.2 | 291.8 |
| Revised VoiceRecorder | Firefox 153 | Fresh, stop tracks | 1099 | 1099 | 1100 | 1436 | 1361 |
| Revised VoiceRecorder | Firefox 153 | Repeat, stopped tracks | 2 | 2 | 161 | 220 | 262 |
| Revised VoiceRecorder | Firefox 153 | First keep-warm take | 0 | 1 | 163 | 163 | 259 |
| Revised VoiceRecorder | Firefox 153 | Warm repeat | reused | 0 | 3 | 3 | 260 |

The real-device cold path remains slow and variable; the fix removes repeated device/context acquisition from subsequent gestures. The controller retains the granted stream and analyser while the chat is visible, stops each MediaRecorder at Stop/Cancel, and releases the stream/context on hide, pagehide, unmount or loss of access. Visibility return never starts a take or requests permission. Pointerdown begins acquisition; keyboard/screen-reader click activation remains supported without a duplicate pointer click. The existing waiting line stays while permission/context activation is pending. Capture starts as soon as the stream is available, before awaiting the monitoring context, so monitoring startup cannot delay the recorder. It does not wait for nonzero amplitude: legitimate silence must remain recordable. Cold-device readiness cannot be proven from nonzero amplitude or from a browser's synthetic initial zero frames.

## Historical founder browser and shell

**Unknown from existing logs.** `internal/usage/usage.go` has no user-agent or client-build fields; the 113 host rows inspected contain neither. The host supplies API responses, not the web shell. No new host telemetry was added to this web-only ticket. PM was asked for the founder's browser/device and whether the waiting line was visible; no answer is assumed.

`web/src/sw.ts` uses network-first navigation and versioned assets; a currently open app can continue executing an older bundle. It intentionally does not call `skipWaiting`, so activation can wait for old windows to close. After the gate deploys, reload online; close all Infercat tabs/PWA windows and reopen to activate a waiting worker. Neither “reload twice” nor the API host's version proves which historical shell the founder ran. The gate must verify the deployed bundle; this candidate does not deploy it.
