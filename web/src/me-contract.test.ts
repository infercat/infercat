import { expect, expectTypeOf, it } from 'vitest';
import { fakeMe } from '../dev/fake-backend';
import type { Me as ClientMe, Session, GatewayErrorBody } from '../../packages/client/src/index';
import type { Me as AppMe } from './api';

it('shares the app/client contract with the real fake-backend constructor', () => {
  expectTypeOf<AppMe>().toEqualTypeOf<ClientMe>();
  expectTypeOf<ReturnType<Session['me']>>().toEqualTypeOf<Promise<AppMe>>();
  expectTypeOf<ReturnType<typeof fakeMe>>().toMatchTypeOf<AppMe>();
  const me = fakeMe({ models: ['model'], vision: false, transcriptions: true, speech: true });
  expect(me.host.vision).toEqual({ model: false });
  expect(me.host.audio).toEqual({ transcriptions: 'whisper-large-v3', speech: 'kokoro' });
  const legacy = { ...me, host: { name: me.host.name, models: me.host.models,
    upstream: me.host.upstream, relay: me.host.relay } } satisfies ClientMe;
  expect(legacy.host).not.toHaveProperty('audio');
  const future: GatewayErrorBody = { error: { code: 'future_code', type: 'upstream_error', message: 'future' } };
  expect(future.error.code).toBe('future_code');
});
