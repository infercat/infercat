English · [简体中文](README.zh-CN.md)

# Repository tools

Run these from the repository root. Host and browser usage belongs in the [root README](../README.md); build commands are in [Contributing](../CONTRIBUTING.md#building).

- [install.sh](install.sh): the `curl | sh` installer; selects the platform archive, verifies its exact SHA-256 entry before extraction, installs without sudo, and reports the executable path.
- [install_test.sh](install_test.sh) + [install-fixture/](install-fixture/): exercise the installer against local fake releases, including corrupt archives/checksums; run by `make check`.
- [notices.sh](notices.sh): `make notices` regenerates third-party licence texts/inventory; `make notices-check` verifies them.
- [measure.sh](measure.sh): measure first-token latency and tokens/s through direct, relay or native-connect paths against an existing host; prints runs and medians for measurement records.
- [subset-font.py](subset-font.py): rebuild the CJK WOFF2 subset from both language tables; `python3 hack/subset-font.py --check` checks coverage. See [font setup](../hosting/README.md#rebuilding-the-chinese-font-subset).
- [load/](load/): simulate friends with individual keys/tunnels and sample request/host/engine/relay metrics; invoke `go run ./hack/load -h` for the explicit host and output arguments.
- [tunneldemo/](tunneldemo/): start a small HTTP service on a real tunnel for wasm development; `go run ./hack/tunneldemo -h` lists its options. The [browser tunnel check](../web/README.md#commands) uses it.

Measurement/load tools operate against services you supply; use your own test hosts and keys. They do not start or reconfigure the engine for you.
