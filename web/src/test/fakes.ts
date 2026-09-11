import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { Transport } from '../transport';
export function fakeTransport<T extends object = object>(fetch: Transport['fetch'], overrides?: T & Partial<Omit<Transport, 'fetch'>>): Transport & T {
  return { kind: 'direct', fetch, ping: async () => null, close() {}, ...overrides } as Transport & T;
}
export function mountHost() {
  const container = document.createElement('div'); document.body.append(container);
  let root: Root | undefined;
  return { container,
    async render(node: ReactNode) { root ??= createRoot(container); await act(async () => root!.render(node)); },
    async unmount() { if (root) await act(async () => root!.unmount()); root = undefined; container.remove(); },
  };
}
export async function typeInto(field: HTMLTextAreaElement, text: string) {
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, text); field.dispatchEvent(new Event('input', { bubbles: true })); });
}
