// Real Chrome decode through two isolated hosts; raw invites remain outside the repo.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { chromium } from 'playwright';
const invites=JSON.parse(readFileSync('/tmp/infercat-123-invites.json','utf8'));
const browser=await chromium.launch({channel:'chrome',headless:true});
try{
 const context=await browser.newContext();await context.grantPermissions(['local-network-access'],{origin:'http://127.0.0.1:49123'});
 for(const kind of ['wav','mp3']){
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)));
  await page.goto(`http://127.0.0.1:49123/dev/player-proof.html?kind=${kind}#${invites[kind]}`);
  await page.waitForFunction(()=>window.result?.ready);
  for(let run=0;run<2;run++){
   await page.locator('#listen').click();await page.waitForFunction(()=>window.result.ended||window.result.error,null,{timeout:45000});
   const result=await page.evaluate(()=>window.result);assert.ok(result.ended&&!result.error,JSON.stringify(result));assert.equal(result.requests,1);
   assert.deepEqual(errors,[]);console.log(JSON.stringify({run,...result}));
  }
  await page.screenshot({path:`/tmp/infercat-123-chrome-${kind}.png`});await page.close();
 }
}finally{await browser.close();}
console.log('Real Chrome: 4 passed / 0 failed / 0 skipped');
