# 096 — iOS simulator findings

2026-09-09; Xcode 26.6 (17F113), iOS 26.5 (23F77), iPhone 14
(390×844 points; native captures 1170×2532, committed exports 390×844). Native WebDriverAgent 16.12.6,
commit `23b864e7cc2cd1e94bbbc83b3a32e501f1f2b484`. Both EN and Simplified
Chinese use actual simulator system languages. This is iOS Simulator evidence,
not a physical-phone claim and not Chrome device emulation.

Reproduce with [the opt-in harness](../../web/dev/ios-proof/README.md).
The application tested is the live `https://infercat.ai`; the tunnel terminates
at a fresh **096 isolated host** on this Mac. No launch-host changes or production
deployments were made. The original launch recordings and machine-readable
result are in the run's output directory.

| Observation | Actual iOS behavior / implication |
| --- | --- |
| Installation | Safari's native “…” → Share → View More → Add to Home Screen works. Confirmation offers **Open as Web App** and the clean `https://infercat.ai/` URL, without an invite fragment. |
| Clipboard | The app's Copy invite puts the exact owned invite on the simulator clipboard. The fresh installed app starts empty. Paste causes a native consent dialog; Allow Paste inserts that exact invite, and Connect reaches the owned host. iOS calls the source and recipient “Safari” (localized in Chinese), rather than naming the installed app. |
| Status bar | Black status text on white paper; the web view starts at y=47 points on this device. There is no Safari toolbar in the installed window. This agrees with the design's `default` status-bar choice. |
| Safe areas | The composer ends at y=798 points, above the home indicator. Neither the top bar nor the bottom controls overlap native chrome in either language. The 390-class mock's 59-point island status area is not this iPhone 14's geometry. |
| Default icon | iOS applies its rounded-square mask. Ears and tail remain inside the tile, with the intended white padding. |
| Tinted icon | Native Tinted recolors the black cat blue while retaining a white tile in the tested Light tint mode. The design predicted a tinted plate/dark silhouette; the mark survives, but the plate is not recolored like Apple's own icons. No custom tinted asset was substituted. |
| First launch | The actual recording shows a short black-to-blank-light transition before the page. No centered icon/name splash is generated here. The committed launch PNG is an actual frame of that transition, not the Android splash mock. |
| Network-off | **Not proved.** Settings has no Wi-Fi/Airplane Mode section; Control Center has no connectivity controls, and its picker returns no result for Wi-Fi. `simctl` exposes no network-off command. Its status-bar overrides only alter the drawing. The shared Mac's network was left alone. The PM accepted both explicit skips on 2026-09-09 as a founder-phone follow-up; host-wide conditioning is excluded too. |

Follow-up candidates, with no product fix in this ticket:

- Update the iOS install instructions for Safari 26's “…” → Share → View More path;
  the current “Tap Share below the page” assumes the older toolbar. Keep support
  for older Safari layouts in the copy.
- Correct the design's generated iOS splash assumption. If a branded launch is
  required, evaluate supported Apple startup assets in a separate ticket and
  verify on a phone before adding them.
- Finish device-network-off and recovery on a physical phone, or approve a
  genuinely isolated network-conditioning mechanism. Do not count host shutdown,
  a fake radio icon, or a JavaScript online flag as this proof.

Manual extension: the EN installed app sent a real haiku request through the
production wasm bridge to the owned llama.cpp engine. With thinking disabled in
the app's real Settings selector, it returned a 20-token answer; the transcript
stayed visible in the standalone window. This is additional chat evidence, not
an offline result.

## Evidence to review

| State | English | 中文 |
| --- | --- | --- |
| Copy invite | [EN](../../web/dev/screenshots/096-en-copy-invite.png) | [ZH](../../web/dev/screenshots/096-zh-copy-invite.png) |
| Native install confirmation | [EN](../../web/dev/screenshots/096-en-native-install.png) | [ZH](../../web/dev/screenshots/096-zh-native-install.png) |
| Default Home Screen icon | [EN](../../web/dev/screenshots/096-en-icon-default.png) | [ZH](../../web/dev/screenshots/096-zh-icon-default.png) |
| Tinted Home Screen icon | [EN](../../web/dev/screenshots/096-en-icon-tinted.png) | [ZH](../../web/dev/screenshots/096-zh-icon-tinted.png) |
| Actual blank launch surface | [EN](../../web/dev/screenshots/096-en-launch.png) | [ZH](../../web/dev/screenshots/096-zh-launch.png) |
| Native paste permission | [EN](../../web/dev/screenshots/096-en-paste-permission.png) | [ZH](../../web/dev/screenshots/096-zh-paste-permission.png) |
| Standalone owned-host chat | [EN](../../web/dev/screenshots/096-en-standalone.png) | [ZH](../../web/dev/screenshots/096-zh-standalone.png) |

[Additional real model reply](../../web/dev/screenshots/096-en-chat.png).

## Verification

`INFERCAT_PWA_ENGINE=http://127.0.0.1:49186 make ios-proof` completed with
**10 passed / 0 failed / 2 skipped**. The two skips are the explicitly unproved
EN/ZH device-network-off cases. [The result](../../web/dev/ios-proof/result.json)
records the exact harness SHA-256 and the native launch-frame indices. The run's
14 PNGs and the extra manual EN chat PNG are exported at 390×844 with a 256-color
palette. Originals and MOVs stay outside git. The documented export printed
**15 passed / 0 failed / 0 skipped**; largest 125,431 bytes, total 877,244 bytes.

`make check`: **CHECK OK**. Its JavaScript suites printed 104 passed / 0 failed /
1 skipped (the existing opt-in client integration fixture); 10 Go packages
passed. Host compatibility printed 11/0/0, launch asset fixtures 7/0/0,
`launch-check: OK`, and installer fixtures 18/0/0. The opt-in iOS target is absent
from `make -n check`. No product source, dependency, or generated app asset changed.
