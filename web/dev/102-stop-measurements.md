# 102 — Stop during microphone startup

Base 243d92e. The deterministic failure is the pending-stream window: Composer disabled the mic for `requesting`, its toggle treated that state as Start, and `VoiceRecorder.stop()` did nothing without a MediaRecorder. The delayed-permission regression fails on the baseline. The fix leaves the pending mic enabled and labelled with existing Stop copy, centralizes toggle semantics, and remembers Stop until stream resolution. A Stop before any audio can exist becomes an idle cancellation as soon as the stream/recorder arrives; Cancel aborts; a take already capturing while AudioContext resumes is stopped normally. Empty blobs never upload. Warm ownership/release behavior from 101 is unchanged.

Six pending/warm action cases, plus three context-resume action cases and empty-blob coverage, pass. Complete voice unit suite 32/0/0; full web 403/0/0; UI matrix 229/0/0 across 390/1280 EN/ZH; make check OK. No new copy, host change or ASR padding is part of 102.

## First-data and delayed-permission addenda

The founder subsequently supplied a cold Safari take: 1.94 s recorder start after a permission prompt, 2.83 s measured versus 2.12 s decoded with 200 ms leading quiet. His warm take started at1 ms, measured2.11 s, decoded2.12 s, no leading quiet. These are founder-reported observations recorded in ticket102, not additional driven runs by the engineer.

The final candidate retains `requesting` with an idle waveform on a cold stream until its first nonempty MediaRecorder chunk **and** AudioContext readiness. The counter begins at that chunk; empty chunks do nothing. A retained stream that has delivered data starts its next display immediately; release clears that readiness. Stop/Cancel remain effective throughout. The mic's `on` class and `aria-pressed` derive only from `RecordingState.kind === 'recording'`, never a tap-local flag. A delayed grant and one Stop is covered in the real Composer matrix, including a screenshot while native capture has started but no chunk has arrived. No site permission setting was changed.

| Final candidate, real mic | Recorder start | First chunk | Recording shown | Stop result |
|---|---:|---:|---:|---|
| Chrome cold |212 ms|488.7 ms|488.9 ms|done|
| Chrome warm |0.1 ms|300.4 ms|0.2 ms|done|
| Chrome reopened |78.6 ms|361.5 ms|361.6 ms|done|
| Firefox cold |94 ms|360 ms|1163 ms|done|
| Firefox warm |0 ms|259 ms|3 ms|done|
| Firefox reopened |1 ms|264 ms|265 ms|done|

Warm added wait is therefore effectively zero (0.1 ms Chrome,3 ms Firefox between recorder start and display). Cold Chrome hides276.9 ms before first data; cold Firefox retains waiting through its later context readiness. A nonempty container chunk is the ruled readiness signal, **not proof of a first physical microphone sample**. Encoders may buffer a timeslice or deliver headers before useful speech. Warm stream retention remains the capture-startup mitigation. Logs: `/tmp/infercat-102-warm-v2.log`; fresh 1.2 s/5 s gateway-transcribed checks: `/tmp/infercat-102-real-v2.log`. The acoustic fixture's strongly attenuated Chrome results are not claimed as dictation quality proof.

The earlier Safari table below predates this first-data addendum. The final Safari rerun could not be driven: CUA returned `cgWindowNotFound` twice, including after opening the loopback page; Safari settings were untouched. The retained page now prints `firstData` and `recordingShown` for the founder's final-candidate check. This proof gap is explicit; neither the previous Safari run nor synthetic WebKit is represented as verifying the added first-data gate.

## Real browser observations before the first-data addendum

Native Chrome 152 and Firefox 153 were driven with their **real MacBook Pro Microphone**, not fake capture devices. A known synthetic spoken WAV was played through the Mac speakers; browser echo cancellation/input processing can attenuate it, so these are control/capture diagnostics, not natural-dictation quality scores. Local blobs were retained privately. Safari 26.5.2 WebDriver still returned the exact disabled-automation error, but the actual Safari window became available through CUA. Its normal localhost microphone prompt was allowed; no other Safari setting changed. CUA ran the real controls and retained seven Safari blobs.

Real Firefox reproduced the ignored second mic tap at the 1-second boundary: baseline tap at1117 ms held `requesting`, native recorder=`recording`, mic disabled; it was still recording 2.2 s later and required a separate Stop at3434 ms. Candidate tap at1132 ms held the same pending/native state, mic enabled, and reached `done`. This is the context-resume arm in a real device run, in addition to the deterministic pending-permission fixture. Logs: `/tmp/infercat-102-firefox-early-before.log` and `-after.log`.

The exact Safari ignored-Stop incident was **not reproduced** at the timed1–2 s/5 s attempts once permission was settled: those controls were already in `recording`, and Stop completed on baseline and candidate. This qualification remains; the Firefox reproduction does not identify every detail of the founder's Safari incident.

