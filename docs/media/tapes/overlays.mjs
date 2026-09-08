// Eight static overlays; CAPTIONS.md is the single source of all displayed copy.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '../../../web/node_modules/playwright/index.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '../../..');
const spec = readFileSync(join(here, 'CAPTIONS.md'), 'utf8');
function copy(row) {
  const line = spec.split('\n').find((s) => s.startsWith(`| ${row} |`) || s.startsWith(`| ${row} (`));
  const text = line?.split('|')[2].match(/`([^`]+)`/)?.[1];
  if (!text) throw new Error(`caption spec missing ${row}`);
  return text;
}
const labels = Array.from({ length: 6 }, (_, i) => {
  const number = String(i + 1).padStart(2, '0');
  const row = spec.split('\n').find((s) => s.startsWith(`| ${number} |`));
  const label = row?.split('|')[7].match(/`([^`]+)`/)?.[1];
  if (!label) throw new Error(`caption spec missing step ${number}`);
  return [number, label];
});
const escape = (s) => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
const mark = readFileSync(join(root, 'docs/brand/mark.svg'), 'utf8').replace(/<style>[\s\S]*?<\/style>/, '');
const font = (file) => readFileSync(join(root, 'web/public/fonts', file)).toString('base64');
const css = `
@font-face{font-family:Archivo;font-weight:100 900;src:url(data:font/woff2;base64,${font('archivo-latin-var.woff2')})}
@font-face{font-family:Plex;font-weight:400;src:url(data:font/woff2;base64,${font('ibm-plex-mono-400-latin.woff2')})}
@font-face{font-family:Plex;font-weight:500;src:url(data:font/woff2;base64,${font('ibm-plex-mono-500-latin.woff2')})}
*{box-sizing:border-box}body{margin:0;background:transparent;color:#0a0a0a;font-family:Archivo;font-kerning:normal;font-variant-ligatures:none}
.ov{position:relative;width:1280px;height:800px}.card{background:#fff}
.bar{position:absolute;left:0;top:720px;width:1280px;height:80px;background:#fff;border-top:1px solid #0a0a0a;display:flex;align-items:center}
.num{position:absolute;left:24px;color:#1f3bff;font:500 39px/1 Plex}
.label{position:absolute;left:80px;font:500 39px/1 Archivo;letter-spacing:-.02em;white-space:nowrap}
.foot{position:absolute;right:24px;color:#5c6068;font:400 20px/1 Plex}
.repo{position:absolute;left:24px;color:#5c6068;font:400 20px/1 Plex}
.mark svg{width:100%;height:100%}.mark .m{fill:currentColor}.mark .s{fill:none;stroke:currentColor;stroke-width:3;stroke-linecap:round}
.title .mark{position:absolute;left:96px;top:300px;width:112px;height:112px}
.word{position:absolute;left:230px;top:294px;font:700 112px/1 Archivo;letter-spacing:-.04em}
.tag{position:absolute;left:96px;white-space:nowrap;letter-spacing:-.015em}
.title .tag{top:452px;font:400 50px/1.2 Archivo}.end .tag{top:496px;font:400 40px/1.2 Archivo}
.end .mark{position:absolute;left:96px;top:216px;width:104px;height:104px}
.url{position:absolute;left:88px;top:344px;color:#1f3bff;font:700 144px/1 Archivo;letter-spacing:-.045em}
.licence{position:absolute;left:96px;top:566px;font:500 28px/1 Plex}
`;
export async function renderOverlays(dir) {
  const footer = `<span class="foot">${escape(copy('footer'))}</span>`;
  const frames = [
    ['title', `<div class="ov card title"><div class="mark">${mark}</div><div class="word">${escape(copy('wordmark'))}</div><div class="tag">${escape(copy('tagline'))}</div><div class="bar">${footer}</div></div>`],
    ...labels.map(([n, text]) => [`step-${n}`, `<div class="ov"><div class="bar"><span class="num">${n}</span><span class="label">${escape(text)}</span>${footer}</div></div>`]),
    ['end', `<div class="ov card end"><div class="mark">${mark}</div><div class="url">${escape(copy('URL'))}</div><div class="tag">${escape(copy('tagline'))}</div><div class="licence">${escape(copy('licence line'))}</div><div class="bar"><span class="repo">${escape(copy('rule + repo'))}</span></div></div>`],
  ];
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, colorScheme: 'light' });
    await page.evaluate(() => new FontFace('demo-mono', 'local("IBM Plex Mono")').load());
    for (const [name, html] of frames) {
      await page.setContent(`<style>${css}</style>${html}`);
      await page.evaluate(() => document.fonts.ready);
      const fit = await page.evaluate(() => {
        const label = document.querySelector('.label')?.getBoundingClientRect();
        const foot = document.querySelector('.foot')?.getBoundingClientRect();
        const tag = document.querySelector('.tag')?.getBoundingClientRect();
        return { fits: (!label || label.right + 16 <= foot.left) && (!tag || tag.right <= 1184), width: label?.width };
      });
      if (!fit.fits) throw new Error(`${name}: caption needs shortening to fit the enlarged type`);
      const png = await page.screenshot({ omitBackground: true, path: join(dir, `${name}.png`) });
      if (name.startsWith('step-')) {
        const alpha = await page.evaluate(async (data) => {
          const img = new Image(); img.src = data; await img.decode();
          const canvas = document.createElement('canvas'); canvas.width = 1280; canvas.height = 800;
          const ctx = canvas.getContext('2d'); ctx.drawImage(img, 0, 0);
          const pixels = ctx.getImageData(0, 0, 1280, 800).data;
          for (let i = 3; i < pixels.length; i += 4) if (pixels[i] !== (i < 1280 * 720 * 4 ? 0 : 255)) return false;
          return true;
        }, `data:image/png;base64,${png.toString('base64')}`);
        if (!alpha) throw new Error(`${name}: overlay intrudes above y720 or leaves the bar transparent`);
      }
      console.log(`overlays: ${name} OK${fit.width ? `, label ${fit.width.toFixed(1)} px` : ''}`);
    }
  } finally { await browser.close(); }
}
