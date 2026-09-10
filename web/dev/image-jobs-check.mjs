import { Buffer } from 'node:buffer';
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdirSync } from 'node:fs';
const browser = await chromium.launch();
const out = process.env.JOBS_OUT ?? '/private/tmp/infercat-144b', base = process.env.JOBS_URL ?? 'http://127.0.0.1:49144';
mkdirSync(out, { recursive: true });
let checks = 0; const errors = [], ok = (v, text) => { assert.ok(v, text); checks++; };
const invite = `ic1.tcIMAGEproofaddressIMAGEproofaddress.${'D'.repeat(43)}`;
async function connect(width=1280, lang='en', extra='') {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 }, locale: lang === 'zh' ? 'zh-CN' : 'en-US', isMobile: width === 390, hasTouch: width === 390, acceptDownloads: true });
  await context.addInitScript((l) => window.localStorage.setItem('bn.language', JSON.stringify(l)),lang);
  const page = await context.newPage(); page.on('pageerror', (e) => errors.push(String(e)));
  await page.goto(`${base}/?fake&imageJobs&transcriptions&connectMs=20&invite=${encodeURIComponent(invite)}&autoconnect${extra}`);
  await page.locator('.make').waitFor(); await page.waitForFunction(() => !document.querySelector('.composer textarea').disabled);
  await page.evaluate(() => { const c = document.createElement('canvas'); c.width=768;c.height=512; const x=c.getContext('2d');x.fillStyle='#f7f6f2';x.fillRect(0,0,768,512);x.fillStyle='#1f3fdd';x.fillRect(600,72,72,72);x.fillStyle='#111';x.font='bold 44px sans-serif';x.fillText('BORROWED',96,120);x.fillText('COMPUTE',96,164);x.fillRect(160,316,448,28);x.fillRect(128,352,512,28);x.fillRect(96,388,576,28);window.__fakeImages.control.png=c.toDataURL('image/png').split(',')[1]; });
  return { page, context };
}
async function shot(page,name) { await page.evaluate(() => document.fonts.ready); ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),`${name} overflow`);await page.screenshot({path:`${out}/${name}.png`}); }
async function finish(page,id,patch={}) { await page.evaluate(({id,patch})=>{ window.__fakeImages.update(id,{state:'done',output:{url:'https://untrusted.invalid/never-fetch',mime:'image/png',w:768,h:512,bytes:8192,expiresAt:new Date(Date.now()+604800000).toISOString()},...patch});}, {id,patch}); }
try {
 for (const width of [1280,390]) for (const lang of ['en','zh']) {
  const {page,context}=await connect(width,lang), label=`${width}-${lang}`;
  await page.locator('.make').click();await page.locator('.composer textarea').fill('A paper cat on a stack of GPUs.'); await shot(page,`ask-${label}`);
  const field=await page.locator('.composer textarea').boundingBox(), square=await page.locator('.make').boundingBox();ok(width===390 ? field.y<square.y : Math.abs(field.y-square.y)<5,'composer layout');
  await page.locator('.composer textarea').fill('A paper cat on a stack of GPUs.\n\nA paper cat by a window.\n\nA paper cat on a bridge.');await shot(page,`planting-${label}`);await page.locator('.composer .primary').click();await page.locator('.row.run[data-run-state]').first().waitFor();
  await page.waitForFunction(() => document.querySelector('.make')?.getAttribute('aria-pressed') === 'false');ok(await page.locator('.row.run').count()===3,'one row per image');ok(await page.locator('.make').getAttribute('aria-pressed')==='false','mode ends');
  await page.locator('.row.run[data-run-state="running"] .spoken button').click();await page.waitForFunction(()=>document.querySelector('.row.run .spoken')?.textContent.includes('after this image')||document.querySelector('.row.run .spoken')?.textContent.includes('这张完成'));await shot(page,`cancelling-${label}`);
  await finish(page,'image-1');await page.locator('.row.run .shot img').waitFor();
  await page.evaluate(()=>window.__fakeImages.update('image-2',{state:'running',started:new Date(Date.now()-9000).toISOString()}));await page.waitForFunction(()=>document.querySelectorAll('.row.run[data-run-state="running"]').length===1);await shot(page,`planted-${label}`);
  await page.locator('.row.run .shot').click();await page.locator('.sheet.image').waitFor();await shot(page,`image-sheet-${label}`);
  const download=page.waitForEvent('download');await page.locator('.sheet.image .sheet-actions button').first().click();await download;
  ok(await page.evaluate(()=>window.__fakeImages.calls.some((c)=>c.path.endsWith('?download=1'))),'Save authenticates attachment route');await page.keyboard.press('Escape');
  if(width===390) await page.locator('.hamburger').click();
  await page.locator('.conv.harvest button').click();await page.locator('.sheet.harvest').waitFor();await page.locator('.sheet.harvest .shot img').waitFor();if(width===390) await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=0);await shot(page,`images-${label}`);
  await page.locator('.output .actions button').last().click();await page.waitForFunction(()=>document.querySelector('.output .line')?.textContent.includes('no longer')||document.querySelector('.output .line')?.textContent.includes('已不存在'));
  await page.locator('.output .actions button').first().click();ok(await page.locator('.make').getAttribute('aria-pressed')==='true','Edit prompt mode');ok(await page.locator('.composer textarea').inputValue()==='A paper cat on a stack of GPUs.','Edit prompt text');
  await page.evaluate(()=>window.__fakeImages.update('image-2',{state:'failed',reason:'Engine rejected <script>raw diagnostic</script>'}));await page.locator('.row.run[data-run-state="failed"]').waitFor();await shot(page,`failed-${label}`);
  await context.close();
 }
 {
  const {page,context}=await connect();
  await page.locator('.make').click();await page.locator('.composer textarea').fill('Keep this image prompt.');
  await page.locator('input[type=file]').setInputFiles({name:'notes.txt',mimeType:'text/plain',buffer:Buffer.from('Attached text stays here.')});
  await page.locator('.attachment-reading').waitFor({state:'hidden'});await page.locator('.composer .primary').click();
  await page.getByText('Images are made from text. Remove the attachments, or switch back to chat.').waitFor();
  ok(await page.locator('.composer textarea').inputValue()==='Keep this image prompt.','attachment refusal retains draft');
  ok(await page.evaluate(()=>window.__fakeImages.calls.filter((c)=>c.method==='POST').length)===0,'attachment refusal sends nothing');
  ok(await page.locator('.attached .chip').count()>0,'attachment retained');await context.close();
 }
 for(const race of [false,true]) {
  const {page,context}=await connect();await page.evaluate((race)=>{window.__fakeImages.control.cap=race?8:1;window.__fakeImages.control.refuse=race;},race);
  await page.locator('.make').click();await page.locator('.composer textarea').fill('Keep on refusal.\n\nAnother prompt.');await page.locator('.composer .primary').click();
  await page.getByText(/keeps .* of your images in line at most/).waitFor();
  ok(await page.locator('.composer textarea').inputValue()==='Keep on refusal.\n\nAnother prompt.','cap draft retained');ok(await page.locator('.make').getAttribute('aria-pressed')==='true','cap mode retained');ok(await page.locator('.row.run').count()===0,'cap no ended row');
  ok(await page.evaluate(()=>window.__fakeImages.calls.filter((c)=>c.method==='POST').length)===(race?1:0),'cap preflight versus atomic refusal');await context.close();
 }
 {
  const {page,context}=await connect();await page.evaluate(()=>window.__fakeImages.control.lose=true);
  await page.locator('.make').click();await page.locator('.composer textarea').fill('One image.\n\nAnother image.');await page.locator('.composer .primary').click();
  await page.waitForFunction(()=>document.querySelectorAll('.row.run[data-run-state]').length===2 && document.querySelector('.make').getAttribute('aria-pressed')==='false');
  ok(await page.evaluate(()=>window.__fakeImages.calls.filter((c)=>c.method==='POST').length)===1,'ambiguous batch never replayed');
  await page.locator('.row.run[data-run-state="queued"] .spoken button').click();await page.locator('.row.run[data-run-state="cancelled"]').waitFor();ok((await page.locator('.row.run[data-run-state="cancelled"]').innerText()).includes('cancelled · not started'),'queued cancellation immediate');
  await page.reload();await page.waitForFunction(()=>document.querySelectorAll('.row.run[data-run-state]').length===2);ok(await page.locator('.row.run').count()===2,'reload discovers without duplicate rows');await context.close();
 }
 {
  const {page,context}=await connect();await page.evaluate(()=>window.__fakeImages.control.budget=true);const originalTitle=await page.locator('.conv:not(.harvest) .conv-open').first().innerText();
  await page.locator('.make').click();await page.locator('.composer textarea').fill('Keep the daily-cap draft.');await page.locator('.composer .primary').click();await page.getByText("The host's images for today are used up. Try again tomorrow.").waitFor();
  ok(await page.locator('.row.run').count()===0,'daily cap creates no ended rows');ok(await page.locator('.conv:not(.harvest) .conv-open').first().innerText()===originalTitle,'daily cap restores provisional title');ok(await page.locator('.composer textarea').inputValue()==='Keep the daily-cap draft.','daily cap retains draft');ok(await page.locator('.make').getAttribute('aria-pressed')==='true','daily cap retains mode');await context.close();
 }
 {
  const {page,context}=await connect();await page.evaluate(()=>{window.__fakeImages.control.cap=-1;window.__fakeImages.control.daily=-1;});
  await page.locator('.make').click();await page.locator('.composer textarea').fill(Array.from({length:9},(_,i)=>`Image ${i}`).join('\n\n'));await page.locator('.composer .primary').click();
  await page.waitForFunction(()=>document.querySelectorAll('.row.run[data-run-state]').length===9);ok(await page.locator('.row.run').count()===9,'negative cap admits beyond default');ok(!(await page.locator('.meters').innerText()).includes('/-1'),'negative daily has no bogus denominator');await context.close();
 }
 ok(errors.length===0,JSON.stringify(errors));console.log(JSON.stringify({checks,errors,out}));
} finally { await browser.close(); }
