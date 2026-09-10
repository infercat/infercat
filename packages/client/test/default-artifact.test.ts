import { afterEach, expect, it, vi } from 'vitest';
import { version } from '../package.json';
import { connect } from '../src/index';
import { loadInfercatTunnel } from '../src/transport/wasm';
vi.mock('../src/transport/wasm', () => ({ loadInfercatTunnel: vi.fn(async () => { throw new Error('fixture: loader boundary'); }) }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });
it.each([undefined, 'https://assets.test/custom/module.wasm.gz'])('selects an exact matching artifact pair (%s)', async (override) => {
  vi.stubGlobal('document', { baseURI: 'https://consumer.test/' });
  await expect(connect('ic1.tcFixture.test_secret', { wasmURL: override })).rejects.toThrow('fixture: loader boundary');
  const exact = override ?? `https://infercat.ai/v/${version}/infercat.wasm.gz`;
  expect(loadInfercatTunnel).toHaveBeenCalledOnce();
  expect(loadInfercatTunnel).toHaveBeenCalledWith(expect.any(Function), new URL('.', exact).href, exact);
});
