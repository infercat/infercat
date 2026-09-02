// Leak and input-guard check for the wasm bridge (ticket 005 fixes 10l and 10m). Drives
// demo.html in headless Chromium against hack/tunneldemo, dials N times through the tunnel,
// and reports BunnyTunnel.stats() (live js.Func handles, Go heap after GC) before and after;
// then feeds write() every wrong type and checks the session survives.
//
//   go run ./hack/tunneldemo -ephemeral -demo-listen 127.0.0.1:59080   # prints the address
//   N=200 node web/wasm/leak-check.mjs "http://127.0.0.1:59080/wasm/demo.html?addr=tc…"
//
// Exit 1 if live handles grow, the heap grows past HEAP_MB (default 4), or a bad write kills
// the program. PLAYWRIGHT_PATH works as in demo-check.mjs.
const { chromium } = await import(process.env.PLAYWRIGHT_PATH ?? "playwright");
const url = process.argv[2];
const N = Number(process.env.N ?? 200);
const HEAP_MB = Number(process.env.HEAP_MB ?? 4);
if (!url) { console.error("usage: node leak-check.mjs <demo.html URL with ?addr=…>"); process.exit(2); }
const browser = await chromium.launch();
const page = await browser.newPage();
page.on("pageerror", (e) => console.log("[pageerror]", e.message));
await page.goto(url);
const r = await page.evaluate(async (n) => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("/public/bunny.wasm"), go.importObject);
  go.run(instance);
  const enc = new TextEncoder();
  const addr = new URLSearchParams(location.search).get("addr");
  const session = await BunnyTunnel.connect({ addr });
  const healthz = async () => {
    const conn = await session.dial(80);
    await conn.write(enc.encode("GET /healthz HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n"));
    await conn.closeWrite();
    let text = "";
    for (;;) { const c = await conn.read(); if (c === null) break; text += new TextDecoder().decode(c); }
    conn.close();
    if (!text.includes('{"ok":true}')) throw new Error("healthz body: " + text.slice(-80));
  };
  await healthz();
  const before = BunnyTunnel.stats();
  const samples = [];
  for (let i = 1; i <= n; i++) {
    await healthz();
    if (i % 50 === 0) samples.push({ i, ...BunnyTunnel.stats() });
  }
  const after = BunnyTunnel.stats();
  // 10m: every wrong type is a rejection with a message, never a crash.
  const conn = await session.dial(80);
  const bad = [];
  for (const [name, v] of [["ArrayBuffer", new ArrayBuffer(8)], ["DataView", new DataView(new ArrayBuffer(8))], ["object", {}], ["array", [1, 2, 3]], ["string", "GET /"], ["undefined", undefined]]) {
    try { await conn.write(v); bad.push(`${name}: accepted`); } catch (e) { bad.push(`${name}: rejected (${e.message})`); }
  }
  conn.close();
  await healthz(); // the program is still alive and the session still works
  const alive = typeof BunnyTunnel.connect === "function";
  session.close();
  const closed = BunnyTunnel.stats();
  return { before, samples, after, bad, alive, closed };
}, N);
await browser.close();
console.log(JSON.stringify(r, null, 2));
// A handful of promise handlers are in flight at any snapshot, so the property is BOUNDED growth,
// not an exact match: N dials must not each retain their funcs (that would be +4N). A small slack
// covers the in-flight handlers; the heap bound is the real backstop.
const SLACK = 8;
const funcsBounded = r.after.liveFuncs <= r.before.liveFuncs + SLACK;
const heapMB = (r.after.heapAllocBytes - r.before.heapAllocBytes) / 1048576;
const badAccepted = r.bad.some((b) => b.endsWith("accepted"));
console.log(`N=${N}: liveFuncs ${r.before.liveFuncs} → ${r.after.liveFuncs} (bound ${r.before.liveFuncs + SLACK}); heap ${(r.before.heapAllocBytes / 1048576).toFixed(2)} → ${(r.after.heapAllocBytes / 1048576).toFixed(2)} MiB (Δ ${heapMB.toFixed(2)}); after session.close() liveFuncs ${r.closed.liveFuncs}`);
const ok = funcsBounded && heapMB < HEAP_MB && !badAccepted && r.alive;
console.log(ok ? "PASS" : "FAIL");
process.exit(ok ? 0 : 1);
