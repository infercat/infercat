import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const browser=await chromium.launch(), out='/private/tmp/infercat-144b-privacy';mkdirSync(out,{recursive:true});
const invite=`ic1.tcIMAGEproofaddressIMAGEproofaddress.${'D'.repeat(43)}`;
const copy={en:'Pictures you ask for, and the words you asked with, stay on the host under your key for 7 days.',zh:'你请求生成的图片和所用的文字，会按你的密钥在主机上保留 7 天。'};
let checks=0;const errors=[];
try {
 for(const width of [390,1280]) for(const lang of ['en','zh']) {
  const context=await browser.newContext({viewport:{width,height:900},locale:lang==='zh'?'zh-CN':'en-US'});
  await context.addInitScript((lang)=>window.localStorage.setItem('bn.language',JSON.stringify(lang)),lang);
  const page=await context.newPage();page.on('pageerror',(e)=>errors.push(String(e)));
  await page.goto(`http://127.0.0.1:49144/?fake&imageJobs&logPrompts&connectMs=20&invite=${encodeURIComponent(invite)}&autoconnect`);
  await page.locator('.failure .primary').waitFor();
  assert.equal(await page.locator('p:visible').filter({hasText:copy[lang]}).count(),1);checks++;
  await page.screenshot({path:`${out}/connect-logging-${width}-${lang}.png`});
  await page.locator('.failure .primary').click();await page.locator('.empty').waitFor();
  assert.ok((await page.locator('.empty').innerText()).includes(copy[lang]));checks++;
  await page.screenshot({path:`${out}/empty-${width}-${lang}.png`});
  await page.locator('.meters').click();await page.locator('.sheet').waitFor();assert.ok((await page.locator('.sheet').innerText()).includes(copy[lang]));checks++;
  await page.locator('.sheet p').filter({hasText:copy[lang]}).scrollIntoViewIfNeeded();await page.screenshot({path:`${out}/limits-${width}-${lang}.png`});
  await page.locator('.sheet .primary').click();await page.getByRole('button',{name:lang==='zh'?'设置':'Settings',exact:true}).click();await page.locator('.sheet').waitFor();assert.ok((await page.locator('.sheet').innerText()).includes(copy[lang]));checks++;
  await context.close();
 }
 assert.deepEqual(errors,[]);console.log(JSON.stringify({checks,errors,out}));
}finally{await browser.close();}
