// Run from make demo. All transient keys, transcripts and uncaptioned captures stay outside git.
// Prerequisites: brew install vhs jq; brew install --cask font-ibm-plex-mono; pnpm install in web.
import { spawn, spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '../../../web/node_modules/playwright/index.mjs';

const tapes = dirname(fileURLToPath(import.meta.url));
const root = resolve(tapes, '../../..');
const media = join(root, 'docs/media');
const dir = mkdtempSync('/tmp/icdemo-'); // Short enough for the Go host's Unix socket.
const env = { ...process.env, PATH: `${dir}:${process.env.PATH}` };
const captions = [
  'One binary in front of the model you already run.',
  'A friend opens the link. No account, no install.',
  'Or any OpenAI-compatible client, over the same tunnel.',
];
console.log(`demo: working directory ${dir}`);

async function run(cmd, args, options = {}) {
  await new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { cwd: dir, env, stdio: 'inherit', ...options });
    child.once('error', reject);
    child.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`${cmd}: ${signal || code}`)));
  });
}
function probe(file) {
  const r = spawnSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`ffprobe failed: ${r.stderr}`);
  return Number(r.stdout.trim());
}
async function freePort() {
  const s = createServer();
  await new Promise((resolve, reject) => { s.once('error', reject); s.listen(0, '127.0.0.1', resolve); });
  const port = s.address().port;
  await new Promise((resolve) => s.close(resolve));
  return port;
}
async function captionFrames() {
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 80 } });
    // VHS resolves its font through the same system font collection; fail instead of substituting.
    await page.evaluate(() => new FontFace('demo-mono', 'local("IBM Plex Mono")').load());
    const font = readFileSync(join(root, 'web/public/fonts/archivo-latin-var.woff2')).toString('base64');
    for (const [i, caption] of captions.entries()) {
      await page.setContent(`<style>@font-face{font-family:Archivo;src:url(data:font/woff2;base64,${font})}*{box-sizing:border-box}body{margin:0;background:#fff;color:#0a0a0a;border-top:1px solid #0a0a0a;height:80px;display:flex;align-items:center;padding:0 32px;font:500 30px Archivo}</style><body>${caption}</body>`);
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({ path: join(dir, `caption-${i}.png`) });
    }
  } finally { await browser.close(); }
}
function cleanup() {
  // Revoke even when recording or encoding fails. Never leave a usable key in a failed capture.
  const keys = join(dir, 'host/keys.json');
  if (existsSync(keys)) {
    for (const key of JSON.parse(readFileSync(keys, 'utf8')).keys) {
      if (key.status !== 'revoked') {
        const r = spawnSync(join(dir, 'infercat'), ['--data-dir', join(dir, 'host'), 'keys', 'revoke', key.id, '--yes'], { stdio: 'inherit' });
        if (r.status !== 0) throw new Error(`could not revoke recording key ${key.id}; data at ${dir}`);
      }
    }
  }
  for (const name of ['friend', 'host']) {
    const pid = join(dir, `${name}.pid`);
    if (!existsSync(pid)) continue;
    try { process.kill(Number(readFileSync(pid, 'utf8').trim()), 'SIGTERM'); }
    catch (e) { if (e.code !== 'ESRCH') throw e; }
  }
}
let cleaned = false;
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => { cleanup(); process.exit(1); });
try {
  for (const cmd of ['vhs', 'ffmpeg', 'ffprobe', 'go', 'jq']) {
    const r = spawnSync('which', [cmd], { env, stdio: 'ignore' });
    if (r.status !== 0) throw new Error(`missing ${cmd}; install the recording prerequisites`);
  }
  await captionFrames();
  await run('go', ['build', '-o', join(dir, 'infercat'), './cmd/infercat'], { cwd: root });
  for (const file of ['style.tape', 'host.tape']) copyFileSync(join(tapes, file), join(dir, file));
  // A request file keeps the terminal's curl command legible; it contains only the real API body.
  writeFileSync(join(dir, 'question.json'), JSON.stringify({ messages: [{ role: 'user', content: 'Say hello in one sentence.' }], stream: false }) + '\n');
  await run('vhs', ['host.tape']);
  const transcript = readFileSync(join(dir, 'host.ascii'), 'utf8');
  const invite = transcript.replace(/\r?\n/g, '').match(/ic1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}/)?.[0];
  if (!invite) throw new Error('host tape did not produce a real invite');
  await run(process.execPath, [join(root, 'web/dev/launch-check.mjs'), '--demo-dir', dir], {
    env: { ...env, APP: 'https://infercat.ai', INVITE: invite },
  });
  const friend = readFileSync(join(tapes, 'friend.tape'), 'utf8')
    .replaceAll('@INVITE@', invite).replaceAll('@LISTEN@', String(await freePort()));
  writeFileSync(join(dir, 'friend.tape'), friend);
  await run('vhs', ['friend.tape']);
  const native = readFileSync(join(dir, 'friend.ascii'), 'utf8');
  if (readFileSync(join(dir, 'response.status'), 'utf8').trim() !== '0') throw new Error('native curl/jq did not produce an answer');
  console.log(`demo: native ${native.match(/path[^\r\n]+/)?.[0] || 'path unavailable'}`);
  cleanup();
  cleaned = true;

  const inputs = ['host.mp4', 'browser.webm', 'friend.mp4'];
  const durations = inputs.map((file) => probe(join(dir, file)));
  const total = durations.reduce((a, b) => a + b, 0);
  console.log(`demo: scenes ${durations.map((n) => n.toFixed(2)).join(' / ')} s; total ${total.toFixed(2)} s`);
  if (!Number.isFinite(total) || total > 60) throw new Error('demo exceeds 60 s; shorten the tapes, never accelerate real inference');
  for (const [i, input] of inputs.entries()) {
    await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', input, '-i', `caption-${i}.png`, '-filter_complex',
      '[0:v]pad=1280:800:(ow-iw)/2:0:white,setsar=1[scene];[scene][1:v]overlay=0:720:shortest=0,fps=25,format=yuv420p[out]',
      '-map', '[out]', '-an', '-c:v', 'libx264', '-crf', '24', '-preset', 'slow',
      // Uniform metadata prevents GIF palette buffering from resetting at a scene boundary.
      '-color_range', 'tv', '-colorspace', 'bt470bg', `scene-${i}.mp4`]);
  }
  writeFileSync(join(dir, 'scenes.txt'), inputs.map((_, i) => `file 'scene-${i}.mp4'`).join('\n') + '\n');
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', 'scenes.txt', '-c', 'copy', '-movflags', '+faststart', 'demo.mp4']);
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', 'demo.mp4', '-vf',
    'fps=10,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle', '-loop', '0', 'demo.gif']);
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', 'browser-first-token.png', '-i', 'caption-1.png', '-filter_complex',
    '[0:v]pad=1280:800:(ow-iw)/2:0:white[scene];[scene][1:v]overlay=0:720', '-frames:v', '1', 'demo-poster.png']);
  for (const [file, limit] of [['demo.mp4', 8_000_000], ['demo.gif', 4_000_000], ['demo-poster.png', Infinity]]) {
    const bytes = statSync(join(dir, file)).size;
    console.log(`demo: ${file} ${bytes} bytes`);
    if (bytes > limit) throw new Error(`${file} exceeds its ticket budget`);
    if (file !== 'demo-poster.png') {
      const duration = probe(join(dir, file));
      if (Math.abs(duration - total) > 0.2) throw new Error(`${file} lost scene time: ${duration} vs ${total}`);
      console.log(`demo: ${file} ${duration.toFixed(2)} s, all three scenes retained`);
    }
  }
  mkdirSync(media, { recursive: true });
  for (const file of ['demo.mp4', 'demo.gif', 'demo-poster.png']) copyFileSync(join(dir, file), join(media, file));
  console.log(`demo: OK; recording keys revoked; raw evidence retained at ${dir}`);
} finally {
  if (!cleaned) cleanup();
}
