// The connect screen: the landing page a stranger meets. One sentence about what this is, one
// field, one button, and progress that says what is actually happening. It owns no connection
// state of its own — it dispatches into the session machine (src/session.ts) and renders it.
import { useEffect, useRef, useState } from 'react';
import { describeError, getMe, hostName, logsPrompts, type FriendlyError, type Me } from '../api';
import { decodeInvite, inviteFromHash, InviteError, maskInvite } from '../invite';
import { PRODUCT_NAME, privacyLine } from '../product';
import type { Live, SessionEvent, SessionState } from '../session';
import {
  countChats,
  dropLegacyHistory,
  forget,
  hostScope,
  KEYS,
  load,
  save,
  type LastHost,
} from '../storage';
import { claimTunnelIdentity, openTransport, type Transport } from '../transport';
import { composing } from './composing';

declare const __DEFAULT_DIRECT_URL__: string;

/**
 * An invite handed over as a link (`<app>/#bn1.…`, which the host's CLI prints). Read once, at
 * module load, and wiped from the address bar in the same breath, so the secret is not left in
 * history or in a screenshot of the address bar. Not a hook or an initializer — those run twice
 * under StrictMode.
 */
const HASH_INVITE = takeHashInvite();

/**
 * A code that arrived by link or from the last visit connects by itself — the click on the link,
 * or the return, is the consent (020 promise 8, ruling) — once per page load. This screen remounts
 * whenever a session ends, and a component-level ref would make a revoked invite reconnect itself
 * for ever; after the one attempt the card is the reader's.
 */
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
  // Kept the moment it is read (014 promise 10): the link is gone from the address bar by design,
  // so a reader who reloads before the connect has finished must not lose the invite with it.
  save(KEYS.invite, found);
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
  const [disclosure, setDisclosure] = useState<Disclosure | null>(null);
  // A returning reader is not a stranger: this is what we already know about their last host.
  const [lastHost] = useState<LastHost | null>(() => load<LastHost | null>(KEYS.lastHost, null));
  const [chats] = useState(() => (lastHost ? countChats(lastHost.scope) : 0));
  const [showCode, setShowCode] = useState(false);
  const [pasting, setPasting] = useState(false);
  const attempt = useRef(0);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!autoconnected && state.name === 'idle' && text !== '' && inviteProblem(text) === null) {
      autoconnected = true;
      void connect();
      return;
    }
    field.current?.focus();
    // Mount only: this is the entry point, not a reactive form.
  }, []);

  async function connect(): Promise<void> {
    const raw = text.trim();
    let addr: string;
    let secret: string;
    try {
      ({ addr, secret } = decodeInvite(raw));
    } catch {
      // The inline check below already says what is wrong and has disabled the button; this is
      // only the keyboard path arriving at the same wall.
      field.current?.focus();
      return;
    }
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
      // attempt superseding this one — and a Cancel, which is a superseding attempt with no dial.
      dispatch({ t: 'sessionUp', transport });
      const me = await getMe(transport, secret);
      if (mine !== attempt.current) return; // cancelled while verifying; the machine closed it
      save(KEYS.invite, raw);
      // What the connect screen may say next time before it has reconnected: a name and a scope,
      // both public. Never the secret (014 promise 9).
      save(KEYS.lastHost, { name: hostName(me), scope: hostScope(addr, me.key.id) } as LastHost);
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
        meOk: true,
        key: me.key.status,
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
      const who = lastHost?.name ?? '';
      dispatch(
        transport
          ? { t: 'meError', error: describeError(err, who) }
          : { t: 'abort', error: describeConnectError(err, reached, who) },
      );
    }
  }

  /** Stops the attempt in flight: it becomes a superseded one, and the machine closes what it opened. */
  function cancel(): void {
    attempt.current++;
    dispatch({ t: 'abort', error: null });
  }

  function forgetInvite(): void {
    forget(KEYS.invite, KEYS.privateKey, KEYS.lastHost);
    setRemembered('');
    setText('');
    setPasting(true);
    dispatch({ t: 'abort', error: null });
    field.current?.focus();
  }

  /** Typing is the reader answering the last failure; the old one stops being the current news. */
  function edit(next: string): void {
    setText(next);
    if (state.name === 'disconnected' && state.reason !== null) dispatch({ t: 'abort', error: null });
  }

  const busy = state.name === 'loadingWasm' || state.name === 'connecting' || state.name === 'verifying';
  const steps = direct ? STEPS.filter((s) => s.at === 'verifying') : STEPS;
  const at = steps.findIndex((s) => s.at === state.name);
  const failure = state.name === 'disconnected' ? state.reason : null;
  // 014 promise 10: the check runs as they paste, so Connect is never a dead button with no
  // reason next to it — the reason is what disables it.
  const formatError = text.trim() === '' ? null : inviteProblem(text);
  const malformed = formatError !== null;
  // A code we already hold — from the link, or from the last visit — is a secret sitting in a text
  // box on a screen somebody may be sharing: shown as what it is, and in full only when the reader
  // asks (014 promise 9, 020 promise 8).
  const known = !pasting && text !== '' && (text === HASH_INVITE || text === remembered);
  // A returning reader with history on this device gets their own face (014 promise 9). A code
  // that arrived by link is new news and takes precedence over "welcome back".
  const returning = !busy && !pasting && HASH_INVITE === '' && remembered !== '' && text === remembered && chats > 0;
  const who = lastHost?.name?.trim() ?? '';
  // A revoked or unrecognised invite cannot be retried; the only move is a new code from the host.
  const needsNewCode = failure?.fatal === true;

  if (disclosure) return <LogPromptsGate me={disclosure.me} onAccept={disclosure.accept} />;

  // While an attempt runs the card says what is happening and offers the one thing that makes
  // sense meanwhile. The reader never sees a field they cannot type into.
  if (busy) {
    return (
      <main className="connect">
        <div className="connect-card">
          <h1>{PRODUCT_NAME}</h1>
          <p className="pitch">{who ? `Connecting to ${who}…` : 'Connecting…'}</p>
          <ol className="steps" aria-live="polite">
            {steps.map((step, i) => (
              <li key={step.at} className={i < at ? 'done' : i === at ? 'now' : 'next'}>
                <span>{step.label}</span>
                <span className="step-detail">{stepDetail(state, i, at)}</span>
              </li>
            ))}
          </ol>
          <div className="connect-actions">
            <button className="ghost" onClick={cancel}>
              Cancel
            </button>
          </div>
          <p className="privacy">{privacyLine(who, false)}</p>
        </div>
      </main>
    );
  }

  return (
    <main className="connect">
      <div className="connect-card">
        <h1>{PRODUCT_NAME}</h1>
        {returning ? (
          <p className="pitch">
            <strong>Welcome back.</strong> Your {chats} {chats === 1 ? 'chat is' : 'chats are'} with{' '}
            {who || 'your host'} still on this device.
          </p>
        ) : (
          <p className="pitch">
            Chat with a friend’s GPU. They send you one code; you paste it here. No account, no
            install, nothing to set up.
          </p>
        )}

        {/* Never above a failure: a code that just failed is not "ready" (020 promise 5). */}
        {HASH_INVITE !== '' && text === HASH_INVITE && !failure && (
          <p className="notice">Invite from your link is ready.</p>
        )}

        {known && !showCode ? (
          <div className="field">
            <span className="field-label">Invite code</span>
            <div className="masked">
              <code>{maskInvite(text)}</code>
              <button className="ghost tiny" onClick={() => setShowCode(true)}>
                Show
              </button>
            </div>
          </div>
        ) : (
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
              onChange={(e) => {
                edit(e.target.value);
                void import('./Chat'); // warm the chat chunk while they are still typing
              }}
              onKeyDown={(e) => {
                // An IME's Enter commits a candidate; it is not a submit (promise 9).
                if (e.key === 'Enter' && !e.shiftKey && !composing(e)) {
                  e.preventDefault();
                  if (!needsNewCode) void connect();
                }
              }}
            />
          </label>
        )}
        {formatError && <p className="inline-error">{formatError}</p>}

        <div className="connect-actions">
          {/* A code the host has revoked gets no Connect: pressing it could only reproduce the
              failure below, whose own button is the one that works (020 promise 5). */}
          <button
            className="primary"
            onClick={() => void connect()}
            disabled={text.trim() === '' || malformed || needsNewCode}
          >
            {returning ? 'Reconnect' : 'Connect'}
          </button>
          {/* While a failure is showing, the same action lives inside it, next to the reason. */}
          {remembered !== '' && !failure && (
            <button className="ghost" onClick={forgetInvite}>
              Forget this invite
            </button>
          )}
        </div>

        {failure && (
          <div className="failure" role="alert">
            <strong>{failure.title}</strong>
            <p>{failure.detail}</p>
            {failure.hostSaid && (
              <details className="host-said">
                <summary>Details</summary>
                <p>{failure.hostSaid}</p>
              </details>
            )}
            <div className="connect-actions">
              {needsNewCode ? (
                <button className="primary" onClick={forgetInvite}>
                  Paste a new code
                </button>
              ) : (
                <button className="ghost" onClick={() => void connect()}>
                  Try again
                </button>
              )}
              {remembered !== '' && !needsNewCode && (
                <button className="ghost" onClick={forgetInvite}>
                  Forget this invite
                </button>
              )}
            </div>
          </div>
        )}

        <p className="privacy">{privacyLine(who, false)}</p>

        {dev && (
          <label className="devmode">
            <input type="checkbox" checked={direct} onChange={(e) => setDirect(e.target.checked)} />
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

/** What is wrong with what is in the field, or null when nothing is. Cheap; runs on every key. */
function inviteProblem(text: string): string | null {
  try {
    decodeInvite(text.trim());
    return null;
  } catch (err) {
    return err instanceof InviteError ? err.message : String(err);
  }
}

/** The same failure means different things depending on how far we got; say the useful thing. */
function describeConnectError(err: unknown, at: SessionState['name'], host: string): FriendlyError {
  if (at === 'loadingWasm') {
    return {
      title: 'Could not load the tunnel',
      detail: 'Reload the page; if it keeps failing, this copy of the app was published without its tunnel module.',
      ...(err instanceof Error && err.message.trim() !== '' ? { hostSaid: err.message.trim() } : {}),
    };
  }
  if (at === 'connecting') {
    return {
      title: `${host.trim() || 'The host'} didn’t answer`,
      detail:
        'The invite looks well-formed, so either their machine is asleep or offline, or the relay could not be reached from this network. Ask them to check that the host is running.',
      ...(err instanceof Error && err.message.trim() !== '' ? { hostSaid: err.message.trim() } : {}),
    };
  }
  return describeError(err, host);
}
