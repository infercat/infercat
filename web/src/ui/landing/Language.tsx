import { createContext, useContext, useState, type ReactNode } from 'react';
import { en } from '../../i18n/en';
import { zh } from '../../i18n/zh';
import { KEYS, load, save } from '../../storage';

export type Language = 'en' | 'zh';
export type CopyKey = keyof typeof en;
const LanguageContext = createContext({
  lang: 'en' as Language,
  t: en as Record<CopyKey, string>,
  choose: (_lang: Language) => {},
});
export function initialLanguage(): Language {
  const saved = load<string>(KEYS.language, '');
  if (saved === 'en' || saved === 'zh') return saved;
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}
export function LanguageProvider({ children }: { children: ReactNode }) {
  const [lang, setLang] = useState<Language>(initialLanguage);
  function choose(next: Language) {
    setLang(next);
    save(KEYS.language, next);
  }
  return (
    <LanguageContext.Provider value={{ lang, t: lang === 'zh' ? zh : en, choose }}>
      {children}
    </LanguageContext.Provider>
  );
}
export function useLanguage() {
  return useContext(LanguageContext);
}
export function Copy({ name }: { name: CopyKey }) {
  const { t } = useLanguage();
  return <span data-copy={name} dangerouslySetInnerHTML={{ __html: t[name] }} />;
}
export function LanguageLinks() {
  const { lang, choose } = useLanguage();
  return (
    <span className="language-links" aria-label="EN · 中文">
      {(['en', 'zh'] as const).map((value, i) => (
        <span key={value}>
          {i > 0 && <span aria-hidden="true"> · </span>}
          <a
            href={`#${value}`}
            lang={value === 'zh' ? 'zh-Hans' : 'en'}
            aria-current={lang === value ? 'true' : undefined}
            onClick={(e) => {
              e.preventDefault();
              choose(value);
            }}
          >
            {value === 'en' ? 'EN' : '中文'}
          </a>
        </span>
      ))}
    </span>
  );
}
export function FoldStrip() {
  return (
    <div className="landing-fold page-only">
      <a href="#p-s1">
        <Copy name="h_down" />
      </a>
      <p>
        <Copy name="idx" />
      </p>
    </div>
  );
}
