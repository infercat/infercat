# Native helper bundle

This archive contains `bin/infercat-speech` and `bin/audiocpp_server`. It contains no
models, sherpa libraries, or espeak-ng. `manifest.json` records the source and dependency
pins, dynamic dependencies, binary hashes, and the `voices.txt` hash. The adjacent `.sha256` verifies the archive.

Fetch sherpa's matching **v1.13.7 shared** archive separately, verify the size and SHA-256
in the manifest against `packaging/helpers/pins.json`, and place its `lib` directory at
`sherpa/lib` beside this archive's `bin` directory. Do not use a system library search path.
The upstream sherpa artifact includes the GPLv3 espeak-ng dependency. Keeping that artifact
separate describes the packaging; it does not change its licence or obligations. Our bridge
uses sherpa's Apache-2.0 C API. Audio.cpp and its vendored notices are under `licenses`.

With the verified Kokoro v1.1-zh model bundle already present:

```
bin/infercat-speech --model-dir /absolute/path/kokoro-multi-lang-v1_1 --listen 127.0.0.1:8082
```

Only literal loopback addresses are accepted. `/health` answers `ready`; `/v1/models`
lists `kokoro`. `/v1/audio/speech` accepts `input`, optional `model: "kokoro"`, `voice`,
`speed` (0.5–2), and `response_format` (`wav`, the default, or `pcm`). Other formats are
refused. The full 103-voice set and native ID order are pinned in
`voices.txt` (shipped in the helper archive), from the bundle's
`speaker_names` metadata. Absent voice selects `zf_001` for Han text, otherwise
`af_maple`. Behind the gateway, configure `--upstream-speech-voices` with names from
this set; mappings from another Kokoro bundle (such as `af_heart`) must be changed.

Admission plans for at most 90 seconds, with a 120-second output safety cap.
ASCII characters cost 1 unit, other scripts 4, digits 8; the larger weight of the
original and normalized text is used. The unit limit is
`600 × min(speed, 1 + 0.3 × (speed − 1))`, for speed 0.5–2: 300 / 600 / 690 / 780
units at 0.5 / 1 / 1.5 / 2. Thus speed 1 allows up to 600 ASCII or 150 Han characters;
mixed text shares that budget. JSON is capped at 32 KiB and reflected unknown field
names at 64 characters including the ellipsis. Excess input gets 400 before native
synthesis. These are conservative measured estimates, not a duration guarantee for
every string and voice; the output cap remains a safety net.

The v2 sentence-only calibration missed spelled acronyms. The review measured
900 acronym characters at speed 1 yielding 103.13 s, and 1800 at speed 2 yielding
119.98 s; its 1800-character spelled-only case aborted. It also measured 900 spaced
letters at speed 1 yielding 83.31 s. All four lengths/speeds now refuse before synthesis.
The review's ordinary-acronym list was abbreviated; our recorded full list shares
its published prefix, while the spelled-only list is verbatim.

V3 measurements on the development Mac, pinned model and native CPU/2-thread engine:

| Input class | Characters at speeds 0.5 / 1 / 1.5 / 2 | Audio seconds at those speeds |
| --- | --- | --- |
| Acronyms | 300 / 600 / 690 / 780 | 75.54 / 78.80 / 54.90 / 52.78 |
| Spelled-only acronyms | 300 / 600 / 690 / 780 | 73.34 / 78.95 / 55.46 / 52.72 |
| Spaced letters | 300 / 600 / 690 / 780 | 65.20 / 65.08 / 39.97 / 39.62 |
| Han | 75 / 150 / 172 / 195 | 24.91 / 24.45 / 17.04 / 15.95 |
| ASCII digits | 37 / 75 / 86 / 97 | 16.60 / 9.36 / 5.52 / 5.00 |

The worst speed-1 rate was 0.132 s/unit; planning 0.15 s/unit gives 90 / 0.15 = 600.
At speed 0.5 the worst was 0.252 s/unit, below the planned 0.30. Faster synthesis is
nonlinear: the review's 2× speed compressed its corpus by only about 1.52×. The
admission curve allows just 1.15×/1.30× more units at 1.5/2, instead of 1.5×/2×.
All 24 boundary probes (including four repeated-W cases that collapsed to short audio)
finished below 90 s; maximum 78.96 s. Repeated W is not used to estimate a rate.
First audio for **600 spelled acronym characters at speed 1** was 8.725 s, complete
28.307 s; **300 at speed 0.5** was 16.486 s, complete 26.630 s. The historical
**100-character EN** sample's median was 0.847 s first audio / 2.147 s complete;
that short sample is not a latency promise for long input.

Go normalization runs only if the input contains Han: years are read digit by digit,
dates use year/month/day, integers below 10,000 use cardinal readings, decimals spell
the fractional digits, and 11-digit mainland mobile numbers use telephone digits.
Long identifiers and leading zeroes are preserved as digit readings. No sherpa FST
is loaded: English `123` and `13800138000` pass unchanged to the English phonemizer.
The 42-character numeric Chinese sample took 1.129 s to first audio / 3.643 s complete
and produced 9.038 s. English `123` produced 1.354 s versus 1.240 s for the Han reading;
English phone digits produced 3.400 s versus 2.126 s for the Han telephone reading.

PCM is for a future consumer that knows its wire format; the shipped player plays WAV.
Audio is mono signed PCM16 little-endian at 24 kHz. WAV streams have unknown-length
RIFF/data sentinels (`0xffffffff`); HTTP end-of-stream closes the payload. The helper
flushes each sherpa sentence/batch callback. A single native batch cannot be interrupted
before its callback. After a disconnect the slot is released at the next native
callback, up to one batch later. It runs one synthesis at a time; concurrent calls
receive 429 with `Retry-After` equal to the longest completed batch observed in the
current synthesis (or the current batch's elapsed duration if longer), rounded up
to seconds and capped at 30. Before the first callback it uses a 3-second hint.
This observed duration is not a prediction of remaining runtime. The gateway returns
503 `upstream_down` with that hint and releases the busy refusal's RPM entry. The
player retries up to twice: after 3 seconds, then the returned hint. Both waits are
abortable; hints above 120 seconds are left to the person. A long unfinished batch
can still outlast both retries. Every dispatched speech request records measured
characters; served responses and client cuts after the first audio byte charge them.
Upstream failures, busy refusals and client cuts before audio charge zero.
Transcription accounting is unchanged, including its existing charge on a cut.
Cancellation, failed generation, and the 120-second output/request bounds stop callbacks;
a failure after audio starts aborts the response instead of finishing a truncated recording.
The first byte of audio is not necessarily the first browser playback: WAV uses the
player's Blob path. The model stays loaded until shutdown.

To build a snapshot (Python 3.12+, CMake, C/C++ compiler, and the project's Go toolchain):

```
python3 packaging/helpers/build.py --audio-source /path/to/clean/pinned/audio.cpp --work /new/build/directory --version snapshot
```

The audio source must match the exact commit in `pins.json`. The build verifies upstream
sherpa before compiling, builds only Fun-ASR-Nano in audio.cpp (Metal on Darwin, portable
CPU on Linux), checks dynamic dependencies, and executes both binaries after relocation.
It never publishes. Darwin arm64 and Linux amd64 are the supported build targets; the
Linux build is not a NVIDIA loadout measurement.