| Safari run | Stream ready | Recorder start | First nonzero analyser | Stop tap | Result |
|---|---:|---:|---:|---:|---|
| Candidate cold, Stop button |83 ms|83 ms|1347 ms|1434 ms|done|
| Candidate warm repeat |reused|8 ms|8 ms|1434 ms|done|
| Candidate reopened device,5 s |51 ms|51 ms|1257 ms|5234 ms|done|
| Candidate reopened device, second mic tap |29 ms|29 ms|160 ms|1434 ms|done|
| Baseline cold, second mic tap |238 ms|238 ms|340 ms|1450 ms|done|
| Baseline warm repeat |reused|33 ms|34 ms|1451 ms|done|
| Baseline reopened device,5 s |147 ms|147 ms|198 ms|5233 ms|done|

Milliseconds are from the actual pointer action, not nominal sleeps. Candidate was run before baseline; permission-only takes were discarded. Cold results vary with browser/device lifetime; warm analyser reads may contain a prefilled buffer and are not timestamps of a first new physical sample. Chrome's candidate early/5 s Stop actions occurred at 1346/5012 ms and Firefox's at 1349/5082 ms, all while recording and reaching the transcript result. The retained Safari clips were decoded and sent through the pinned CLI separately; Safari in-page authenticated gateway transcription was not exercised in the native-UI run.

## Saved-blob split: capture versus model

| Safari clip | Recorder elapsed | Decoded WAV | Leading quiet | Same-WAV CLI result |
|---|---:|---:|---:|---|
| Candidate cold early |1.351 s|0.380 s|230 ms|unrelated text from a short fragment|
| Candidate warm early |1.426 s|1.4375 s|440 ms|the red bicycle|
| Candidate reopened5 s |5.183 s|4.2525 s|250 ms|Pinnacle is beside the blue house. This is a test of the.|
| Candidate second mic tap |1.405 s|1.330 s|270 ms|The red bicycle.|
| Baseline cold early |1.212 s|1.2125 s|240 ms|The rent bicycle.|
| Baseline warm early |1.418 s|1.4275 s|420 ms|the red bicycle|
| Baseline reopened5 s |5.086 s|5.085 s|260 ms|The red bicycle is beside the blue house. This is a test of the inferno.|

The candidate cold encoded duration is about 1 s shorter than the recorder clock; warm duration matches within codec padding. Together with the delayed first signal, this is capture/encoding-startup evidence **before inference**. Shorter leading quiet does not mean a better cold capture: it can mean the beginning was absent entirely. Leading quiet is consecutive 10 ms RMS windows below −60dBFS, not speech/word detection. The source fixture itself has 90 ms of initial quiet, and speaker/output/input latency also affects the acoustic recording.

The exact Chrome5 s WAV (4.8 s,20 ms leading quiet) returned only“the”; the acoustic speaker path was strongly attenuated. Firefox5 s (5.0535 s,380 ms quiet) returned“The red bicycle is beside the blue house. This is a test of the infer.” No retained clip established an intact first word that only the model dropped, so no 300 ms silence was prepended. This does not assert that all possible model-start failures are ruled out. Audio playback/download remains available for a human auditory check; analysis claims here are from waveform, duration and same-WAV CLI evidence.

Private evidence: `/tmp/infercat-102-blobs/analysis.json` maps all nine decoded/inferred blobs to their exact files; `safari-cold-first500.png`, `safari-warm-first500.png`, `chrome-first500.png` were inspected. Plots span 0–500 ms on a log amplitude scale; the short cold clip is zero-padded after its actual end for that fixed-width view. Raw audio is not committed. CLI: the same pinned audio.cpp CPU binary/checkpoint as100, `--task asr --family fun_asr_nano --backend cpu --language en --audio <decoded.wav> --text-out <result.txt>`. No live ASR runtime change was made for this check.

## Reproduce and retain the Safari page

From `web/`, run `node dev/mic-stop-server.mjs --prepare` once, then `node dev/mic-stop-server.mjs`. Preparation reads the immutable 243d92e controller and compiles it to `/tmp/infercat-102-baseline.js`; the serving job need not read the Desktop git database. Open `http://127.0.0.1:49102/dev/mic-stop.html`; add `?base` for that controller. One tap, speak, Stop; see timestamps/state, leading quiet, first 500 ms plot, playback and download. “Save local copy” writes a mode 0600 blob under `/tmp/infercat-102-blobs` through a same-origin, bounded loopback endpoint. Without an invite fragment there is no transcription upload; the optional fragment uses the local host and English hint for the spoken fixture. For Mandarin analysis, retain the blob and run the CLI with `--language zh`.

`MIC_BASE=1 node dev/mic-stop-check.mjs` runs baseline Chrome/Firefox; omit the variable for the candidate. It expects the private founder invite file and known WAV under the documented `/tmp` paths; neither secret nor raw audio is printed/committed. The real-Safari run used the visible native controls, not synthetic WebKit or WebDriver.

The requested page is kept available by temporary user job `ai.infercat.l2.harness.102`, with **no installed plist**, command `/opt/homebrew/bin/node /tmp/infercat-wt-102/web/dev/mic-stop-server.mjs`. Remove it with `launchctl remove ai.infercat.l2.harness.102` when founder verification ends. This is separate from 100's five founder demo jobs. Safari was left on the candidate page, idle with its microphone released. No product site deployment is performed by this ticket.
