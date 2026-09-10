import { Buffer } from 'node:buffer';
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const base = process.env.RUN_CHECK_URL ?? 'http://127.0.0.1:49139', out = process.env.RUN_CHECK_OUT ?? '/private/tmp/infercat-139-chat';
mkdirSync(out, { recursive: true });
const browser = await chromium.launch(), errors = []; let checks = 0, shots = 0;
const ok = (value, label) => { assert.ok(value, label); checks++; };
const invite = `ic1.tcRUNproofaddressRUNproofaddress.${'D'.repeat(43)}`;
const prompt = 'Look up what the rate-limits doc says about the burst rule and write a two-line summary to notes.md.';
async function connect(width, lang, state = 'running', vision = false) {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 }, isMobile: width === 390, hasTouch: width === 390, locale: lang === 'zh' ? 'zh-CN' : 'en-US' });
  await context.addInitScript((language) => window.localStorage.setItem('bn.language', JSON.stringify(language)), lang);
  const page = await context.newPage(); page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(`${base}/?fake&agent&transcriptions&speech&connectMs=20&runState=${state}${vision ? '&vision=true' : ''}&invite=${encodeURIComponent(invite)}&autoconnect`);
  await page.locator('.composer textarea').waitFor(); await page.waitForFunction(() => !document.querySelector('.composer textarea').disabled);
  return { page, context };
}
async function send(page, text = prompt, snapshot = true) {
  await page.locator('.composer textarea').fill(text); await page.locator('.composer .primary').click();
  await page.locator('.row.run').waitFor();
  await page.waitForFunction(() => { const { runRequests } = window.__fakeRuns; return runRequests.some((r) => r.method === 'GET' && /^\/v1\/runs\/r-/.test(r.path)); });
  if (snapshot) await page.locator('.row.run[data-run-state]').first().waitFor();
}
async function capture(page, name) {
  await page.evaluate(() => document.fonts.ready);
  ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${name}: no page overflow`);
  await page.screenshot({ path: `${out}/${name}.png` }); shots++;
}
try {
  if (!process.env.RUN_CHECK_EDGES) for (const width of [390,1280]) for (const lang of ['en','zh']) for (const state of ['queued','unranked','running','waiting','done','failed','cancelled']) {
    const { page, context } = await connect(width, lang, state); await send(page);
    if (['done','cancelled'].includes(state)) await page.locator('.row.run .chip').waitFor();
    await page.locator('.composer textarea').fill('The next message can still be sent.');
    ok(await page.locator('.composer .primary').isEnabled(), `${state}: composer live`);
    await page.locator('.composer textarea').fill(''); await page.locator('.thread').click({ position: { x: 4, y: 4 } });
    await capture(page, `${state}-${width}-${lang}`);
    if (state === 'running') {
      await page.locator('.steps summary').click();
      await capture(page, `steps-${width}-${lang}`);
      await page.locator('.step button.r').click(); await page.locator('.sheet.file').waitFor();
      ok((await page.locator('.sheet.file pre').innerText()).startsWith('Four search results'), 'full result through scoped output route');
      await capture(page, `result-${width}-${lang}`); await page.keyboard.press('Escape');
      await page.evaluate(async () => {
        const { updateRun, get } = window.__fakeRuns;
        const steps = get('r-1').steps;
        updateRun('r-1', { steps: steps.map((s) => s.id === 'r' ? { ...s, kind: 'write', name: 'notes.md', status: 'running' } : s) });
        updateRun('r-1', { steps: steps.map((s) => s.id === 'r' ? { ...s, kind: 'write', name: 'notes.md', status: 'done', result: '2 lines', text: 'one\ntwo' } : s) });
      });
      await page.waitForFunction(() => document.querySelectorAll('.step').length === 3 && [...document.querySelectorAll('.step')].at(-1).textContent.includes('notes.md'));
      ok(await page.locator('.step').count() === 3, 'full replacement step does not duplicate');
    }
    if (state === 'waiting') {
      ok(await page.locator('.row.run .live').count() === 0, 'approval holds no live square');
      await page.locator('.row.run .actions button').first().click();
      await page.waitForFunction(() => !document.querySelector('.ask'));
      const writes = await page.evaluate(async () => window.__fakeRuns.runRequests.filter((r) => r.path.endsWith('/approval')));
      ok(writes.length === 1 && writes[0].body.allow === true && writes[0].body.id === 'approval-1', 'one explicit approval with id');
    }
    if (state === 'queued') {
      await page.locator('.row.run .spoken button').click();
      await page.waitForFunction(() => !document.querySelector('.row.run .spoken button'));
      ok((await page.evaluate(async () => window.__fakeRuns.runRequests.filter((r) => r.method === 'DELETE'))).length === 1, 'Cancel uses one DELETE');
    }
    if (state === 'done') {
      await page.locator('.row.run .chip').click(); await page.locator('.sheet.file').waitFor();
      ok((await page.locator('.sheet.file pre').innerText()).startsWith('The burst rule'), 'file sheet contains raw produced text');
      ok(!(await page.locator('.sheet.file pre').innerText()).startsWith('[file:'), 'output is not wrapped as sent input');
      await capture(page, `output-${width}-${lang}`);
    }
    await context.close();
  }
  if (!process.env.RUN_CHECK_EDGES) for (const width of [390,1280]) for (const lang of ['en','zh']) {
    const { page, context } = await connect(width, lang); await send(page);
    await page.evaluate(() => window.dispatchEvent(new window.Event('offline')));
    await page.waitForFunction(() => document.querySelectorAll('.row.run .live').length === 0);
    ok((await page.locator('.row.run').innerText()).includes(lang === 'zh' ? '运行 stats.py' : 'running stats.py'), 'disconnection preserves the observed run line');
    await capture(page, `away-${width}-${lang}`);
    await page.evaluate(async () => { const { updateRun, scriptedRun } = window.__fakeRuns; updateRun('r-1', scriptedRun('r-1', 'done')); window.dispatchEvent(new window.Event('online')); });
    await page.locator('.row.run .chip').waitFor();
    await capture(page, `returned-${width}-${lang}`);
    await page.reload(); await page.locator('.row.run .chip').waitFor();
    ok(await page.locator('.row.run').count() === 1, 'return rebuilds one completed run');
    await context.close();
  }
  if (!process.env.RUN_CHECK_EDGES) for (const width of [390,1280]) for (const lang of ['en','zh']) for (const uncertain of [false,true]) {
    const { page, context } = await connect(width, lang);
    await page.evaluate(async (uncertain) => {
      const { TunnelTransport } = await import('/src/transport/index.ts'), original = TunnelTransport.prototype.fetch;
      TunnelTransport.prototype.fetch = function(path, init) {
        if (path !== '/v1/runs' || init?.method !== 'POST') return original.call(this, path, init);
        const request = JSON.parse(init.body);
        window.correlationPersisted = Object.keys(window.localStorage).some((key) => key.startsWith('bn.conversations.') && (window.localStorage.getItem(key) ?? '').includes(request.client_request_id));
        if (uncertain) return Promise.reject(new Error('Connection lost before a response'));
        return new Promise((resolve, reject) => { window.finishRunSubmit = () => original.call(this, path, init).then(resolve, reject); });
      };
    }, uncertain);
    await page.locator('.composer textarea').fill(prompt); await page.locator('.composer .primary').click();
    await page.locator(uncertain ? '.row.run .waiting' : '.row.run .spoken').waitFor();
    ok(await page.evaluate(() => window.correlationPersisted), 'correlation mapping persisted before POST');
    ok(await page.locator('.row.run button').count() === 0, 'unconfirmed creation has no retry action');
    await capture(page, `${uncertain ? 'uncertain' : 'submitting'}-${width}-${lang}`);
    await context.close();
  }
  // The response is lost after the fake host commits: each mutation still occurs once.
  for (const operation of ['create','cancel','approve','deny']) {
    const { page, context } = await connect(390, 'en', operation === 'approve' || operation === 'deny' ? 'waiting' : 'running');
    await page.evaluate(async (op) => {
      const { TunnelTransport } = await import('/src/transport/index.ts'); const original = TunnelTransport.prototype.fetch;
      let lost = false;
      TunnelTransport.prototype.fetch = async function(path, init) {
        const response = await original.call(this, path, init);
        if (!lost && ((op === 'create' && path === '/v1/runs' && init?.method === 'POST') || (op === 'cancel' && init?.method === 'DELETE') || (op === 'approve' && path.endsWith('/approval')))) { lost = true; throw new Error('Response lost after commit'); }
        return response;
      };
    }, operation);
    await send(page, prompt, operation !== 'create');
    if (operation === 'cancel') { await page.locator('.row.run .spoken button').click(); await page.waitForFunction(() => !document.querySelector('.row.run .spoken button')); }
    if (operation === 'approve' || operation === 'deny') { await page.locator('.row.run .actions button').nth(operation === 'deny' ? 1 : 0).click(); await page.waitForFunction(() => !document.querySelector('.ask')); }
    const requests = await page.evaluate(async () => window.__fakeRuns.runRequests);
    ok(requests.filter((r) => r.method === 'POST' && r.path === '/v1/runs').length === 1, `${operation}: no create replay ${JSON.stringify(requests.map((r) => [r.method,r.path]))}`);
    if (operation === 'create') { await page.locator('.row.run[data-run-state]').waitFor(); ok(await page.locator('.row.run').count() === 1, 'lost response reconciles by metadata into the original row'); }
    if (operation === 'cancel') ok(requests.filter((r) => r.method === 'DELETE').length === 1, 'ambiguous Cancel not replayed');
    if (operation === 'approve' || operation === 'deny') ok(requests.filter((r) => r.path.endsWith('/approval')).length === 1, 'approval not replayed');
    await context.close();
  }
  {
    const { page, context } = await connect(390, 'en');
    await page.locator('input[type=file]').setInputFiles({ name: 'notes.md', mimeType: 'text/markdown', buffer: Buffer.from('file evidence with enough readable characters for extraction') });
    await page.locator('.attached .chip').waitFor(); await send(page, 'Use this file.');
    const input = await page.evaluate(async () => window.__fakeRuns.runRequests.find((r) => r.method === 'POST').body.input);
    ok(input.messages.at(-1).content.includes('file evidence') && input.messages.at(-1).content.endsWith('Use this file.'), 'file text carried unchanged into agent input');
    ok(input.model && input.temperature === 0.7, 'model and settings carried');
    await context.close();
  }
  {
    const { page, context } = await connect(390, 'en', 'running', true);
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
    await page.locator('.sheet select').first().selectOption('deepseek-v4-flash');
    await page.locator('.sheet textarea').fill('Keep the supplied evidence intact.');
    await page.locator('.sheet-actions .primary').click();
    await page.locator('input[type=file]').setInputFiles(new URL('./fixtures/upload/color.png', import.meta.url).pathname);
    await page.locator('.attached .thumb img').waitFor(); await send(page, 'Read this image.');
    const input = await page.evaluate(() => window.__fakeRuns.runRequests.find((r) => r.method === 'POST').body.input);
    ok(input.messages.at(-1).content.some((p) => p.type === 'image_url' && p.image_url.url.startsWith('data:image/')), 'image content parts carried into agent input');
    ok(input.messages[0].role === 'system' && input.messages[0].content === 'Keep the supplied evidence intact.' && input.model === 'deepseek-v4-flash', 'chosen model and system prompt apply to runs');
    await context.close();
  }
  {
    const { page, context } = await connect(390, 'en'); await send(page);
    await page.locator('.row.user .actions button').click();
    await page.locator('.bubble.editing textarea').fill('Write an edited summary.');
    await page.locator('.edit-actions .primary').click();
    await page.waitForFunction(() => document.querySelectorAll('.row.run[data-run-state] .spoken').length === 2);
    ok(await page.locator('.row.user').count() === 2 && (await page.locator('.row.user').first().innerText()).includes(prompt), 'Edit preserves old turn and appends the edited question');
    ok(await page.locator('.row.run .spoken button').count() === 2, 'old live run keeps explicit Cancel');
    const inputs = await page.evaluate(() => window.__fakeRuns.runRequests.filter((r) => r.path === '/v1/runs' && r.method === 'POST').map((r) => r.body.input));
    ok(inputs.length === 2 && inputs[1].messages.length === 1 && inputs[1].messages[0].content === 'Write an edited summary.', 'edited input uses edited history, not appended display history');
    await capture(page, 'edit-preserves-run-390-en'); await context.close();
  }
  {
    const { page, context } = await connect(390, 'en', 'failed'); await send(page);
    await page.locator('.row.run .actions button').evaluate((button) => { button.click(); button.click(); });
    await page.waitForFunction(() => document.querySelectorAll('.row.run[data-run-state]').length === 2);
    ok((await page.evaluate(() => window.__fakeRuns.runRequests.filter((r) => r.path === '/v1/runs' && r.method === 'POST'))).length === 2, 'explicit retry appends once and double-click does not replay');
    await context.close();
  }
  assert.deepEqual(errors, []);
  console.log(`${checks} passed / 0 failed / 0 skipped; ${shots} screenshots; 0 browser errors`);
} finally { await browser.close(); }
