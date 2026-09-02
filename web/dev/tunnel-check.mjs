// Drives the REAL wasm bridge (ticket 001) against the REAL tunnel, in a real browser:
// loads web/public/bunny.wasm, connects to hack/tunneldemo over the public relay, and runs
// /healthz and /stream through TunnelTransport's HTTP/1.1 code. Prints measured numbers.
//
//   make wasm && pnpm tunnel-check
//
// Needs the network (public DERP relays). Everything it starts is torn down on exit.
import { spawn } from 'node:child_process';
import { statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const web = join(dirname(fileURLToPath(import.meta.url)), '..');
const repo = join(web, '..');
const WEB_PORT = Number(process.env.WEB_PORT ?? 49173);
const BASE = `http://127.0.0.1:${WEB_PORT}`;

const children = [];
const stop = () => children.forEach((c) => c.kill('SIGTERM'));
process.on('exit', stop);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function startTunnelDemo() {
  return new Promise((resolve, reject) => {
    const child = spawn('go', ['run', './hack/tunneldemo', '-ephemeral', '-demo-listen', ''], {
      cwd: repo,
      env: { ...process.env, GOTOOLCHAIN: 'auto' },
    });
    children.push(child);
    let out = '';
    child.stdout.on('data', (d) => {
      out += d;
      const line = out.split('\n')[0];
      if (out.includes('\n') && line.startsWith('tc')) resolve(line.trim());
    });
    child.stderr.on('data', (d) => process.stderr.write(`[tunneldemo] ${d}`));
    child.on('exit', (code) => reject(new Error(`tunneldemo exited with ${code}`)));
    setTimeout(() => reject(new Error('tunneldemo printed no address within 90 s')), 90_000);
  });
}

async function waitFor(url) {
  for (let i = 0; i < 150; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error(`${url} never came up`);
}

const main = async () => {
  const gz = statSync(join(web, 'public/bunny.wasm.gz')).size;
  const raw = statSync(join(web, 'public/bunny.wasm')).size;
  console.log(`bunny.wasm ${raw} bytes, bunny.wasm.gz ${gz} bytes`);

  console.log('starting hack/tunneldemo (public relay)…');
  const addr = await startTunnelDemo();
  console.log(`tunnel address: ${addr}`);

  const vite = spawn('node', ['node_modules/vite/bin/vite.js', '--port', String(WEB_PORT)], {
    cwd: web,
    env: { ...process.env, WEB_PORT: String(WEB_PORT) },
    stdio: ['ignore', 'ignore', 'inherit'],
  });
  children.push(vite);
  await waitFor(BASE);

  const browser = await chromium.launch();
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
  await page.goto(BASE);

  const result = await page.evaluate(async (tunnelAddr) => {
    const t0 = performance.now();
    const wasm = await import('/src/transport/wasm.ts');
    const transport = await import('/src/transport/index.ts');
    let lastPct = null;
    const bridge = await wasm.loadBunnyTunnel((p) => (lastPct = p.pct));
    const wasmMs = performance.now() - t0;

    const t1 = performance.now();
    const logs = [];
    const session = await bridge.connect({ addr: tunnelAddr, onLog: (l) => logs.push(l) });
    const connectMs = performance.now() - t1;
    const ping = await session.ping();

    const tr = new transport.TunnelTransport(session);
    const t2 = performance.now();
    const health = await tr.fetch('/healthz');
    const healthBody = await health.text();
    const healthMs = performance.now() - t2;

    // /stream is 20 SSE events 100 ms apart. If the first one lands long before the last, the
    // response body is genuinely streaming rather than buffering to completion.
    const t3 = performance.now();
    const stream = await tr.fetch('/stream');
    const reader = stream.body.getReader();
    const decoder = new TextDecoder();
    const arrivals = [];
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf('\n\n')) >= 0) {
        arrivals.push(Math.round(performance.now() - t3));
        buf = buf.slice(i + 2);
      }
    }
    tr.close();
    return {
      wasmMs: Math.round(wasmMs),
      lastPct,
      connectMs: Math.round(connectMs),
      ping,
      healthStatus: health.status,
      healthContentType: health.headers.get('content-type'),
      healthBody,
      healthMs: Math.round(healthMs),
      streamStatus: stream.status,
      streamEvents: arrivals.length,
      firstEventMs: arrivals[0],
      lastEventMs: arrivals.at(-1),
      connectLogSample: logs.slice(0, 3),
    };
  }, addr);

  await browser.close();
  stop();

  console.log('\n--- through the real tunnel ---');
  for (const [k, v] of Object.entries(result)) console.log(`${k}: ${JSON.stringify(v)}`);
  const ok =
    result.healthBody === '{"ok":true}' &&
    result.streamEvents === 20 &&
    result.lastEventMs - result.firstEventMs > 1000 &&
    errors.length === 0;
  console.log(`\n${ok ? 'PASS' : 'FAIL'}: healthz body, 20 streamed events, first event ${result.firstEventMs} ms vs last ${result.lastEventMs} ms`);
  if (errors.length > 0) console.error('browser errors:', errors);
  process.exit(ok ? 0 : 1);
};

main().catch((err) => {
  console.error(err);
  stop();
  process.exit(1);
});
