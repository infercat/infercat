import { fakeTransport } from './test/fakes';
import { expect, it } from 'vitest';
import { acceptedVerification } from './App';
import { reduce, type Live, type SessionState } from './session';
import type { Me } from './api';
import type { Transport } from './transport';
const transport = (): Transport => (fakeTransport(async () => new Response('{}'), { kind: 'tunnel' }));
const me: Me = {
  key: { id: 'k', name: 'test', status: 'active' },
  limits: { rpm: 20, tpm: 20000, max_concurrent: 1, max_output_tokens: 2048, max_context: 0, daily_tokens: 200000 },
  usage: { rpm_used: 0, tpm_used: 0, today_tokens: 0, in_flight: 0 },
  host: { name: 'desk', models: ['m'], vision: { m: false }, relay: { region: 'nyc' }, upstream: { kind: 'llama.cpp', healthy: true, model_context: 8192 } },
};
const candidate: Live = { transport: transport(), addr: 'tcTEST', secret: 'secret', me, mode: 'tunnel', path: null, pathAt: 0, pathOk: false, meOk: true, key: 'active', ephemeral: false, probed: 0 };
it('allows a snapshot only when the reducer accepts the actual verifying transport', () => {
  const before: SessionState = { name: 'verifying', transport: candidate.transport };
  const event = { t: 'verified' as const, live: candidate };
  expect(acceptedVerification(before, event, reduce(before, event))).toBe(true);
});
it('never snapshots a stale, cancelled or wrong-transport candidate', () => {
  for (const before of [{ name: 'idle' }, { name: 'disconnected', reason: null }, { name: 'verifying', transport: transport() }, { name: 'connected', live: candidate }] as SessionState[]) {
    const event = { t: 'verified' as const, live: { ...candidate, transport: transport() } };
    expect(acceptedVerification(before, event, reduce(before, event))).toBe(false);
  }
});
