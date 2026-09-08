import { en } from './en';
import { zh } from './zh';
import { KEYS, load } from '../storage';
import { PRODUCT_NAME } from '../product';

export type AppKey = Extract<keyof typeof en, `app_${string}`>;
export function appLanguage(): 'en' | 'zh' {
  const saved = load<string>(KEYS.language, '');
  if (saved === 'en' || saved === 'zh') return saved;
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}
export function tr(key: keyof typeof en, values: Record<string, string | number> = {}, language = appLanguage()): string {
  return (language === 'zh' ? zh[key] : en[key]).replace(/\{(\w+)\}/g, (slot, name: string) =>
    values[name] === undefined ? slot : String(values[name]));
}
// Values are text, including names and diagnostics; translations never introduce HTML.
export function computer(name: string): string {
  name = name.trim();
  return name === '' ? tr('app_host_computer_unnamed') :
    /(’s|'s|s’|s')$|\b(laptop|desktop|computer|machine|workstation|server|pc|mac|box|rig)$/i.test(name)
      ? tr('app_host_computer_named', { name }) : tr('app_host_computer_person', { name });
}
export function privacy(host: string, logging: boolean): string {
  return tr(logging ? 'app_privacy_logging' : 'app_privacy_normal', { computer: computer(host), product: PRODUCT_NAME });
}
