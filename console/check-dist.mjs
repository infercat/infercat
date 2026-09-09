import { build } from 'vite';
import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, relative, resolve } from 'node:path';

// Compare bytes in a separate output directory; never silently repair the committed bundle.
async function files(dir, base = dir) {
 const out = [];
 for (const entry of await readdir(dir, { withFileTypes: true })) {
  const path = join(dir, entry.name);
  out.push(...(entry.isDirectory() ? await files(path, base) : [relative(base, path)]));
 }
 return out.sort();
}
const generated = await mkdtemp(join(tmpdir(), 'infercat-console-build-'));
try {
 await build({ build: { outDir: generated, emptyOutDir: true } });
 const fresh = await files(generated), committed = await files(resolve('dist'));
 let equal = JSON.stringify(fresh) === JSON.stringify(committed);
 for (const file of fresh) {
  if (!equal) break;
  equal = (await readFile(join(generated, file))).equals(await readFile(join('dist', file)));
 }
 if (!equal) throw new Error('console/dist is stale; run make console-build and commit the generated bundle');
 try { execFileSync('git', ['diff', '--quiet', 'HEAD', '--', 'console/dist'], {cwd:resolve('..')}); }
 catch { throw new Error('console/dist differs from the committed bundle; commit the fresh build'); }
 console.log('committed console/dist matches a fresh build');
} finally { await rm(generated, { recursive: true, force: true }); }
