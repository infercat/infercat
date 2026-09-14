import { expect, it } from 'vitest';
// @ts-expect-error Pages Functions are plain JS, exercised directly here.
import { onRequestGet, onRequestHead } from '../../../../hosting/cloudflare/functions/try.js';

it.each([null, 'https://infercat.ai#ic1.tcExample.secret', 'https://infercat.ai/#ic2.tcExample.secret'])(
  'HEAD /try preserves the GET redirect for %s', async (link) => {
    const context = { env: { SIGNUPS: { get: async () => link } } };
    const get = await onRequestGet(context), head = await onRequestHead(context);
    expect(head.status).toBe(302);
    expect([...head.headers]).toEqual([...get.headers]);
    expect(head.headers.get('Location')).toBe(
      `https://infercat.ai/?from=try${link ? link.slice(link.indexOf('#')) : ''}`,
    );
    expect(await head.text()).toBe('');
  },
);
