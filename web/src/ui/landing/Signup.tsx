import { useId, useState, type FormEvent } from 'react';
import { useLanguage } from './Language';

export function Signup() {
  const { lang, t } = useLanguage();
  const id = useId();
  const [result, setResult] = useState<'idle' | 'saving' | 'ok' | 'invalid' | 'failed' | 'limited'>('idle');
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    if (!form.checkValidity()) {
      setResult('invalid');
      return;
    }
    const email = String(new FormData(form).get('email') ?? '').trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      setResult('invalid');
      return;
    }
    setResult('saving');
    try {
      const response = await fetch('/signup', {
        method: 'POST',
        signal: AbortSignal.timeout(15000),
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          email,
          lang,
          from: 'landing-roadmap',
          ts: new Date().toISOString(),
        }),
      });
      const saved = response.ok && (await response.json()).ok === true;
      setResult(
        saved ? 'ok' : response.status === 400 ? 'invalid' : response.status === 429 ? 'limited' : 'failed',
      );
    } catch {
      setResult('failed');
    }
  }
  const noteKey =
    result === 'invalid'
      ? 'ml_invalid'
      : result === 'failed'
        ? 'ml_failed'
        : result === 'limited'
          ? 'ml_limited'
          : 'ml_note';
  const note = t[noteKey];
  return (
    <form className="ml" action="/signup" method="post" noValidate onSubmit={submit}>
      {result === 'ok' ? (
        <div className="ok" role="status">
          <p className="ok-t">
            <span className="ck">✓</span> <span data-copy="ml_ok_t">{t.ml_ok_t}</span>
          </p>
          <p className="ok-b" data-copy="ml_ok_b">
            {t.ml_ok_b}
          </p>
        </div>
      ) : (
        <>
          <label data-copy="ml_label" className="field-label" htmlFor={id}>
            {t.ml_label}
          </label>
          <div className="ml-row">
            <input
              className="inp"
              id={id}
              type="email"
              name="email"
              data-copy-placeholder="ml_ph"
              placeholder={t.ml_ph}
              autoComplete="email"
              required
              maxLength={254}
              aria-describedby={`${id}-note`}
            />
            <button data-copy="ml_btn" className="primary" type="submit" disabled={result === 'saving'}>
              {t.ml_btn}
            </button>
          </div>
          <p
            className={`fine ${['invalid', 'failed', 'limited'].includes(result) ? 'form-error' : ''}`}
            data-copy={noteKey}
            id={`${id}-note`}
            role="status"
          >
            {note}
          </p>
        </>
      )}
    </form>
  );
}
