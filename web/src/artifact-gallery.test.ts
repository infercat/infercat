import { expect, it } from 'vitest';
import { chromium } from 'playwright';
import { createServer } from 'vite';

// Render the real sheet, rather than a test-only queue consumer. Each of its eight
// tiles sees one 429; successful responses keep their bodies open to measure slots.
it('renders all eight tiles after one 429 round, with four complete reads at most', async () => {
  const fixture = `<!doctype html><div id="root"></div><script type="module">
    import React from 'react';
    import { createRoot } from 'react-dom/client';
    import { ImagesSheet } from '/src/ui/ImageJobs.tsx';
    const metrics = window.metrics = { active: 0, peak: 0, attempts: {}, waits: [], total: 0 };
    const rejectedAt = {};
    const canvas = document.createElement('canvas'); canvas.width = canvas.height = 1;
    const png = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
    const bytes = new Uint8Array(await png.arrayBuffer());
    const transport = { async fetch(path, init) {
      if (new Headers(init.headers).get('authorization') !== 'Bearer fixture') throw Error('Missing auth');
      const id = path.split('/').pop();
      metrics.total++; metrics.active++; metrics.peak = Math.max(metrics.peak, metrics.active);
      const attempt = metrics.attempts[id] = (metrics.attempts[id] || 0) + 1;
      if (attempt > 1) metrics.waits.push(performance.now() - rejectedAt[id]);
      await new Promise(resolve => setTimeout(resolve, 30));
      if (attempt === 1) {
        metrics.active--; rejectedAt[id] = performance.now();
        return new Response(JSON.stringify({error:{code:'concurrency_limited',message:'too many images loading at once; retry in a second'}}), {status:429,headers:{'Retry-After':'1','Content-Type':'application/json'}});
      }
      return new Response(new ReadableStream({ start(controller) {
        setTimeout(() => { controller.enqueue(bytes); controller.close(); metrics.active--; }, 60);
      }}), {headers:{'Content-Type':'image/png'}});
    }};
    const now = new Date().toISOString();
    const jobs = Array.from({length:8}, (_,i) => ({id:'tile-'+i,kind:'image',key_id:'key',state:'done',created:now,updated:now,expires:now,cancel_requested:false,input:{prompt:'Picture '+i},batch:{id:'batch',index:i,count:8},attempts:[],output:{url:'ignored',mime:'image/png',w:1,h:1,bytes:bytes.length,expiresAt:new Date(Date.now()+86400000).toISOString()}}));
    const live = {transport,secret:'fixture',offline:false,me:{host:{name:'Fixture',images:{model:'image-model',retention_days:7}}}};
    const noop = () => {};
    createRoot(document.getElementById('root')).render(React.createElement(ImagesSheet,{jobs,live,connected:true,disabled:false,pending:new Set(),onClose:noop,onEdit:noop,onConversation:noop,conversationTitle:()=> 'Chat',onCancel:noop,onDiscard:noop}));
  </script>`;
  const server = await createServer({ configFile: false, root: process.cwd(), server: { host: '127.0.0.1', port: 0 }, plugins: [{
    name: 'eight-tile-fixture', configureServer(vite) {
      vite.middlewares.use((req, res, next) => {
        if (req.url !== '/gallery') { next(); return; }
        void vite.transformIndexHtml('/gallery', fixture).then((html) => { res.setHeader('Content-Type', 'text/html'); res.end(html); });
      });
    },
  }] });
  let browser: Awaited<ReturnType<typeof chromium.launch>> | undefined;
  try {
    browser = await chromium.launch({ headless: true });
    await server.listen();
    const page = await browser.newPage();
    const errors: string[] = []; page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${server.resolvedUrls!.local[0]}gallery`);
    await page.waitForFunction(() => (window as unknown as {metrics:{total:number}}).metrics?.total === 8);
    expect(await page.locator('[data-image-job]').count()).toBe(8);
    expect(await page.locator('body').innerText()).not.toContain('too many images loading');
    await page.waitForFunction(() => [...document.querySelectorAll<HTMLImageElement>('[data-image-job] img')].filter((img) => img.complete && img.naturalWidth > 0).length === 8);
    const metrics = await page.evaluate(() => (window as unknown as {metrics:{peak:number;active:number;total:number;attempts:Record<string,number>;waits:number[]}}).metrics);
    expect(metrics.peak).toBe(4); expect(metrics.active).toBe(0); expect(metrics.total).toBe(16);
    expect(Object.values(metrics.attempts)).toEqual(Array(8).fill(2));
    expect(metrics.waits).toHaveLength(8); expect(Math.min(...metrics.waits)).toBeGreaterThanOrEqual(990);
    expect(await page.locator('body').innerText()).not.toContain('too many images loading');
    expect(errors).toEqual([]);
  } finally { await browser?.close(); await server.close(); }
}, 20000);
