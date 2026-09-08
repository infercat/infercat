// Shared 043 evidence: the same matrix is exercised by launch-check and the screenshot harness.
import { join } from 'node:path';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

// The authored tables are literals; read their AST so the browser gate checks the actual copy,
// including identical EN/ZH placeholders, without exposing test-only data in the app bundle.
function table(lang) {
  const file = ts.createSourceFile(lang, readFileSync(new URL(`../../i18n/${lang}.ts`, import.meta.url), 'utf8'), ts.ScriptTarget.Latest);
  const result = {};
  function visit(node) {
    if (ts.isPropertyAssignment(node) && ts.isStringLiteral(node.initializer)) result[node.name.getText(file)] = node.initializer.text;
    ts.forEachChild(node, visit);
  }
  visit(file);
  return result;
}
const tables = { en: table('en'), zh: table('zh') };

async function checkCopy(page, lang, assert) {
  const checked = await page.evaluate((expected) => {
    const issues = [], keys = [];
    for (const [marker, attribute] of [['data-copy', null], ['data-copy-aria', 'aria-label'], ['data-copy-placeholder', 'placeholder']]) {
      for (const el of document.querySelectorAll(`[${marker}]`)) {
        const key = el.getAttribute(marker);
        const template = document.createElement('template');
        template.innerHTML = expected[key];
        // The copy owns the words; the product constant supplies the host quickstart URL.
        if (key === 'f_quiet') template.content.querySelector('a').setAttribute('href', document.querySelector('.page-nav a').getAttribute('href'));
        if (key === 'terminal') template.content.textContent = expected[key].replace('{invite}', [...document.querySelectorAll('.anat .invite-part')].map((p) => p.innerText).join('.'));
        const wanted = attribute ? expected[key] : key === 'terminal' ? template.content.textContent : template.innerHTML;
        const actual = attribute ? el.getAttribute(attribute) : key === 'terminal' ? el.innerText : el.innerHTML;
        if (actual !== wanted) issues.push(`${key}: ${actual}`);
        keys.push(key);
      }
    }
    return { issues, keys };
  }, tables[lang]);
  assert(checked.issues.length === 0, `copy differs from ${lang}: ${checked.issues.join(', ')}`);
  for (const key of ['path_label', 'roadmap_label', 'node_f', 'node_r', 'node_h']) assert(checked.keys.includes(key), `aria coverage ${key}`);
  return checked.keys;
}


