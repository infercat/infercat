// Run from make demo. All transient keys, transcripts and uncaptioned captures stay outside git.
// Prerequisites: brew install vhs jq; brew install --cask font-ibm-plex-mono; pnpm install in web.
import { spawn, spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { renderOverlays } from './overlays.mjs';

const tapes = dirname(fileURLToPath(import.meta.url));
const root = resolve(tapes, '../../..');
const media = join(root, 'docs/media');
const dir = mkdtempSync('/tmp/icdemo-'); // Short enough for the Go host's Unix socket.
const env = { ...process.env, PATH: `${dir}:${process.env.PATH}` };
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
// The tape's two-second hold ends at the next changed frame. Derive it from this take.
function terminalBoundary(file) {
  const r = spawnSync('ffmpeg', ['-hide_banner', '-i', file, '-vf', "select='gt(scene,0.00005)',showinfo", '-an', '-f', 'null', '-'], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(`cannot measure ${file}: ${r.stderr}`);
  const times = [...r.stderr.matchAll(/pts_time:([\d.]+)/g)].map((m) => Number(m[1]));
  for (let i = 1; i < times.length; i++) {
    const gap = times[i] - times[i - 1];
    if (gap >= 1.9 && gap <= 2.15) return times[i];
  }
  throw new Error(`${file}: no two-second output hold found; inspect this take`);
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
  await renderOverlays(dir);
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
  const total = durations.reduce((a, b) => a + b, 0) + 1.6 + 2.5;
  const boundaries = [terminalBoundary(join(dir, inputs[0])), JSON.parse(readFileSync(join(dir, 'browser.marks.json'), 'utf8')).sendSeconds, terminalBoundary(join(dir, inputs[2]))];
  if (boundaries.some((t, i) => !Number.isFinite(t) || t <= 0 || t >= durations[i])) throw new Error('caption boundary outside its clip');
  console.log(`demo: measured label boundaries ${boundaries.map((t) => t.toFixed(3)).join(' / ')} s (clip-local)`);
  console.log(`demo: scenes ${durations.map((n) => n.toFixed(2)).join(' / ')} s; total ${total.toFixed(2)} s`);
  if (!Number.isFinite(total) || total > 60) throw new Error('demo exceeds 60 s; shorten the tapes, never accelerate real inference');
  for (const [i, input] of inputs.entries()) {
    await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', input, '-i', `step-0${i * 2 + 1}.png`, '-i', `step-0${i * 2 + 2}.png`, '-filter_complex',
      `[0:v]pad=1280:800:(ow-iw)/2:0:white,setsar=1[scene];[scene][1:v]overlay=0:0:enable='lt(t,${boundaries[i]})'[a];[a][2:v]overlay=0:0:enable='gte(t,${boundaries[i]})',fps=25,format=yuv420p[out]`,
      '-map', '[out]', '-an', '-c:v', 'libx264', '-crf', '24', '-preset', 'slow',
      // Uniform metadata prevents GIF palette buffering from resetting at a scene boundary.
      '-color_range', 'tv', '-colorspace', 'bt470bg', `scene-${i}.mp4`]);
  }
  for (const [name, seconds] of [['title', 1.6], ['end', 2.5]]) {
    await run('ffmpeg', ['-y', '-loglevel', 'error', '-loop', '1', '-framerate', '25', '-i', `${name}.png`, '-t', String(seconds), '-vf', 'format=yuv420p,setsar=1', '-an', '-c:v', 'libx264', '-crf', '24', '-preset', 'slow', '-color_range', 'tv', '-colorspace', 'bt470bg', `${name}.mp4`]);
  }
  writeFileSync(join(dir, 'scenes.txt'), ['title', 'scene-0', 'scene-1', 'scene-2', 'end'].map((name) => `file '${name}.mp4'`).join('\n') + '\n');
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', 'scenes.txt', '-c', 'copy', '-movflags', '+faststart', 'demo.mp4']);
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', 'demo.mp4', '-vf',
    'fps=10,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle', '-loop', '0', 'demo.gif']);
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', 'browser-first-token.png', '-i', 'step-04.png', '-filter_complex',
    '[0:v]pad=1280:800:(ow-iw)/2:0:white[scene];[scene][1:v]overlay=0:0', '-frames:v', '1', 'demo-poster.png']);
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
