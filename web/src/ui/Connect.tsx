// The connect screen: the landing page a stranger meets. One sentence about what this is, one
// field, one button, and progress that says what is actually happening. It owns no connection
// state of its own — it dispatches into the session machine (src/session.ts) and renders it.
import { useEffect, useRef, useState } from 'react';
import { describeError, getMe, hostName, logsPrompts, type FriendlyError, type Me } from '../api';
import { decodeInvite, inviteFromHash, InviteError } from '../invite';
import { PRODUCT_NAME } from '../product';
import type { Live, SessionEvent, SessionState } from '../session';
import { dropLegacyHistory, forget, KEYS, load, save } from '../storage';
import { claimTunnelIdentity, openTransport, type Transport } from '../transport';
import { composing } from './composing';

declare const __DEFAULT_DIRECT_URL__: string;

/**
 * An invite handed over as a link (`<app>/#bn1.…`, which the host's CLI prints). Read once, at
 * module load, and wiped from the address bar in the same breath: the field is filled in, the
 * reader still presses Connect, and the secret is not left in history or in a screenshot of the
 * address bar. Not a hook or an initializer — those run twice under StrictMode.
 */
const HASH_INVITE = takeHashInvite();

/** `?autoconnect` (dev) means once per page load. This screen remounts whenever a session ends,
 *  and a component-level ref would make a revoked invite reconnect itself for ever. */
let autoconnected = false;

function takeHashInvite(): string {
  if (typeof location === 'undefined') return '';
  const found = inviteFromHash(location.hash);
  if (found === '') return '';
  try {
    history.replaceState(null, '', `${location.pathname}${location.search}`);
  } catch {
    /* no history access (sandboxed frame): the field is still filled in */
  }
  return found;
}

const STEPS: { at: SessionState['name']; label: string }[] = [
  { at: 'loadingWasm', label: 'Loading the tunnel' },
  { at: 'connecting', label: 'Connecting to the relay' },
  { at: 'verifying', label: 'Checking your invite' },
];

interface Props {
  state: SessionState;
  dispatch: (e: SessionEvent) => void;
}

/** A host that logs prompts must say so before the reader types, not in a settings sheet. */
interface Disclosure {
  me: Me;
  accept: () => void;
}

