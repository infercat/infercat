import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { transformWithEsbuild, type Plugin } from 'vite';

export function staticAssetPath(name: string, source: string | Uint8Array): string {
  return `static/${createHash('sha256').update(source).digest('hex').slice(0, 16)}/${name}`;
}
const publicDirectory = new URL('../public/', import.meta.url);
const staticFiles = ['favicon.svg', 'favicon.png', 'apple-touch-icon.png', 'icon-192.png', 'icon-512.png', 'icon-maskable-192.png', 'icon-maskable-512.png', 'icon-monochrome-512.png',
  ...readdirSync(new URL('fonts/', publicDirectory)).filter((name) => name.endsWith('.woff2')).map((name) => `fonts/${name}`)];
const publicAssets = new Map(staticFiles.map((name) => {
  const source = readFileSync(new URL(name, publicDirectory));
  return [name, { path: staticAssetPath(name, source), source }];
}));
export const publicAssetURL = (name: string): string | undefined => {
  const asset = publicAssets.get(name); return asset ? `/${asset.path}` : undefined;
};

interface ManifestEntry { file: string; css?: string[]; assets?: string[] }
export function precacheURLs(manifest: Record<string, ManifestEntry>, publicFiles: string[]): string[] {
  const files = ['index.html', ...publicFiles, ...Object.values(manifest).flatMap((entry) => [entry.file, ...(entry.css ?? []), ...(entry.assets ?? [])])];
  for (const file of files) if (!file || file.startsWith('/') || file.includes('..') || /[?#:]/.test(file)) throw new Error(`Not a static precache path: ${file}`);
  return [...new Set(files)].map((file) => `/${file}`).sort();
}
export function offlineShell(version: string): Plugin {
  if (!/^[A-Za-z0-9][A-Za-z0-9._+-]*$/.test(version)) throw new Error('Invalid product version');
  return {
    name: 'infercat-offline-shell', apply: 'build', enforce: 'pre',
    transform(code, id) {
      if (!id.includes('/src/') || !/\.[jt]sx?$/.test(id)) return;
      // Vite rewrites HTML/CSS public URLs; code-owned icon literals need the same immutable URL.
      for (const [name, asset] of publicAssets) for (const quote of ['"', "'"]) code = code.replaceAll(`${quote}/${name}${quote}`, `${quote}/${asset.path}${quote}`);
      return { code, map: null };
    },
    async writeBundle(options) {
      const directory = resolve(options.dir ?? 'dist');
      const manifest = JSON.parse(await readFile(join(directory, '.vite/manifest.json'), 'utf8')) as Record<string, ManifestEntry>;
      const manifestText = await readFile(join(directory, 'manifest.webmanifest'), 'utf8');
      const assets = [...publicAssets.values(), { path: staticAssetPath('manifest.webmanifest', manifestText), source: Buffer.from(manifestText) }];
      for (const { path, source } of assets) { await mkdir(dirname(join(directory, path)), { recursive: true }); await writeFile(join(directory, path), source); }
      const shell = precacheURLs(manifest, assets.map((asset) => asset.path));
      const runtimeDirectory = `runtime/${version}`;
      await mkdir(join(directory, runtimeDirectory), { recursive: true });
      const runtime = ['wasm_exec.js', 'infercat.wasm.gz'].map((file) => `/runtime/${encodeURIComponent(version)}/${file}`);
      for (const file of ['wasm_exec.js', 'infercat.wasm.gz']) await copyFile(join(directory, file), join(directory, runtimeDirectory, file));
      const hash = createHash('sha256');
      for (const url of shell) { hash.update(url); hash.update(await readFile(join(directory, url.slice(1)))); }
      const revision = hash.digest('hex').slice(0, 16);
      const source = await readFile(new URL('../src/sw.ts', import.meta.url), 'utf8');
      const compiled = await transformWithEsbuild(source, 'sw.ts', { loader: 'ts', target: 'es2022', minify: true,
        define: { __PRECACHE__: JSON.stringify({ revision, version, shell, runtime }) } });
      await writeFile(join(directory, 'sw.js'), compiled.code);
      await writeFile(join(directory, 'precache.json'), JSON.stringify({ revision, version, shell, runtime }, null, 2));
    },
  };
}
