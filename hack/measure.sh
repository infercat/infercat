#!/bin/sh
# measure.sh — TTFT and tokens/s for one prompt, N times, via Direct (loopback dev-listen), via
# Tunnel (the public relay, reached through a `tailcat socks` proxy), and — when CONNECT is set —
# via a running `bunny-network connect` (ticket 026). Reproduces the numbers in docs/MEASURE.md.
# It sends requests only; it never starts, stops, or reconfigures the engine or the host (ticket
# 005 promise 8).
#
# Prereqs: a running `bunny-network serve --dev-listen $DIRECT --upstream <engine>`, a key SECRET,
# and the tunnel ADDR (from `serve` or `status`). tailcat, curl, python3 on PATH. For the connect
# mode, a running `bunny-network connect <invite> --listen $CONNECT`.
#
#   DIRECT=127.0.0.1:9090 ADDR=tc… SECRET=… MODEL=gemma-4-E2B-it-Q4_K_M.gguf \
#     N=3 PROMPT="Why is the sky blue? Answer in three sentences." sh hack/measure.sh
#   CONNECT=127.0.0.1:11435 MODES="direct connect" … sh hack/measure.sh   # MODES picks the paths
#
# TTFT = wall time to the first SSE `data:` line. tokens/s = completion_tokens (from the final
# usage chunk) / (total - TTFT). Prints each run and the median of each metric.
set -eu
: "${DIRECT:=127.0.0.1:9090}"
: "${N:=3}"
: "${MODEL:=gemma-4-E2B-it-Q4_K_M.gguf}"
: "${PROMPT:=Why is the sky blue? Answer in three sentences.}"
: "${ADDR:?set ADDR to the tunnel address (tc…)}"
: "${SECRET:?set SECRET to a key secret}"
: "${MODES:=direct tunnel${CONNECT:+ connect}}"

body() {
  printf '{"model":"%s","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"%s"}]}' "$MODEL" "$PROMPT"
}

# run <url> [socks-addr]: streams the response and prints "ttft_ms completion_tokens tok/s total_ms".
run() {
  url=$1; socks=${2:-}
  set --
  [ -n "$socks" ] && set -- --socks5-hostname "$socks"
  curl -sN "$@" -H "Authorization: Bearer $SECRET" -H 'Content-Type: application/json' \
       -H 'Connection: close' --data-binary "$(body)" "$url" \
  | python3 -u -c '
import sys, time, json
start = time.time(); ttft = None; comp = 0
for raw in sys.stdin:
    line = raw.rstrip("\n")
    if not line.startswith("data:"): continue
    p = line[5:].strip()
    if p == "[DONE]": continue
    if ttft is None: ttft = time.time() - start
    try: o = json.loads(p)
    except Exception: continue
    u = o.get("usage")
    if u and u.get("completion_tokens"): comp = u["completion_tokens"]
total = time.time() - start
gen = max(total - (ttft or total), 1e-6)
print(f"{int((ttft or 0)*1000)} {comp} {comp/gen if comp else 0:.1f} {int(total*1000)}")
'
}

median() { sort -n | awk '{a[NR]=$1} END{print (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}'; }

echo "# measure.sh $(date -u +%Y-%m-%dT%H:%M:%SZ)  N=$N  model=$MODEL"
echo "# prompt: $PROMPT"

# One tailcat SOCKS proxy for every tunnel run; the address blob is the destination hostname.
case " $MODES " in *" tunnel "*)
PROXY_LOG=$(mktemp)
tailcat socks --listen=127.0.0.1:0 "$ADDR" >"$PROXY_LOG" 2>&1 &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null || true; rm -f "$PROXY_LOG"' EXIT
for i in $(seq 1 50); do
  PROXY=$(sed -n 's/.*\(127\.0\.0\.1:[0-9][0-9]*\).*/\1/p' "$PROXY_LOG" | head -1)
  [ -n "$PROXY" ] && break; sleep 0.2
done
[ -n "${PROXY:-}" ] || { echo "tailcat socks printed no address"; cat "$PROXY_LOG"; exit 1; }
echo "# tunnel: tailcat socks proxy $PROXY → http://$ADDR:80"
;; esac
[ -n "${CONNECT:-}" ] && echo "# connect: bunny-network connect at http://$CONNECT (the app's key is ignored; the invite's is used)"

for mode in $MODES; do
  echo "## $mode"
  ttfts=""; tokpss=""
  for r in $(seq 1 "$N"); do
    case $mode in
      direct)  out=$(run "http://$DIRECT/v1/chat/completions") ;;
      tunnel)  out=$(run "http://$ADDR:80/v1/chat/completions" "$PROXY") ;;
      connect) out=$(run "http://$CONNECT/v1/chat/completions") ;;
    esac
    set -- $out
    echo "  run $r: ttft ${1} ms, ${2} tokens, ${3} tok/s, total ${4} ms"
    ttfts="$ttfts$1
"; tokpss="$tokpss$3
"
  done
  echo "  median TTFT ${mode}: $(printf '%s' "$ttfts" | grep . | median) ms   median tok/s: $(printf '%s' "$tokpss" | grep . | median)"
done
