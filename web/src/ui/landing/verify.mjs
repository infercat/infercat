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
        const wanted = attribute ? expected[key] : template.innerHTML;
        const actual = attribute ? el.getAttribute(attribute) : el.innerHTML;
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
  for (const width of [1280, 1024, 390]) for (const scheme of ['light', 'dark']) for (const lang of ['en', 'zh']) {
    const label = `043-${width}-${scheme}-${lang}`;
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, locale: lang === 'zh' ? 'zh-CN' : 'en-US' });
    const page = await context.newPage();
    const errors = [], foreign = [];
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('request', (r) => { if (new URL(r.url()).origin !== new URL(base).origin) foreign.push(r.url()); });
    await page.goto(base);
    await page.locator('.landing').waitFor();
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
    const keys = await checkCopy(page, lang, assert);
    for (const key of ['ml_label', 'ml_ph', 'ml_btn', 'ml_note']) assert(keys.includes(key), `form copy coverage ${key}`);
    const other = lang === 'en' ? 'zh' : 'en';
    await page.locator(`.language-links a[lang="${other === 'zh' ? 'zh-Hans' : 'en'}"]`).last().click();
    await checkCopy(page, other, assert);
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
  console.log(`043 landing: ${checked} passed / 0 failed / 0 skipped of ${checked}`);
}
