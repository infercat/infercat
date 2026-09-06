// Manual and automated check for the wasm bridge (promise 4 of ticket 001). Results land in
// window.__demo for demo-check.mjs; the log <pre> shows them to a person.
const $ = (id) => document.getElementById(id);
const log = (s) => { $("log").textContent += s + "\n"; console.log(s); };
const enc = new TextEncoder(), dec = new TextDecoder();
window.__demo = { results: {} };
let session;

async function loadWasm() {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("/public/infercat.wasm"), go.importObject);
  go.run(instance); // runs main() until it blocks, so InfercatTunnel is set when this returns
  const gz = await fetch("/public/infercat.wasm.gz", { method: "HEAD" });
  window.__demo.wasmGzBytes = Number(gz.headers.get("content-length"));
  log(`wasm loaded; infercat.wasm.gz is ${window.__demo.wasmGzBytes} bytes`);
}

// httpGet sends one raw HTTP/1.1 request over a fresh tunnel stream and returns the response
// with timings (ms since the dial started). onChunk sees each chunk as it arrives.
async function httpGet(path, onChunk) {
  const t0 = performance.now();
  const conn = await session.dial(80);
  const dialMs = performance.now() - t0;
  await conn.write(enc.encode(`GET ${path} HTTP/1.1\r\nHost: tunnel\r\nConnection: close\r\n\r\n`));
  await conn.closeWrite();
  const chunks = []; let bytes = 0, firstMs = 0;
  for (;;) {
    const c = await conn.read();
    if (c === null) break;
    if (!firstMs) firstMs = performance.now() - t0;
    chunks.push(c); bytes += c.length;
    onChunk?.(dec.decode(c), performance.now() - t0);
  }
  conn.close();
  const all = new Uint8Array(bytes); let off = 0;
  for (const c of chunks) { all.set(c, off); off += c.length; }
  return { text: dec.decode(all), bytes, dialMs, firstMs, totalMs: performance.now() - t0 };
}

const fmt = (r) => `dial ${r.dialMs.toFixed(0)} ms, first byte ${r.firstMs.toFixed(0)} ms, total ${r.totalMs.toFixed(0)} ms, ${r.bytes} bytes`;

async function connect() {
  if (!window.InfercatTunnel) await loadWasm();
  const addr = $("addr").value.trim();
  log(`connecting to ${addr.slice(0, 20)}… (DERP map ${$("derp").value})`);
  const t0 = performance.now();
  session = await window.InfercatTunnel.connect({ addr, derpMapURL: $("derp").value, verbose: $("verbose").checked, onLog: (l) => log("  bridge: " + l) });
  const connectMs = performance.now() - t0;
  log(`connected in ${connectMs.toFixed(0)} ms; client identity ${session.privateKeyJSON.length} bytes of key JSON`);
  const pings = [];
  for (let i = 0; i < 3; i++) {
    pings.push(await session.ping());
    log(`ping: rtt ${pings[i].rttMs.toFixed(1)} ms via ${pings[i].via} direct=${pings[i].direct}`);
  }
  const healthz = await httpGet("/healthz");
  log(`GET /healthz: ${fmt(healthz)}\n${healthz.text}`);
  Object.assign(window.__demo.results, { addr, connectMs, pings, healthz });
  $("again").disabled = $("stream").disabled = false;
}
async function again() {
  const r = await httpGet("/healthz");
  log(`GET /healthz again: ${fmt(r)} → ${r.text.split("\r\n\r\n")[1]}`);
  window.__demo.results.again = r;
}
async function stream() {
  let events = 0;
  const r = await httpGet("/stream", (s, ms) => { events += (s.match(/^data:/gm) || []).length; log(`  +${ms.toFixed(0)} ms: ${s.trim().split("\n").pop()}`); });
  log(`GET /stream: ${events} events, ${fmt(r)}`);
  window.__demo.results.stream = { events, ...r, text: undefined };
}
const guard = (f) => async () => { try { await f(); } catch (e) { log("error: " + (e.message ?? e)); window.__demo.error = String(e.message ?? e); } };
$("connect").onclick = guard(connect);
$("again").onclick = guard(again);
$("stream").onclick = guard(stream);

const q = new URLSearchParams(location.search);
if (q.get("addr")) {
  $("addr").value = q.get("addr");
  if (q.get("derp")) $("derp").value = q.get("derp");
  if (q.get("verbose")) $("verbose").checked = true;
  guard(async () => { await connect(); if (q.get("auto")) { await again(); await stream(); session.close(); window.__demo.done = true; } })();
}
