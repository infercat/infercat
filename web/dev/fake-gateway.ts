// A Node http server that answers like the real gateway, for developing the web app in Direct mode
// before ticket 002 lands. Permissive CORS, exactly as `serve --dev-listen` promises.
//
//   node --experimental-strip-types dev/fake-gateway.ts [--port 49090]
//
// Ports stay inside 49000-49999 and are overridable with FAKE_GATEWAY_PORT.
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { handleFake, type FakeOptions } from './fake-backend.ts';

const argPort = process.argv.indexOf('--port');
const PORT = Number(
  argPort > 0 ? process.argv[argPort + 1] : (process.env.FAKE_GATEWAY_PORT ?? 49090),
);
const HOST = process.env.FAKE_GATEWAY_HOST ?? '127.0.0.1';

const options: FakeOptions = {};
if (process.env.FAKE_TOKEN_DELAY_MS) options.tokenDelayMs = Number(process.env.FAKE_TOKEN_DELAY_MS);

const CORS = {
  'access-control-allow-origin': '*',
  'access-control-allow-headers': 'authorization, content-type',
  'access-control-allow-methods': 'GET, POST, OPTIONS',
};

const server = createServer((req: IncomingMessage, res: ServerResponse) => {
  if (req.method === 'OPTIONS') {
    res.writeHead(204, CORS).end();
    return;
  }
  const chunks: Buffer[] = [];
  req.on('data', (c: Buffer) => chunks.push(c));
  req.on('end', () => {
    void serve(req, res, Buffer.concat(chunks).toString('utf8'));
  });
});

async function serve(req: IncomingMessage, res: ServerResponse, body: string): Promise<void> {
  const headers: Record<string, string> = {};
  for (const [k, v] of Object.entries(req.headers)) {
    if (typeof v === 'string') headers[k.toLowerCase()] = v;
  }
  const out = handleFake(
    { method: req.method ?? 'GET', path: req.url ?? '/', headers, body },
    options,
  );
  const started = Date.now();
  if (!out.sse) {
    res.writeHead(out.status, { ...CORS, ...out.headers });
    res.end(out.body ?? '');
    log(req, out.status, Date.now() - started);
    return;
  }
  res.writeHead(out.status, { ...CORS, ...out.headers });
  // Chunked, flushed per event: this is the property the whole demo rests on.
  for await (const frame of out.sse) {
    if (res.writableEnded) break;
    res.write(frame);
  }
  res.end();
  log(req, out.status, Date.now() - started);
}

function log(req: IncomingMessage, status: number, ms: number): void {
  process.stdout.write(`${req.method} ${req.url} ${status} ${ms}ms\n`);
}

server.listen(PORT, HOST, () => {
  process.stdout.write(`fake gateway on http://${HOST}:${PORT}\n`);
});
