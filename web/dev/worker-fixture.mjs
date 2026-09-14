import { Buffer } from 'node:buffer';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
export async function workerFixture(suffix = () => '') {
  const root = fileURLToPath(new URL('../dist/', import.meta.url));
  const rules = await readFile(new URL('../../hosting/cloudflare/_redirects', import.meta.url), 'utf8');
  const server = createServer(async (req, res) => {
    const path = new URL(req.url, 'http://localhost').pathname;
    if (path === '/try') { res.writeHead(302, { Location: '/?from=try#ic2.fixture' }).end(); return; }
    const redirect = rules.split('\n').map(line => line.trim().split(/\s+/)).find(parts => parts[0] === path && parts[2] === '302');
    if (redirect) { res.writeHead(302, { Location: redirect[1] }).end(); return; }
    const file = resolve(root, '.' + (path === '/' ? '/index.html' : path));
    if (!file.startsWith(root)) { res.writeHead(404).end(); return; }
    try { let bytes = await readFile(path === '/sw.js' && process.env.SW_FIXTURE ? process.env.SW_FIXTURE : file); if (path === '/sw.js') bytes = Buffer.concat([bytes, Buffer.from(suffix())]); res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json','.svg':'image/svg+xml','.png':'image/png'})[extname(file)] ?? 'application/octet-stream'); res.end(bytes); }
    catch { res.writeHead(404).end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { server, origin: `http://127.0.0.1:${server.address().port}` };
}
