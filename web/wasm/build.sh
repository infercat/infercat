#!/bin/sh
# Builds the browser bridge: web/public/infercat.wasm, infercat.wasm.gz, and the Go toolchain's
# wasm_exec.js. Build tags are pinned in build-tags.txt (tailcat v0.4.0's WasmTags()).
set -eu
cd "$(dirname "$0")/../.."
export GOTOOLCHAIN=auto
mkdir -p web/public
GOOS=js GOARCH=wasm go build -tags "$(cat web/wasm/build-tags.txt)" -ldflags="-s -w" -o web/public/infercat.wasm ./web/wasm
gzip -9 -n -c web/public/infercat.wasm > web/public/infercat.wasm.gz
cp -f "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/public/wasm_exec.js && chmod 644 web/public/wasm_exec.js
wc -c web/public/infercat.wasm web/public/infercat.wasm.gz
