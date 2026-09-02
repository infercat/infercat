# MEASURE — every published number comes from a command here

## Test upstream (laptop)

```
~/Desktop/repos/2185Lab/bunny-kit/binaries/llama-server/b9553/llama-server \
  -m ~/.cache/bunny-network/models/gemma-4-E2B-it-Q4_K_M.gguf \
  --host 127.0.0.1 --port 18080 -np 2 -c 8192 --no-mmproj --metrics
```

Note: llama.cpp divides `-c` across `-np` slots; with `-c 8192 -np 2` the engine reports `n_ctx` 4096 **per slot**, and that is the effective context the gateway must enforce.

## Numbers (fill from ticket reports; date + command + result)

| Date | What | Command | Result |
|---|---|---|---|
| 2026-09-02 | Browser → public relay (nyc) → host on same laptop, headless Chromium | `node web/wasm/demo-check.mjs` against `go run ./hack/tunneldemo` | connect 183 ms; ping 77.6 / 31.2 / 32.9 ms via DERP(nyc), direct=false; `GET /healthz` 64 ms first dial, 33 ms repeat dial (no re-handshake); `/stream` 20 SSE events at 100 ms cadence intact |
| 2026-09-02 | CLI cross-check | `tailcat ping <addr>` | 27.9–29 ms via DERP(nyc) |
| 2026-09-02 | wasm download | `ls -l web/public/bunny.wasm.gz` | 6,182,826 bytes gz (26,912,436 raw) |
