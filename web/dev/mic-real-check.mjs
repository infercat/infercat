import { chromium, firefox } from 'playwright';
for (const [name, type] of Object.entries({chrome:chromium, firefox})) {
  const browser = await type.launch(name === 'chrome' ? {channel:'chrome',headless:false,args:['--use-fake-ui-for-media-stream']} : {headless:false,firefoxUserPrefs:{'media.navigator.permission.disabled':true}});
  try {
    const page = await browser.newPage();
    await page.goto('http://127.0.0.1:49101/dev/mic-start.html' + (process.env.MIC_PRODUCT ? '?product' : '')); 
    for (const warm of [false,true]) {
      await page.locator('#warm').setChecked(warm);
      for (let i=0;i<2;i++) {
        await page.evaluate(()=>{window.result=null;}); await page.click('#record');
        try { await page.waitForFunction(()=>window.result, null, {timeout:15000}); }
        catch { console.log(name,warm,i,'permission/device request still pending after 15 s'); break; }
        console.log(name,warm,i,JSON.stringify(await page.evaluate(()=>window.result)));
        if(await page.evaluate(()=>!!window.result.error))break;
      }
      await page.click('#release');
    }
  } finally {await browser.close();}
}
