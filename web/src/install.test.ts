import { expect, it } from 'vitest';
import { detectPlatform, installPath, shouldSuggest } from './install';
const iosUA = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Version/18.0 Mobile/15E148 Safari/604.1';
const desktopSafari = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Version/18.0 Safari/605.1.15';
const chrome = 'Mozilla/5.0 (Linux; Android 15) Chrome/140.0 Mobile Safari/537.36';
it('gates the one suggestion on a completed reply, phone, path, and no stored choice', () => {
  const phone = detectPlatform(chrome, 1, true, false);
  expect(shouldSuggest(phone, true, true, '')).toBe(true);
  for (const completed of [false, true]) for (const choice of ['accepted', 'dismissed']) expect(shouldSuggest(phone, true, completed, choice)).toBe(false);
  expect(shouldSuggest(phone, true, false, '')).toBe(false);
  expect(shouldSuggest(phone, false, true, '')).toBe(false);
  expect(shouldSuggest({ ...phone, standalone: true }, true, true, '')).toBe(false);
  expect(shouldSuggest({ ...phone, coarse: false }, true, true, '')).toBe(false);
});
it('offers iOS Safari manual steps, including iPad desktop mode, but no unknown browser path', () => {
  const ios = detectPlatform(iosUA, 1, true, false);
  expect(installPath(ios, false)).toBe('ios'); expect(shouldSuggest(ios, false, true, '')).toBe(true);
  expect(detectPlatform(desktopSafari, 5, true, false).ios).toBe(true);
  expect(installPath(detectPlatform(iosUA.replace('Version/18.0', 'CriOS/140.0'), 1, true, false), false)).toBeNull();
  expect(installPath(detectPlatform('Firefox/140', 0, false, false), false)).toBeNull();
});
it('distinguishes desktop prompts, Safari Dock instructions, and installed window copy', () => {
  expect(installPath(detectPlatform(chrome, 0, false, false), true)).toBe('desktop');
  expect(installPath(detectPlatform(desktopSafari, 0, false, false), false)).toBe('safari');
  expect(installPath(detectPlatform(desktopSafari.replace('18.0', '16.0'), 0, false, false), false)).toBeNull();
  expect(installPath(detectPlatform(chrome, 1, true, true), false)).toBe('installed-phone');
  expect(installPath(detectPlatform(chrome, 0, false, true), false)).toBe('installed-desktop');
});
