import { useSyncExternalStore } from 'react';
import { load, save } from './storage';

export interface InstallPrompt extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}
export interface Platform {
  standalone: boolean;
  coarse: boolean;
  ios: boolean;
  safariDesktop: boolean;
}
export function detectPlatform(ua: string, touchPoints: number, coarse: boolean, standalone: boolean): Platform {
  const safari = /Safari\//.test(ua) && !/Chrome|Chromium|CriOS|FxiOS|Edg|OPR|Android/.test(ua);
  const ios = safari && (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1));
  const version = Number(/Version\/(\d+)/.exec(ua)?.[1] ?? 0);
  return { standalone, coarse, ios, safariDesktop: safari && !ios && /Macintosh/.test(ua) && version >= 17 };
}
export function platform(): Platform {
  return detectPlatform(navigator.userAgent, navigator.maxTouchPoints, globalThis.matchMedia?.('(pointer: coarse)').matches ?? false,
    globalThis.matchMedia?.('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true);
}
export function installPath(p: Platform, prompt: boolean): 'phone' | 'desktop' | 'ios' | 'safari' | 'installed-phone' | 'installed-desktop' | null {
  if (p.standalone) return p.coarse ? 'installed-phone' : 'installed-desktop';
  if (p.ios) return 'ios';
  if (prompt) return p.coarse ? 'phone' : 'desktop';
  return p.safariDesktop ? 'safari' : null;
}
export function shouldSuggest(p: Platform, hasPrompt: boolean, completed: boolean, choice: string): boolean {
  return completed && !choice && p.coarse && !p.standalone && (p.ios || hasPrompt);
}
const INSTALL_KEY = 'bn.install';
let held: InstallPrompt | null = null;
let completed = false;
let choice = ''; // Still dismisses this page if storage is unavailable.
let revision = 0;
const listeners = new Set<() => void>();
function changed(): void { revision++; for (const listener of listeners) listener(); }
export function initializeInstall(): () => void {
  const prompt = (event: Event) => { event.preventDefault(); held = event as InstallPrompt; changed(); };
  const installed = () => { held = null; choice = 'accepted'; save(INSTALL_KEY, choice); changed(); };
  const storage = (event: StorageEvent) => { if (event.key === INSTALL_KEY) { choice = load(INSTALL_KEY, ''); changed(); } };
  const mode = matchMedia('(display-mode: standalone)');
  window.addEventListener('beforeinstallprompt', prompt);
  window.addEventListener('appinstalled', installed);
  window.addEventListener('storage', storage);
  mode.addEventListener('change', changed);
  return () => { window.removeEventListener('beforeinstallprompt', prompt); window.removeEventListener('appinstalled', installed); window.removeEventListener('storage', storage); mode.removeEventListener('change', changed); };
}
export function noteCompletedReply(): void { if (!completed) { completed = true; changed(); } }
export function dismissInstall(): void { choice = 'dismissed'; save(INSTALL_KEY, choice); changed(); }
/** Must be called in the Add gesture; never automatically prompt a returning reader. */
export function acceptInstall(): 'ios' | 'prompt' | null {
  const path = installPath(platform(), held !== null);
  if (path !== 'ios' && !held) return null;
  choice = 'accepted'; save(INSTALL_KEY, choice);
  const prompt = held; held = null; changed();
  if (path === 'ios') return 'ios';
  void prompt?.prompt().catch(() => {});
  return 'prompt';
}
export function useInstall() {
  useSyncExternalStore((listener) => { listeners.add(listener); return () => listeners.delete(listener); }, () => revision);
  const p = platform();
  return { platform: p, path: installPath(p, held !== null), suggest: shouldSuggest(p, held !== null, completed, choice || load(INSTALL_KEY, '')) };
}