export default function Connect({ state, dispatch }: Props) {
  const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const dev = import.meta.env.DEV;
  const [remembered, setRemembered] = useState(() => load<string>(KEYS.invite, ''));
  const [text, setText] = useState(
    () => HASH_INVITE || (dev ? (params.get('invite') ?? '') : '') || remembered,
  );
  const [direct, setDirect] = useState(() => dev && params.has('direct'));
  const [formatError, setFormatError] = useState<string | null>(null);
  const [disclosure, setDisclosure] = useState<Disclosure | null>(null);
  const attempt = useRef(0);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    field.current?.focus();
    if (dev && params.has('autoconnect') && !autoconnected) {
      autoconnected = true;
      void connect();
    }
    // Mount only: this is the entry point, not a reactive form.
  }, []);

  async function connect(): Promise<void> {
    const raw = text.trim();
    let addr: string;
    let secret: string;
    try {
      ({ addr, secret } = decodeInvite(raw));
    } catch (err) {
      setFormatError(err instanceof InviteError ? err.message : String(err));
      field.current?.focus();
      return;
    }
    setFormatError(null);
    setDisclosure(null);
    const mine = ++attempt.current;
    const mode: 'direct' | 'tunnel' = direct ? 'direct' : 'tunnel';
    dispatch({ t: 'start', mode });

    // How far this attempt got, for the failure copy. The prop is a render-time snapshot and this
    // function outlives several renders, so the attempt tracks its own progress.
    let reached: SessionState['name'] = mode === 'direct' ? 'connecting' : 'loadingWasm';
    let transport: Transport | null = null;
    try {
      // Only the tab holding the identity lock may use — or overwrite — the stored tunnel key.
      const exclusive = mode === 'tunnel' ? await claimTunnelIdentity() : true;
      const saved = exclusive ? load<string>(KEYS.privateKey, '') : '';
      const opened = await openTransport(addr, {
        mode,
        directURL: __DEFAULT_DIRECT_URL__,
        ...(saved ? { privateKey: saved } : {}),
        onWasmProgress: (pct) => mine === attempt.current && dispatch({ t: 'wasmProgress', pct }),
        onWasmLoaded: () => {
          reached = 'connecting';
          if (mine === attempt.current) dispatch({ t: 'wasmLoaded' });
        },
      });
      transport = opened.transport;
      reached = 'verifying';
      // From here the machine owns it: every path out of `verifying` closes it, including a newer
      // attempt superseding this one.
      dispatch({ t: 'sessionUp', transport });
      const me = await getMe(transport, secret);
      save(KEYS.invite, raw);
      if (exclusive && opened.privateKeyJSON) save(KEYS.privateKey, opened.privateKeyJSON);
      setRemembered(raw);
      dropLegacyHistory();
      const live: Live = {
        transport,
        secret,
        addr,
        me,
        mode,
        path: opened.path,
        pathAt: Date.now(),
        pathOk: opened.path !== null,
        ephemeral: !exclusive,
      };
      // Promise 12: the privacy sentence on this page is only true when the host is not logging.
      // If it is, the correction goes here — before the first message, not after it.
      if (logsPrompts(me)) {
        setDisclosure({ me, accept: () => dispatch({ t: 'verified', live }) });
        return;
      }
      dispatch({ t: 'verified', live });
    } catch (err) {
      if (mine !== attempt.current) return; // superseded; the machine already closed our transport
      dispatch(
        transport
          ? { t: 'meError', error: describeError(err) }
          : { t: 'abort', error: describeConnectError(err, reached) },
      );
    }
  }

  function forgetInvite(): void {
    forget(KEYS.invite, KEYS.privateKey);
    setRemembered('');
    setText('');
    dispatch({ t: 'abort', error: null });
    field.current?.focus();
  }

  const busy = state.name === 'loadingWasm' || state.name === 'connecting' || state.name === 'verifying';
  const steps = direct ? STEPS.filter((s) => s.at === 'verifying') : STEPS;
  const at = steps.findIndex((s) => s.at === state.name);
  const failure = state.name === 'disconnected' ? state.reason : null;

  if (disclosure) return <LogPromptsGate me={disclosure.me} onAccept={disclosure.accept} />;

  return (
    <main className="connect">
      <div className="connect-card">
        <h1>{PRODUCT_NAME}</h1>
        <p className="pitch">
          Chat with a friend’s GPU. They send you one code; you paste it here. No account, no
          install, nothing to set up.
        </p>

        <label className="field">
          <span className="field-label">Invite code</span>
          <textarea
            ref={field}
            value={text}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            rows={3}
            placeholder="bn1.…"
            disabled={busy}
            onChange={(e) => {
              setText(e.target.value);
              setFormatError(null);
              void import('./Chat'); // warm the chat chunk while they are still typing
            }}
            onKeyDown={(e) => {
              // An IME's Enter commits a candidate; it is not a submit (promise 9).
              if (e.key === 'Enter' && !e.shiftKey && !composing(e)) {
                e.preventDefault();
                void connect();
              }
            }}
          />
        </label>
        {formatError && <p className="inline-error">{formatError}</p>}

        <div className="connect-actions">
          <button className="primary" onClick={() => void connect()} disabled={busy || text.trim() === ''}>
            {busy ? 'Connecting…' : 'Connect'}
          </button>
          {/* While a failure is showing, the same action lives inside it, next to the reason. */}
          {remembered !== '' && !busy && !failure && (
            <button className="ghost" onClick={forgetInvite}>
              Forget this invite
            </button>
          )}
        </div>

        {busy && (
          <ol className="steps" aria-live="polite">
            {steps.map((step, i) => (
              <li key={step.at} className={i < at ? 'done' : i === at ? 'now' : 'next'}>
                <span>{step.label}</span>
                <span className="step-detail">{stepDetail(state, i, at)}</span>
              </li>
            ))}
          </ol>
        )}

        {failure && (
          <div className="failure" role="alert">
            <strong>{failure.title}</strong>
            <p>{failure.detail}</p>
            {failure.hostSaid && <p className="dim">The host said: “{failure.hostSaid}”</p>}
            {failure.fatal && remembered !== '' && (
              <p className="dim">
                This browser is still holding the invite you pasted last time. If your host rotated
                it, forget it and paste the new one.
              </p>
            )}
            <div className="connect-actions">
              <button className="ghost" onClick={() => void connect()}>
                Try again
              </button>
              {remembered !== '' && (
                <button className="ghost" onClick={forgetInvite}>
                  Forget this invite
                </button>
              )}
            </div>
          </div>
        )}

        <p className="privacy">
          Your messages travel end-to-end encrypted to your host’s machine. The host sees counts —
          how many requests and tokens you used — never what you wrote.
        </p>

        {dev && (
          <label className="devmode">
            <input type="checkbox" checked={direct} onChange={(e) => setDirect(e.target.checked)} disabled={busy} />
            Direct mode (dev) — talk to {__DEFAULT_DIRECT_URL__} instead of the tunnel
          </label>
        )}
      </div>
    </main>
  );
}

/**
 * The host runs with --log-prompts. BELIEFS.md says that flag "says so loudly": the reader learns it
 * here, in place of the promise this page just made them, and chooses before typing anything.
 */
function LogPromptsGate({ me, onAccept }: { me: Me; onAccept: () => void }) {
  return (
    <main className="connect">
      <div className="connect-card">
        <h1>{PRODUCT_NAME}</h1>
        <div className="failure" role="alert">
          <strong>{hostName(me) || 'This host'} is recording what you write</strong>
          <p>
            This host is running with prompt logging on. Everything you send, and everything the
            model answers, is written to a log on their machine. That is not the normal setting and
            it is not something this app can turn off.
          </p>
          <p className="dim">
            Connected as <code>{me.key.name}</code> ({me.key.id}). Nothing has been sent yet.
          </p>
          <div className="connect-actions">
            <button className="primary" onClick={onAccept}>
              I understand — start chatting
            </button>
            <button className="ghost" onClick={() => location.reload()}>
              Not now
            </button>
          </div>
        </div>
      </div>
    </main>
  );
}

function stepDetail(state: SessionState, i: number, at: number): string {
  if (i < at) return 'done';
  if (i !== at) return '';
  if (state.name === 'loadingWasm') return state.pct === null ? '…' : `${state.pct}%`;
  return '…';
}

/** The same failure means different things depending on how far we got; say the useful thing. */
function describeConnectError(err: unknown, at: SessionState['name']): FriendlyError {
  if (at === 'loadingWasm') {
    return {
      title: 'Could not load the tunnel',
      detail:
        err instanceof Error
          ? `${err.message}. Reload the page; if it keeps failing, this copy of the app was published without its tunnel module.`
          : 'Reload the page and try again.',
    };
  }
  if (at === 'connecting') {
    return {
      title: 'The host did not answer',
      detail:
        'The invite looks well-formed, so either the host’s machine is asleep or offline, or the relay could not be reached from this network. Ask them to check that the host is running.',
    };
  }
  return describeError(err);
}
