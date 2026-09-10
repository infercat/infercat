// Native media APIs with browser fake devices; timestamps are ms from the click.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.VOICE_CHECK_URL ?? 'http://127.0.0.1:49199';
// Chromium's default fake source begins with silence; use a tone from sample zero.
const dir = mkdtempSync(join(tmpdir(), 'voice-start-'));
const tone = Buffer.alloc(44 + 32000);
tone.write('RIFF'); tone.writeUInt32LE(tone.length - 8, 4); tone.write('WAVEfmt ', 8);
tone.writeUInt32LE(16, 16); tone.writeUInt16LE(1, 20); tone.writeUInt16LE(1, 22);
tone.writeUInt32LE(16000, 24); tone.writeUInt32LE(32000, 28); tone.writeUInt16LE(2, 32);
tone.writeUInt16LE(16, 34); tone.write('data', 36); tone.writeUInt32LE(32000, 40);
for (let i = 0; i < 16000; i++) tone.writeInt16LE(Math.round(16000 * Math.cos(i * 2 * Math.PI * 440 / 16000)), 44 + 2 * i);
const file = join(dir, 'tone.wav'); writeFileSync(file, tone);
try {
for (const [name, type] of Object.entries({ chromium, firefox, webkit })) {
  const browser = await type.launch({headless:true, ...(name === 'chromium' ? {args:['--use-fake-device-for-media-stream','--use-fake-ui-for-media-stream', `--use-file-for-fake-audio-capture=${file}`]} : name === 'firefox' ? {firefoxUserPrefs:{'media.navigator.streams.fake':true,'media.navigator.permission.disabled':true}} : {})});
  try {
    const page = await browser.newPage();
    await page.route('**/voice-start-test', r => r.fulfill({contentType:'text/html',body:'<button id="start">Record</button><svg><path id="wave" /></svg>'}));
    await page.goto(base+'/voice-start-test');
    await page.evaluate(async ({synthetic, module}) => {
      const {VoiceRecorder}=await import(module);
      window.times={}; const mark=k=>{window.times[k]??=Math.round((performance.now()-window.tap)*10)/10;};
      const SourceContext = window.AudioContext;
      const gum = synthetic ? async () => {
        const context = new SourceContext(); await context.resume();
        const oscillator = context.createOscillator();
        const output = context.createMediaStreamDestination(); oscillator.connect(output); oscillator.start();
        window.closeSource = () => { oscillator.stop(); void context.close(); };
        return output.stream;
      } : window.navigator.mediaDevices.getUserMedia.bind(window.navigator.mediaDevices);
      Object.defineProperty(window.navigator, 'mediaDevices', {configurable:true, value:{getUserMedia:async o=>{const s=await gum(o); mark('stream'); window.stream=s; return s;}}});
      const NativeContext=window.AudioContext;
      window.AudioContext=class extends NativeContext { constructor(...a){super(...a); this.addEventListener('statechange',()=>{if(this.state==='running')mark('context');}); const create=this.createAnalyser.bind(this); this.createAnalyser=()=>{const n=create(); const read=n.getFloatTimeDomainData.bind(n); n.getFloatTimeDomainData=a=>{read(a); mark('analyser'); if(a.some(x=>x!==0))mark('signal');}; return n;};}};
      const NativeRecorder=window.MediaRecorder;
      window.MediaRecorder=class extends NativeRecorder {start(...a){mark('captureStart');this.addEventListener('dataavailable',()=>mark('data'));super.start(...a);}};
      window.recorder=new VoiceRecorder(async()=>'', s=>{if(s.kind==='recording'){mark('recordingUI');window.requestAnimationFrame(()=>mark('drawnFrame'));document.querySelector('#wave').setAttribute('d',s.waveform.map((x,i)=>`M${i} 0v${x}`).join(' '));if(s.waveform.some(x=>x>0))window.requestAnimationFrame(()=>mark('drawnSignal'));}});
      document.querySelector('#start').onclick=()=>{window.times={};window.tap=performance.now();void window.recorder.start();};
    }, {synthetic: name === 'webkit', module: process.env.VOICE_START_MODULE ?? '/src/voice.ts'});
    for(let i=0;i<2;i++){
      await page.click('#start'); await page.waitForTimeout(2000);
      console.log(name,browser.version(),name === 'webkit' ? 'oscillator-stream' : 'native-fake-device',i===0?'first':'repeat',JSON.stringify(await page.evaluate(()=>({times:window.times,state:window.recorder.state.kind}))));
      assert.equal(await page.evaluate(()=>window.recorder.state.kind), 'recording');
      assert.equal(await page.evaluate(()=>window.times.captureStart <= window.times.recordingUI), true);
      await page.evaluate(()=>{window.recorder.release?.();window.recorder.cancel();window.closeSource?.();});
      if(await page.evaluate(()=>window.stream?.getTracks().some(t=>t.readyState!=='ended')))throw Error('microphone retained');
    }
  }finally{await browser.close();}
}
} finally { rmSync(dir, { recursive: true, force: true }); }
