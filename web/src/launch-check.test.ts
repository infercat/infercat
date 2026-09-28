import { afterAll, beforeAll, expect, it } from 'vitest';
import { chromium, type Browser } from 'playwright';
import { createServer, type ViteDevServer } from 'vite';
import { ANSWER_SELECTOR, CLOUDFLARE_BEACON_ORIGIN, completedAnswer, landingOrigins, liveChat, safeDiagnostic } from '../dev/launch-support.mjs';

it('allows exactly the HTTPS Cloudflare beacon origin only on deployed targets', () => {
  for (const origin of ['http://localhost:6833', 'http://127.0.0.1:6833', 'http://127.9.1.2', 'http://[::1]:6833', 'http://app.localhost']) {
    expect(landingOrigins(origin)).toEqual([origin]);
  }
  const allowed = landingOrigins('https://infercat.ai');
  expect(allowed).toEqual(['https://infercat.ai', CLOUDFLARE_BEACON_ORIGIN]);
  for (const origin of ['http://static.cloudflareinsights.com', 'https://static.cloudflareinsights.com.evil.test', 'https://cloudflareinsights.com', 'https://cdn.example.com']) expect(allowed).not.toContain(origin);
});
it('redacts invite strings and URL fragments in browser diagnostics', () => {
  const secret = `ic2.test-address.${'s'.repeat(43)}`;
  const line = safeDiagnostic(`goto https://infercat.ai/?from=try#${secret}\nDOM ${secret}; https://local.test/#anything`);
  expect(line).not.toContain(secret); expect(line).not.toContain('#');
  expect(line).toContain('https://infercat.ai/?from=try');
});

let server: ViteDevServer, browser: Browser, origin: string, mode = 'normal';
const fixture = `<!doctype html><div id="root"></div><script type="module">
import React from 'react'; import {createRoot} from 'react-dom/client';
import Chat from '/src/ui/Chat.tsx'; import {handleFake} from '/dev/fake-backend.ts';
window.sent = 0;
const options={tokenDelayMs:1,ttftMs:10,streamMode:new URLSearchParams(location.search).get('mode')};
const transport={kind:'direct',close(){},ping:async()=>null,async fetch(path,init={}){
 if(path==='/v1/events')return new Response(new ReadableStream({start(c){init.signal?.addEventListener('abort',()=>c.close(),{once:true});}}));
 if(path==='/v1/chat/completions')window.sent++;
 const out=handleFake({method:init.method||'GET',path,headers:Object.fromEntries(new Headers(init.headers)),body:String(init.body||'')},options);
 const body=out.sse?new ReadableStream({async start(c){for await(const frame of out.sse)c.enqueue(new TextEncoder().encode(frame));c.close();}}):out.body;
 return new Response(body,{status:out.status,headers:out.headers});
}};
const me=await (await transport.fetch('/me',{headers:{authorization:'Bearer fixture'}})).json();
const live={addr:'launch-check',secret:'fixture',transport,me,mode:'direct',path:null,pathAt:0,pathOk:true,meOk:true,key:'active',ephemeral:true,probed:0};
createRoot(document.getElementById('root')).render(React.createElement(Chat,{live,state:{name:'connected',live},dispatch(){},onRedial(){}}));
</script>`;
beforeAll(async () => {
  server = await createServer({ configFile: false, root: process.cwd(), server: { host: '127.0.0.1', port: 0 }, plugins: [{ name: 'launch-chat-fixture', configureServer(vite) {
    vite.middlewares.use((req, res, next) => {
      if (req.url === '/try') { res.writeHead(302, { Location: `/fixture?mode=${mode}#ic2.fake.secret` }); res.end(); return; }
      if (!req.url?.startsWith('/fixture')) { next(); return; }
      void vite.transformIndexHtml('/fixture', fixture).then(html => { res.setHeader('Content-Type', 'text/html'); res.end(html); });
    });
  } }] });
  await server.listen(); origin = server.resolvedUrls!.local[0]!;
  browser = await chromium.launch();
}, 20000);
afterAll(async () => { await browser?.close(); await server?.close(); });
// CI runner, 20 cold-transform samples: p95 2.80 s (connection 0.68 s, chat 2.11 s).
// Allow ~5x p95 for contention, rounded to the sibling browser cases' 15 s budget.
it('the live path follows the demo redirect and sends one completed fake-gateway chat without leaking its fragment', async () => {
  const lines: string[] = [];
  await liveChat(browser, origin, line => lines.push(line));
  expect(lines).toEqual(['LIVE connection: PASS — public demo connected', 'LIVE chat: PASS — one short message, nonempty complete answer, no browser errors']);
}, 15000);
it.each(['normal', 'reasoning-only', 'eof-no-done', 'error-mid-stream', 'capped'])('checks the real answer wrapper and stream completion: %s', async (streamMode) => {
  mode = streamMode;
  const context = await browser.newContext({ locale: 'en-US' });
  try {
    const page = await context.newPage(); await page.goto(new URL('/try', origin).href);
    await page.locator('.composer textarea').fill('Why is the sky blue?');
    await page.getByRole('button', { name: 'Send', exact: true }).click();
    const answer = completedAnswer(page, 5000);
    if (mode === 'normal') {
      expect(await answer).toContain('sky');
      expect(await page.locator('.row.assistant > .md').count()).toBe(0); // old selector cannot find it
      expect(await page.locator(ANSWER_SELECTOR).count()).toBe(1);
      expect(await page.locator('.thinking .md').count()).toBe(1);
      await page.locator('.row.assistant').evaluate(row => { const old = document.createElement('details'); old.className = 'previous'; old.innerHTML = '<div class="md">OLD ANSWER</div>'; row.prepend(old); });
      expect(await completedAnswer(page)).not.toContain('OLD ANSWER');
    } else await expect(answer).rejects.toThrow('chat: no complete answer');
    expect(await page.evaluate(() => (window as unknown as {sent:number}).sent)).toBe(1);
  } finally { await context.close(); }
}, 15000);
