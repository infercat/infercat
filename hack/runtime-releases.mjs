import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const files = ['infercat.wasm.gz', 'wasm_exec.js'];
const manifestURL = new URL('../hosting/cloudflare/runtime-releases.json', import.meta.url);
function validate(manifest) {
  if (!Object.keys(manifest).length) throw new Error('No released runtimes listed');
  for (const [version, pair] of Object.entries(manifest)) {
    if (!/^\d+\.\d+\.\d+$/.test(version) || Object.keys(pair).sort().join() !== [...files].sort().join() || files.some(f => !/^[a-f0-9]{64}$/.test(pair[f]))) throw new Error(`Invalid runtime pair: ${version}`);
  }
}
async function checked(url, hash, fetcher, cors = false) {
  const response = await fetcher(url, { signal: AbortSignal.timeout(120000), ...(cors ? { headers: { Origin: 'https://client.example' } } : {}) });
  if (response.status !== 200) throw new Error(`${url}: HTTP ${response.status}`);
  if (cors && response.headers.get('access-control-allow-origin') !== '*') throw new Error(`${url}: missing public CORS`);
  const bytes = Buffer.from(await response.arrayBuffer());
  if (createHash('sha256').update(bytes).digest('hex') !== hash) throw new Error(`${url}: SHA-256 mismatch`);
  return bytes;
}
/** Finish all downloads before changing /v; a failure stops make before publication. */
export async function stageRuntimes(manifest, directory, current, fetcher = fetch) {
  validate(manifest);
  if (!/^\d+\.\d+\.\d+(?:-dev)?$/.test(current)) throw new Error('Invalid current version');
  if (!current.endsWith('-dev') && !manifest[current]) throw new Error(`Released version ${current} missing from runtime manifest`);
  await mkdir(directory, { recursive: true });
  const temporary = await mkdtemp(join(directory, '.runtimes-'));
  try {
    for (const [version, pair] of Object.entries(manifest)) {
      await mkdir(join(temporary, version));
      for (const file of files) {
        const url = `https://github.com/infercat/infercat/releases/download/v${version}/${file}`;
        await writeFile(join(temporary, version, file), await checked(url, pair[file], fetcher));
      }
    }
    if (!manifest[current]) {
      await mkdir(join(temporary, current));
      for (const file of files) await cp(join(directory, file), join(temporary, current, file));
    }
    await mkdir(join(directory, 'v'), { recursive: true });
    for (const version of [...Object.keys(manifest), ...(!manifest[current] ? [current] : [])]) await cp(join(temporary, version), join(directory, 'v', version), { recursive: true });
  } finally { await rm(temporary, { recursive: true, force: true }); }
}
export async function verifyRuntimes(manifest, origin, fetcher = fetch) {
  validate(manifest);
  for (const [version, pair] of Object.entries(manifest)) for (const file of files) {
    const url = `${origin.replace(/\/$/, '')}/v/${version}/${file}`;
    await checked(url, pair[file], fetcher, true);
    console.log(`PASS ${url}: 200, CORS *, SHA-256 ${pair[file]}`);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const manifest = JSON.parse(await readFile(manifestURL, 'utf8'));
  if (process.argv[2] === '--verify') await verifyRuntimes(manifest, process.argv[3] ?? 'https://infercat.ai');
  else {
    if (process.argv.length !== 4) throw new Error('Usage: node hack/runtime-releases.mjs DEPLOY_DIR CURRENT_VERSION | --verify [ORIGIN]');
    await stageRuntimes(manifest, resolve(process.argv[2]), process.argv[3]);
    console.log(`Staged ${Object.keys(manifest).length} released runtime pairs; current ${process.argv[3]}`);
  }
}