export async function landingEvidence(browser, base, shots, inspect = async () => {}) {
  let checked = 0;
  const scheme = 'light';
  for (const width of [1280, 1024, 390]) for (const lang of ['en', 'zh']) {
    const label = `043-${width}-${scheme}-${lang}`;
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, locale: lang === 'zh' ? 'zh-CN' : 'en-US' });
    const page = await context.newPage();
    const errors = [], foreign = [], videoRequests = [];
    page.on('request', (r) => { if (/\.mp4(?:$|\?)/.test(r.url())) videoRequests.push(r.url()); });
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('request', (r) => { if (new URL(r.url()).origin !== new URL(base).origin) foreign.push(r.url()); });
    await page.goto(base);
    await page.locator('.landing').waitFor();
    // A dark OS must not select the dormant theme, including native controls and light-dark().
    await page.emulateMedia({ colorScheme: 'dark' });
    const palette = await page.evaluate(() => ({
      scheme: window.getComputedStyle(document.documentElement).colorScheme,
      paper: window.getComputedStyle(document.body).backgroundColor,
      selected: document.documentElement.hasAttribute('data-theme'),
    }));
    if (palette.scheme !== 'light' || palette.paper !== 'rgb(255, 255, 255)' || palette.selected) throw new Error(`${label}: OS selected a dark page: ${JSON.stringify(palette)}`);
    await page.emulateMedia({ colorScheme: 'light' });
    await page.evaluate(() => document.fonts.ready);
    const assert = (ok, message) => { if (!ok) throw new Error(`${label}: ${message}`); };
    assert(await page.locator('.landing').evaluate((e, lang) => e.classList.contains(lang), lang), 'navigator language not selected');
    assert(await page.locator('.landing > section').count() === 4, 'four sections');
    await page.locator('.landing details').evaluateAll((els) => els.forEach((e) => { e.open = true; }));
    await inspect(page, label);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    assert(!overflow, 'horizontal page overflow');
    assert(await page.locator('.quiet a').count() === 1, 'one host-your-own tab stop');
    if (lang === 'zh') {
      const missing = await page.locator('.landing-page *').evaluateAll((els) => els.filter((e) => {
        const hasChinese = [...e.childNodes].some((n) => n.nodeType === 3 && /[\u3400-\u9fff]/.test(n.textContent));
        return hasChinese && e.getBoundingClientRect().width && !window.getComputedStyle(e).fontFamily.includes('Noto Sans SC');
      }).map((e) => e.className));
      assert(!missing.length, `CJK font missing on ${missing.join(', ')}`);
    }
    const bracket = await page.locator('.brk path').evaluateAll((els) => els.filter((e) => window.getComputedStyle(e).display !== 'none').map((e) => e.getAttribute('d')));
    assert(bracket.length === 1 && bracket[0] === (width === 390 ? 'M 0 0 H 100 V 100 H 0' : 'M 0 0 V 100 H 100 V 0'), 'responsive SVG path attribute');
    const geometry = await page.evaluate(() => {
      const failures = [];
      for (const figure of document.querySelectorAll('.landing .fig, .landing .rm')) {
        const bounds = figure.getBoundingClientRect();
        for (const node of figure.querySelectorAll('.node, .bx')) {
          const box = node.getBoundingClientRect();
          if (box.left < bounds.left - 0.5 || box.right > bounds.right + 0.5 || box.top < bounds.top - 0.5 || box.bottom > bounds.bottom + 0.5) {
            failures.push(`${node.className} outside ${figure.className}: [${box.left},${box.top},${box.right},${box.bottom}] vs [${bounds.left},${bounds.top},${bounds.right},${bounds.bottom}]`);
          }
        }
      }
      // Both relay legs must meet the node centres: containment alone misses a line at the edge.
      const nodes = [...document.querySelectorAll('.landing .fig .node')].map((e) => e.getBoundingClientRect());
      const axis = window.innerWidth < 900 ? 'x' : 'y';
      for (const leg of document.querySelectorAll('.landing .fig .leg')) {
        const box = leg.getBoundingClientRect();
        const centre = axis === 'x' ? box.left + box.width / 2 : box.top + box.height / 2;
        if (nodes.some((n) => Math.abs(centre - (axis === 'x' ? n.left + n.width / 2 : n.top + n.height / 2)) > 1)) failures.push(`${leg.className.baseVal} misses node centres`);
      }
      return failures;
    });
    assert(geometry.length === 0, `figure geometry: ${geometry.join('; ')}`);
    const boxes = await page.locator('.node, .rm .bx, .def-v, .seg .n').evaluateAll((els) => els.filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent));
    assert(boxes.length === 0, `overflowing labels: ${boxes.join(', ')}`);
    await page.locator('.landing details').evaluateAll((els) => els.forEach((e) => { e.open = false; }));
    await page.screenshot({ path: join(shots, `${label}.png`), fullPage: true });
    const video = page.locator('.landing-demo video');
    const media = await video.evaluate((v) => ({
      autoplay: v.autoplay || v.hasAttribute('autoplay'), preload: v.preload,
      source: v.src, poster: v.poster, controls: v.controls, inline: v.playsInline, paused: v.paused,
    }));
    const suffix = lang === 'zh' ? '.zh' : '';
    assert(!videoRequests.length, 'no MP4 request before play');
    assert(!media.autoplay && media.preload === 'none' && media.paused, 'demo does not load or play automatically');
    assert(media.source === new URL(`/demo${suffix}.mp4`, base).href, 'same-origin language video');
    assert(media.poster === new URL(`/demo-poster${suffix}.png`, base).href, 'language poster');
    assert(media.controls && media.inline, 'native inline controls');
    assert(await page.locator('.demo-play').getAttribute('aria-label') === tables[lang].demo_label, 'play accessible name');
    const placement = await page.locator('#p-s1 .s-grid').evaluate((grid) => {
      const text = grid.querySelector('.st').getBoundingClientRect();
      const art = grid.querySelector('.art').getBoundingClientRect();
      const demo = grid.querySelector('.landing-demo').getBoundingClientRect();
      return window.innerWidth < 900 ? demo.top >= art.bottom : demo.top >= text.bottom && Math.abs(demo.left - text.left) < 1;
    });
    assert(placement, 'demo follows the prose on desktop and figure on phone');
    await page.waitForFunction(() => document.querySelector('.demo-still')?.naturalWidth > 0);
    if (width !== 1024) await page.locator('#p-s1').screenshot({ path: join(shots, `049-${width}-${lang}.png`) });
    await page.locator('.demo-play').click();
    await page.waitForFunction(() => { const v = document.querySelector('.landing-demo video'); return !v.paused && v.currentTime > 0; });
    assert(await page.locator('.demo-play').count() === 0, 'play control yields to native controls');
    await video.evaluate((v) => v.pause());
    const anatomy = await page.locator('.anat').evaluate((figure) => {
      const failures = [], parts = [...figure.querySelectorAll('.invite-part')];
      const address = parts[1].title, secret = parts[2].title;
      if (!/^tc[A-Za-z0-9_-]{98}$/.test(address) || !/^[A-Za-z0-9_-]{43}$/.test(secret)) failures.push('fake invite shape');
      const edge = window.innerWidth < 900 ? 16 : 22;
      if (parts[1].innerText !== `${address.slice(0, edge)}…${address.slice(-edge)}` || parts[2].innerText !== `${secret.slice(0, 8)}…${secret.slice(-8)}`) failures.push('middle abbreviation');
      if (parts[1].innerText.length <= parts[2].innerText.length) failures.push('address must read longer than key');
      const bounds = figure.getBoundingClientRect();
      for (const part of parts) {
        const row = part.parentElement, box = row.getBoundingClientRect(), value = part.getBoundingClientRect();
        const label = row.querySelector('.n').getBoundingClientRect();
        if (value.left < box.left - 1 || value.right > box.right + 1 || box.right > bounds.right + 1) failures.push('part outside its row');
        if (label.left < bounds.left - 1 || label.right > bounds.right + 1) failures.push('label outside figure');
        // The unchanged version label is wider than ic1 and sits above it, aligned left.
        const aligned = window.innerWidth < 900 || row.classList.contains('v') ? Math.abs(label.left - value.left) < 1 : Math.abs(label.left + label.width / 2 - (box.left + box.width / 2)) < 1;
        if (!aligned || (window.innerWidth < 900 && (label.top < box.top || label.bottom > box.bottom))) failures.push('label does not belong to its part');
        const bracket = window.getComputedStyle(row, '::after');
        if (window.innerWidth >= 900 && (parseFloat(bracket.left) !== 0 || parseFloat(bracket.right) !== 0 || Math.abs(value.width - box.width) > 1)) failures.push('bracket does not span its part');
      }
      const invite = parts.map((p) => p.innerText).join('.');
      if (!document.querySelector('.code').innerText.startsWith(`$ infercat connect ${invite}\n`)) failures.push('transcript differs from figure');
      return failures;
    });
    assert(!anatomy.length, `invite anatomy: ${anatomy.join(', ')}`);
    if (width !== 1024) await page.locator('#p-s2').screenshot({ path: join(shots, `052-${width}-${lang}.png`) });
    const keys = await checkCopy(page, lang, assert);
    for (const key of ['ml_label', 'ml_ph', 'ml_btn', 'ml_note']) assert(keys.includes(key), `form copy coverage ${key}`);
    const other = lang === 'en' ? 'zh' : 'en';
    await page.locator(`.language-links a[lang="${other === 'zh' ? 'zh-Hans' : 'en'}"]`).last().click();
    await checkCopy(page, other, assert);
    assert(await video.getAttribute('src') === (other === 'zh' ? '/demo.zh.mp4' : '/demo.mp4'), 'video follows language toggle');
    await page.reload();
    assert(await page.locator('.landing').evaluate((e, other) => e.classList.contains(other), other), 'language choice not remembered');
    await page.locator('.node.n-r').click();
    assert(await page.locator('#p-pop-r').isVisible(), 'relay popover');
    await page.keyboard.press('Escape');
    assert(!(await page.locator('#p-pop-r').isVisible()), 'escape closes popover');
    await page.locator('button.direct').click();
    assert(await page.locator('button.direct').getAttribute('aria-checked') === 'true', 'direct switch');
    await page.keyboard.press('ArrowLeft');
    assert(await page.locator('button.relayed').getAttribute('aria-checked') === 'true', 'path keyboard switch');
    await page.locator('.seg.s').click();
    assert(await page.locator('#p-tt-s').isVisible(), 'invite secret details');
    await page.keyboard.press('Escape');
    await page.locator('input[name="email"]').fill('not-an-email');
    await page.locator('.ml button').click();
    assert(await page.locator('.ml .form-error').isVisible(), 'invalid email note');
    assert((await checkCopy(page, other, assert)).includes('ml_invalid'), 'invalid copy coverage');
    await page.locator('input[name="email"]').fill('friend@example.com');
    for (const status of [503, 429]) {
      await page.route('**/signup', (route) => route.fulfill({ status, contentType: 'application/json', body: '{"ok":false}' }));
      await page.locator('.ml button').click();
      await page.waitForFunction(() => !document.querySelector('.ml button').disabled);
      assert(await page.locator('.ml .form-error').isVisible(), `failure note ${status}`);
      assert(await page.locator('.ml .ok').count() === 0, `no false thanks for ${status}`);
      assert((await checkCopy(page, other, assert)).includes(status === 429 ? 'ml_limited' : 'ml_failed'), 'failure copy coverage');
      await page.unroute('**/signup');
    }
    let posted;
    await page.route('**/signup', (route) => { posted = route.request().postDataJSON(); return route.fulfill({ status: 200, contentType: 'application/json', body: '{"ok":true}' }); });
    await page.locator('input[name="email"]').fill('friend@example.com');
    await page.locator('.ml button').click();
    await page.locator('.ml .ok').waitFor();
    const thanks = await checkCopy(page, other, assert);
    assert(thanks.includes('ml_ok_t') && thanks.includes('ml_ok_b'), 'thanks copy coverage');
    assert(posted?.email === 'friend@example.com' && posted.lang === other && posted.from === 'landing-roadmap' && typeof posted.ts === 'string', 'signup body');
    assert(!foreign.length, `third-party requests: ${foreign}`);
    assert(!errors.length, `page errors: ${errors}`);
    await context.close();
    checked++;
    console.log(`  ${label}: PASS — layout, labels, language, controls, signup, same-origin`);
  }
  console.log(`049 video: ${checked} passed / 0 failed / 0 skipped of ${checked}`);
  console.log(`052 anatomy: ${checked} passed / 0 failed / 0 skipped of ${checked}`);
  console.log(`043 landing: ${checked} passed / 0 failed / 0 skipped of ${checked}`);
}
