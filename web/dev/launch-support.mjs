// Cloudflare injects Web Analytics on deployed sites; local builds must remain wholly first-party.
export const CLOUDFLARE_BEACON_ORIGIN = 'https://static.cloudflareinsights.com';
export function landingOrigins(base) {
  const url = new URL(base), host = url.hostname;
  const local = host === 'localhost' || host.endsWith('.localhost') || host === '[::1]' || host === '0.0.0.0' || /^127\./.test(host);
  return [url.origin, ...(!local ? [CLOUDFLARE_BEACON_ORIGIN] : [])];
}

// The spoken answer has its own wrapper; thinking and previous attempts are separate siblings.
export const ANSWER_SELECTOR = '.row.assistant > div:not(.thinking):not(.meta) > .md';
export async function completedAnswer(page, timeout = 180_000) {
  await page.locator('.row.assistant').last().waitFor({ timeout });
  await page.getByRole('button', { name: 'Stop', exact: true }).waitFor({ state: 'detached', timeout });
  const row = page.locator('.row.assistant').last();
  const answer = await page.locator(ANSWER_SELECTOR).last().innerText({ timeout });
  if (!answer.trim() || await row.locator('.ended, .waiting').count() || await row.evaluate(el => el.classList.contains('streaming'))) {
    throw new Error('chat: no complete answer');
  }
  return answer;
}

// Browser exceptions can contain the redirect URL or a credential shown in the DOM.
export function safeDiagnostic(value) {
  return String(value).replace(/https?:\/\/[^\s"'<>]+/g, url => url.split('#')[0])
    .replace(/\bic[12]\.[A-Za-z0-9_.…-]+/g, '[invite redacted]');
}

export async function liveChat(browser, base, report = console.log) {
  const context = await browser.newContext({ locale: 'en-US', viewport: { width: 1280, height: 800 } });
  const page = await context.newPage(), errors = [];
  page.on('pageerror', () => errors.push('page error'));
  page.on('console', m => { if (['warning', 'error'].includes(m.type())) errors.push(`console ${m.type()}`); });
  let stage = 'demo connection';
  try {
    await page.goto(new URL('/try', base).href);
    await page.locator('.composer textarea').waitFor({ timeout: 120_000 });
    report('LIVE connection: PASS — public demo connected');
    stage = 'complete answer';
    await page.locator('.composer textarea').fill('Say hello in one short sentence.');
    await page.getByRole('button', { name: 'Send', exact: true }).click();
    await completedAnswer(page);
    if (errors.length) throw new Error('browser errors');
    report('LIVE chat: PASS — one short message, nonempty complete answer, no browser errors');
  } catch { throw new Error(`LIVE ${stage}: FAIL (browser details withheld to protect the invite)`); }
  finally { await context.close(); }
}
