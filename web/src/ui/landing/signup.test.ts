import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
// @ts-expect-error The existing Pages middleware is plain JS.
import { onRequest } from '../../../../hosting/cloudflare/functions/_middleware.js';
// @ts-expect-error Pages Functions are plain JS, exercised directly by this runtime test.
import { onRequestPost } from '../../../../hosting/cloudflare/functions/signup.js';

function fixture() {
  const rows = new Map<string, string>();
  const writes: {
    key: string;
    value: string;
    options: { expirationTtl?: number; metadata?: unknown };
  }[] = [];
  const kv = {
    get: async (key: string) => rows.get(key) ?? null,
    put: async (key: string, value: string, options: { expirationTtl?: number; metadata?: unknown }) => {
      rows.set(key, value);
      writes.push({ key, value, options });
    },
  };
  const send = (email = 'Friend@example.com', extra = {}) =>
    onRequestPost({
      request: new Request('https://infercat.test/signup', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'CF-Connecting-IP': '192.0.2.1',
        },
        body: JSON.stringify({
          email,
          lang: 'zh',
          from: 'landing-roadmap',
          ts: '2020-01-01',
          ...extra,
        }),
      }),
      env: { SIGNUPS: kv },
    });
  return { rows, writes, kv, send };
}
describe('signup Function with local KV', () => {
  it('stores a canonical signup and metadata, and duplicate retries preserve it', async () => {
    const f = fixture();
    expect((await f.send()).status).toBe(200);
    const record = f.writes.find((w) => w.key.startsWith('email:'))!;
    expect(JSON.parse(record.value)).toMatchObject({
      email: 'friend@example.com',
      lang: 'zh',
      from: 'landing-roadmap',
    });
    expect(record.options.metadata).toEqual(JSON.parse(record.value));
    expect((await f.send('friend@example.com')).status).toBe(200);
    expect(f.writes).toHaveLength(2);
    expect(f.writes.find((w) => w.key.startsWith('rate:'))!.options.expirationTtl).toBe(60);
  });
  it('rejects invalid addresses without writes', async () => {
    const f = fixture();
    expect((await f.send('not-an-email')).status).toBe(400);
    expect(f.rows.size).toBe(0);
  });
  it('limits another address from the same network', async () => {
    const f = fixture();
    await f.send();
    expect((await f.send('second@example.com')).status).toBe(429);
    expect(f.writes.filter((w) => w.key.startsWith('email:'))).toHaveLength(1);
  });
  it('does not thank the reader if persistence fails', async () => {
    const f = fixture();
    const put = f.kv.put;
    f.kv.put = async (key, value, options) => {
      if (key.startsWith('email:')) throw new Error('offline');
      return put(key, value, options);
    };
    expect((await f.send()).status).toBe(503);
    expect(f.rows.size).toBe(0);
    f.kv.put = put;
    expect((await f.send()).status).toBe(200);
  });
});

it('routes signup to its Function without recording a page view', async () => {
  const routes = JSON.parse(
    readFileSync(new URL('../../../../hosting/cloudflare/_routes.json', import.meta.url), 'utf8'),
  );
  expect(routes.include).toContain('/signup');
  let counted = 0;
  const env = {
    LOADS: {
      writeDataPoint: () => {
        counted++;
      },
    },
  };
  const next = async () => new Response('ok');
  for (const path of ['/signup', '/', '/infercat.wasm.gz']) {
    await onRequest({ request: new Request(`https://infercat.test${path}`), env, next });
  }
  expect(counted).toBe(2);
});
