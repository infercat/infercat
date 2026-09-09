# Real iOS simulator proof (096)

Opt-in; `make check` does not run or install this tooling. The target visits the live
`https://infercat.ai` application and uses a new, isolated local Infercat host/key/data directory.
It never uses the launch host. Supply an **owned** model engine on loopback:

```sh
INFERCAT_PWA_ENGINE=http://127.0.0.1:49186 make ios-proof
```

Prerequisites: Xcode **26.6 / 17F113**, installed iOS **26.5 / 23F77** runtime,
Python 3, Git, and ffmpeg (export verified with **9.0.1**). Check `xcrun simctl list runtimes` first. The script does
not download runtimes, alter the selected Xcode, or change the Mac's network.
For this run the engine was llama.cpp **b9553** with
`gemma-4-E2B-it-Q4_K_M.gguf`, `-np 1 -c 8192 --no-mmproj -ngl 0`.
See [the project's measurement setup](../../../docs/MEASURE.md) for model/engine setup.

The script clones official Appium **WebDriverAgent 16.12.6**, verifies commit
`23b864e7cc2cd1e94bbbc83b3a32e501f1f2b484`, builds its XCUITest runner,
and binds the server to loopback. Each language gets a fresh iPhone 14 simulator
(390×844 points, screenshots 1170×2532), with real EN/ZH system preferences.
Tooling is temporary and test-only; nothing is added to the app's dependencies.
The script revokes its key, stops its host/runner, and deletes only its own
simulators on success or failure. It leaves the caller's model engine running.

Output defaults to `/private/tmp/infercat-096-proof`; for a different directory:

```sh
INFERCAT_PWA_ENGINE=http://127.0.0.1:49186 python3 web/dev/ios-proof/run.py --output /private/tmp/096-review
```

`result.json` records the tool pins, source base, live HTML digest, and explicit
pass/fail/skip accounting. Original PNGs are native `simctl` captures. The launch PNG is a
detected blank-transition frame of the original MOV, retained beside it for inspection;
this is a real transition frame, not an authored splash screen. Native appearance,
clipboard consent and install actions are driven by WDA, not browser emulation.
No invite is printed or captured in a screenshot. Clipboard equality is checked
in memory both after Safari's Copy invite and before the installed app connects.

**Accepted phone follow-up:** this runtime has no per-device Wi-Fi/Airplane Mode
control. The PM accepted both network-off skips on 2026-09-09; the founder's phone
will finish device-offline verification. Host-wide Network Link Conditioner is
also excluded. No Mac network setting, fake online flag, or stopped-host
substitute is used.

## Screenshot export

`make ios-proof` runs `export.py` after the native proof. Originals and MOVs stay
in the external output directory; only review PNGs go to `web/dev/screenshots`.
To repeat the export independently (including a manually captured 096 PNG):

```sh
python3 web/dev/ios-proof/export.py --source /private/tmp/infercat-096-proof
```

The FFmpeg 9.0.1 recipe is Lanczos scaling to **390×844**, a separate **256-color
palette** per image, and ordered Bayer dithering (`bayer_scale=3`). The helper
requires original 1170×2532 PNGs and verifies indexed PNG output under **300,000
bytes each**. It does not crop, retouch, or alter the depicted state. All fifteen
committed exports passed: largest **125,431 bytes**, total **877,244 bytes**.
The native run's recorded harness hash still identifies the unchanged `run.py`;
the export is a separate, verified post-processing step.
