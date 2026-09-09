import type { RemoteState } from './remote';
export declare function mountConsole(host: HTMLElement, secret: string, request: typeof fetch, remote: RemoteState): { stop(): void; refresh(): Promise<boolean> };
