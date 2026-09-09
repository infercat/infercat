import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { resolve } from 'node:path';
import { build } from 'esbuild';
import { chromium } from 'playwright';
import { expect, it } from 'vitest';

// Only the caller's test invite is used. Never reads a default host data directory.
it.skipIf(!process.env.INFERCAT_TEST_INVITE)('lists models and streams a reply through the real wasm and host', async () => {
  const bundle = await build({ entryPoints: ['test/browser.ts'], bundle: true, format: 'esm', write: false });
  const root = resolve('../../web/public');
  const server = createServer(async (request, response) => {
    try {
      if (request.url === '/') { response.setHeader('content-type', 'text/html'); response.end('<title>Infercat client integration</title>'); return; }
      if (request.url === '/client.js') { response.setHeader('content-type', 'text/javascript'); response.end(bundle.outputFiles[0]!.contents); return; }
      const assets: Record<string, string> = { '/infercat.wasm.gz': 'application/gzip', '/wasm_exec.js': 'text/javascript' };
      const type = assets[request.url ?? ''];
      if (!type) { response.writeHead(404).end(); return; }
      response.setHeader('content-type', type);
      response.end(await readFile(resolve(root, request.url!.slice(1))));
    } catch { response.writeHead(500).end(); }
  });
  await new Promise<void>((done) => server.listen(0, '127.0.0.1', done));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('No test server address');
  const base = `http://127.0.0.1:${address.port}`;
  let browser: Awaited<ReturnType<typeof chromium.launch>> | undefined;
  try {
    browser = await chromium.launch();
    const page = await browser.newPage();
    const errors: string[] = []; page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(base);
    await page.addScriptTag({ type: 'module', content: "import { run } from '/client.js'; globalThis.runClientCheck = run;" });
    await page.waitForFunction(() => 'runClientCheck' in globalThis);
    const result = await page.evaluate(async ({ invite, wasmURL }) => {
      const scope = globalThis as unknown as { runClientCheck: typeof import('./browser').run };
      return scope.runClientCheck(invite, wasmURL);
    }, { invite: process.env.INFERCAT_TEST_INVITE!, wasmURL: `${base}/infercat.wasm.gz` });
    expect(result.models.length).toBeGreaterThan(0);
    expect(result.text.trim().length).toBeGreaterThan(0);
    expect(result.arrivals.length).toBeGreaterThan(0);
    expect(errors).toEqual([]);
    console.log('real-host evidence:', JSON.stringify(result));
  } finally {
    await browser?.close(); server.closeAllConnections();
    await new Promise<void>((done) => server.close(() => done()));
  }
}, 120_000);
