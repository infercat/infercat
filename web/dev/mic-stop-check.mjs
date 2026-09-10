import { chromium,firefox } from 'playwright';
import { Buffer } from 'node:buffer';
import { readFileSync,writeFileSync,mkdirSync } from 'node:fs';
const invite=JSON.parse(readFileSync('/tmp/infercat-founder-voice-invite.json','utf8')).invite;
const out='/tmp/infercat-102-blobs';mkdirSync(out,{recursive:true,mode:0o700});
for(const [name,type]of Object.entries({chrome:chromium,firefox})){
 const browser=await type.launch(name==='chrome'?{channel:'chrome',headless:false,ignoreDefaultArgs:['--mute-audio'],args:['--use-fake-ui-for-media-stream']}:{headless:false,firefoxUserPrefs:{'media.navigator.permission.disabled':true}});
 try{for(const delay of [1000,1200,5000]){
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:49102/dev/mic-stop.html${process.env.MIC_BASE?'?base':''}#${invite}`);
  await page.locator('#fixture').setInputFiles('/tmp/infercat-078-input.wav');
  await page.locator('#mic').click();await page.waitForTimeout(delay);
  const before=await page.evaluate(()=>({state:window.recorder.state.kind,native:window.nativeRecorder?.state,disabled:document.querySelector('#mic').disabled}));
  const box=await page.locator('#mic').boundingBox();await page.mouse.click(box.x+box.width/2,box.y+box.height/2);
  await page.waitForTimeout(2200);const after=await page.evaluate(()=>window.recorder.state.kind);
  console.log(JSON.stringify({name,delay,base:!!process.env.MIC_BASE,before,after}));
  if(after==='recording'||after==='requesting')await page.locator('#stop').click();
  try{await page.waitForFunction(()=>window.result && ('transcript' in window.result || 'transcriptionError' in window.result),null,{timeout:15000});
   const result=await page.evaluate(()=>window.result);console.log(JSON.stringify(result));
   const bytes=await page.evaluate(async()=>Array.from(new Uint8Array(await window.lastBlob.arrayBuffer())));
   writeFileSync(`${out}/${name}-${process.env.MIC_BASE?'before':'after'}-${delay}.webm`,Buffer.from(bytes),{mode:0o600});
  }catch{console.log(name,delay,'no completed transcript',await page.locator('#state').innerText());}
  await page.locator('#release').click();await page.close();
 }}finally{await browser.close();}
}
