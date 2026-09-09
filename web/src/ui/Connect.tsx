import { Text } from '../i18n/RichText';
import { tr, privacy } from '../i18n/text';
// The connect screen: the landing page a stranger meets. One sentence about what this is, one
// field, one button, and progress that says what is actually happening. It owns no connection
// state of its own — it dispatches into the session machine (src/session.ts) and renders it.
//
// The hero serves the friend with a code; the sections below serve the reader meeting the product.
// Above 900 px the statement and card share the header's grid. On phones the card carries the
// fold and language links. Page keeps that composition across connection states; its footer and
// landing sections remain available at every width, without owning any connection behaviour.
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { describeError, getMe, hostName, logsPrompts, type FriendlyError, type Me } from '../api';
import { decodeCode as decodeInvite,isAdminCode } from '../admin-route';
import { inviteFromHash, InviteError, inviteHint, maskInvite } from '../invite';
import { HOST_URL, PRODUCT_NAME, SOURCE_URL, VERSION } from '../product';
import { boundHandshake, handshakeFailure, type Live, type SessionEvent, type SessionState } from '../session';
import {
  countChats,
  dialsOnArrival,
  dropLegacyHistory,
  forget,
  hostScope,
  KEYS,
  load,
  save,
  type LastHost,
} from '../storage';
import { claimTunnelIdentity, openTransport, type Transport } from '../transport';
import { clipboardReader } from './clipboard';
import { composing } from './composing';
import { Landing } from './landing/Landing';
import { Copy, FoldStrip, LanguageLinks, LanguageProvider, useLanguage } from './landing/Language';

declare const __DEFAULT_DIRECT_URL__: string;

/**
 * An invite handed over as a link (`<app>/#ic1.…`, which the host's CLI prints). Read once, at
 * module load, and wiped from the address bar in the same breath, so the secret is not left in
 * history or in a screenshot of the address bar. Not a hook or an initializer — those run twice
 * under StrictMode.
 */
const networkDown = (): boolean => navigator.onLine === false;
const HASH_INVITE = takeHashInvite();
export const arrivedByLink = (): boolean => HASH_INVITE !== '';

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
  { at: 'loadingWasm', get label() { return tr('app_loading_the_tunnel'); } },
  { at: 'connecting', get label() { return tr('app_connecting_to_the_relay'); } },
  { at: 'verifying', get label() { return tr('app_checking_your_invite'); } },
];

interface Props {
 onAdmin?: (code:string)=>void;
  state: SessionState;
  offline?: boolean;
  dispatch: (e: SessionEvent) => void;
}

/** A host that logs prompts must say so before the reader types, not in a settings sheet. */
interface Disclosure {
  me: Me;
  accept: () => void;
}

export default function Connect(props: Props) {
  return <LanguageProvider><ConnectBody {...props} /></LanguageProvider>;
}

function ConnectBody({ state, dispatch, offline = false, onAdmin }: Props) {
  const { t } = useLanguage();
  const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const dev = import.meta.env.DEV;
  const [remembered, setRemembered] = useState(() => load<string>(KEYS.invite, ''));
  // The link and the dev query seed only the first mount; a later card holds what is remembered — nothing, after "Paste a new code".
  const [text, setText] = useState(
    () => (autoconnected ? remembered : HASH_INVITE || (dev ? (params.get('invite') ?? '') : '') || remembered),
  );
  const [direct, setDirect] = useState(() => dev && params.has('direct'));
  const [disclosure, setDisclosure] = useState<Disclosure | null>(null);
  // A returning reader is not a stranger: this is what we already know about their last host.
  const [lastHost] = useState<LastHost | null>(() => load<LastHost | null>(KEYS.lastHost, null));
  const [chats] = useState(() => (lastHost ? countChats(lastHost.scope) : 0));
  const [showCode, setShowCode] = useState(false);
  const [pasting, setPasting] = useState(false);
  // Read once: whether this browser will hand us the clipboard at all decides whether the button
  // exists, and a button that appears halfway through a visit is a state nobody asked for.
  const [clipboard] = useState(() => clipboardReader(typeof navigator === 'undefined' ? null : navigator));
  const fieldId = `${useId()}code`;
  const attempt = useRef(0);
  const wasOffline = useRef(offline);
  /** The bridge's own progress lines for the attempt in flight: the witness the failure copy reads (033). */
  const handshake = useRef<string[]>([]);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (offline) { attempt.current++; setDisclosure(null); wasOffline.current = true; return; }
    const resumed = wasOffline.current; wasOffline.current = false;
    // A link is consent; so is a return visit, unless the reader's last move was Disconnect
    // (022 promise 5) — then the card waits for them.
    if (
      (!autoconnected || resumed) &&
      (state.name === 'idle' || resumed) &&
      !(state.name === 'disconnected' && state.reason?.fatal) &&
      text !== '' &&
      inviteProblem(text) === null &&
      dialsOnArrival(lastHost, HASH_INVITE !== '')
    ) {
      autoconnected = true;
      void connect();
      return;
    }
    field.current?.focus();
    // Entry or network recovery only; typing never starts a dial.
  }, [offline]);

  // The handshake bound (033): 8 s to mark the wait, 20 s to end the attempt the way Cancel does —
  // superseded, so what it opens later is closed. Auto-connect and Connect both pass through here.
  const handshaking = state.name === 'connecting';
  useEffect(() => {
    if (!handshaking) return;
    return boundHandshake(dispatch, () => {
      attempt.current++;
      dispatch({ t: 'abort', error: handshakeFailure(handshake.current, lastHost?.name ?? '') });
    });
  }, [handshaking, lastHost, dispatch]);

  async function connect(): Promise<void> {
    if (offline || networkDown()) return;
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
    if(isAdminCode(raw)){onAdmin?.(raw);return;}
    setDisclosure(null);
    const mine = ++attempt.current;
    handshake.current = [];
    const mode: 'direct' | 'tunnel' = direct ? 'direct' : 'tunnel';
    dispatch({ t: 'start', mode });

    // How far this attempt got, for the failure copy. The prop is a render-time snapshot and this
    // function outlives several renders, so the attempt tracks its own progress.
    let reached: SessionState['name'] = mode === 'direct' ? 'connecting' : 'loadingWasm';
    let transport: Transport | null = null;
    try {
      // Only the tab holding the identity lock may use — or overwrite — the stored tunnel key.
      const exclusive = mode === 'tunnel' ? await claimTunnelIdentity() : true;
      if (mine !== attempt.current || networkDown()) return;
      const saved = exclusive ? load<string>(KEYS.privateKey, '') : '';
      const opened = await openTransport(addr, {
        mode,
        assetBase: import.meta.env.PROD ? `/runtime/${encodeURIComponent(VERSION)}/` : undefined,
        directURL: __DEFAULT_DIRECT_URL__,
        ...(saved ? { privateKey: saved } : {}),
        onWasmProgress: (pct) => mine === attempt.current && dispatch({ t: 'wasmProgress', pct }),
        onWasmLoaded: () => {
          reached = 'connecting';
          if (mine === attempt.current) dispatch({ t: 'wasmLoaded' });
        },
        // Only the last few lines: with the network down the bridge writes five a second.
        onLog: (line) => {
          if (mine === attempt.current) handshake.current = [...handshake.current.slice(-3), line];
        },
      });
      transport = opened.transport;
      reached = 'verifying';
      // Ended by the bound or Cancel meanwhile: closed here, never handed to a newer attempt.
      if (mine !== attempt.current || networkDown()) {
        transport.close();
        return;
      }
      // From here the machine owns it: every path out of `verifying` closes it.
      dispatch({ t: 'sessionUp', transport });
      const me = await getMe(transport, secret);
      if (mine !== attempt.current || networkDown()) return; // cancelled while verifying; the machine closed it
      save(KEYS.invite, raw);
      // What the connect screen may say next time before it has reconnected: a name and a scope,
      // both public. Never the secret (014 promise 9).
      save(KEYS.lastHost, { name: hostName(me), scope: hostScope(addr) } as LastHost);
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
        probed: 0,
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
          : { t: 'abort', error: describeConnectError(err, reached, who, handshake.current) },
      );
    }
  }

  /** Stops the attempt in flight: it becomes a superseded one, and the machine closes what it opened. */
  function cancel(): void {
    attempt.current++;
    dispatch({ t: 'abort', error: null });
  }

  /** An empty card for the next code. Forgets the dead code — a reload must not dial it again — and nothing else (022 promise 6). */
  function newCode(): void {
    forget(KEYS.invite);
    setRemembered('');
    setText('');
    setPasting(true);
    dispatch({ t: 'abort', error: null });
    field.current?.focus();
  }

  /** The one action that removes anything: the code, the tunnel identity, the last host. Never a chat. */
  function forgetInvite(): void {
    forget(KEYS.privateKey, KEYS.lastHost);
    newCode();
  }

  /**
   * The tap is the gesture the clipboard read needs, and the paste is the same event as typing —
   * it goes through `edit`, so the hint, the inline check and Connect's enabled state all answer to
   * it exactly as they answer a key. A refused or empty clipboard leaves the field alone: the reader
   * is already looking at the only other way in.
   */
  async function pasteCode(): Promise<void> {
    if (!clipboard) return;
    let text = '';
    try {
      text = await clipboard.readText();
    } catch {
      /* denied, or nothing to read: the field is still there to type into */
    }
    if (text.trim() !== '') edit(text.trim());
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
  // The card after a revoke, or the empty one for the next code, says what the returning card says
  // (024 promise 1): the chats are keyed to the host, so a new code from it opens the same drawer.
  const kept = !busy && !returning && lastHost !== null && chats > 0;
  const who = lastHost?.name?.trim() ?? '';
  // A revoked or unrecognised invite cannot be retried; the only move is a new code from the host.
  const needsNewCode = failure?.fatal === true;
  // The one button that removes anything says what it removes, and what it keeps (022 promise 6).
  const forgetHint = (
    <p className="field-hint">{tr('app_forget_removes_the_code_and_this_device_s_tunnel')}</p>
  );

  /**
   * The field shows the whole code. Three rows is its floor — the mock's 85 px field, which an empty
   * one and every short code keep — and a code that needs a fourth row gets it rather than scrolling
   * its first line out of sight. It needs saying because Paste reserves the top-right corner on
   * every line (the mock reserves it on the first line alone, which a `<textarea>` cannot do), and
   * that is enough to push a real 90-character invite past three rows at 390 px. A code the reader
   * cannot read is the one thing this card must never show them.
   */
  useEffect(() => {
    const el = field.current;
    if (!el) return;
    function fit(box: HTMLTextAreaElement): void {
      box.style.height = 'auto'; // back to the rows attribute, so the box can shrink as well as grow
      // Everything here is border-box, so the frame's own two hairlines are part of the height and
      // scrollHeight — which is content plus padding — is two pixels short of holding the last line.
      box.style.height = `${box.scrollHeight + box.offsetHeight - box.clientHeight}px`;
    }
    fit(el);
    // The code also rewraps when the *box* changes width — a window dragged narrower, a phone
    // turned — and a height measured at the old width leaves the last line under the frame, which
    // is the one thing this field must not do. Width only: the observer sees the height we just
    // set as well, and re-fitting on that would be a loop.
    if (typeof ResizeObserver === 'undefined') return; // jsdom, and browsers older than the app's floor
    let width = el.clientWidth;
    const ro = new ResizeObserver(() => {
      if (el.clientWidth === width) return;
      width = el.clientWidth;
      fit(el);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [text, known, showCode, busy, failure]);

  if (disclosure) return <LogPromptsGate me={disclosure.me} onAccept={disclosure.accept} />;

  // While an attempt runs the card says what is happening and offers the one thing that makes
  // sense meanwhile. The reader never sees a field they cannot type into.
  if (busy) {
    return (
      <Page>
        <CardHead />
        {/* News, not the pitch: this one is the card's own in both layouts. */}
        <p className="pitch">
          {state.name === 'connecting' && state.slow ? (who ? tr('app_still_connecting_host', { host: who }) : tr('app_still_connecting')) : (who ? tr('app_connecting_host', { host: who }) : tr('app_connecting'))}
        </p>
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
            {tr('app_cancel')}
          </button>
        </div>
        <CardFoot who={who} />
      </Page>
    );
  }

  return (
    <Page>
      <CardHead />
      {returning ? (
        <p className="pitch">
          <strong>{tr('app_welcome_back')}</strong> {tr(chats === 1 ? 'app_kept_chat' : 'app_kept_chats', { count: chats, host: who || tr('app_your_host') })}
        </p>
      ) : kept ? (
        <p className="pitch">
          {tr(chats === 1 ? 'app_kept_chat' : 'app_kept_chats', { count: chats, host: who || tr('app_your_host') })}
        </p>
      ) : (
        // The one line the statement column already carries: on the page the reader meets it once,
        // on the left, and the card gets on with the task (039).
        <p className="pitch card-only">
          <Copy name="h_pitch" />
        </p>
      )}

      {/* Never above a failure: a code that just failed is not "ready" (020 promise 5). */}
      {HASH_INVITE !== '' && text === HASH_INVITE && !failure && (
        <p className="notice">{tr('app_invite_from_your_link_is_ready')}</p>
      )}

      {known && !showCode ? (
        <div className="field">
          <span className="field-label"><Copy name="f_label" /></span>
          <div className="masked">
            <code>{maskInvite(text)}</code>
            <button className="ghost tiny" onClick={() => setShowCode(true)}>
              {tr('app_show')}
            </button>
          </div>
        </div>
      ) : (
        <>
          {/* Not a wrapping `<label>`: Paste lives inside the field's frame, and a button inside
              the label is part of the label, which made the field announce itself as "Invite code
              Paste". `htmlFor` associates the two without nesting one control in the other. */}
          <div className="field tight">
            <label className="field-label" htmlFor={fieldId}>
              <Copy name="f_label" />
            </label>
            {/* The frame is the positioning context: Paste sits inside it, top-right, on the
                field's own text grid, and the textarea reserves that corner so a long code can
                never run under the label (039 comfort 2). */}
            <div className={`field-wrap${clipboard ? ' has-paste' : ''}`}>
              <textarea
                id={fieldId}
                ref={field}
                value={text}
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                rows={3}
                placeholder="ic1.…"
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
              {/* Absent, not disabled, where the browser will not hand over the clipboard. */}
              {clipboard && (
                <button type="button" className="paste" onClick={() => void pasteCode()}>
                  <Copy name="f_paste" />
                </button>
              )}
            </div>
          </div>
          <Hint text={text} />
        </>
      )}
      {formatError && <p className="inline-error">{formatError}</p>}

      <div className="connect-actions">
        {/* A code the host has revoked gets no Connect: pressing it could only reproduce the
            failure below, whose own button is the one that works (020 promise 5). */}
        <button
          className="primary"
          onClick={() => void connect()}
          disabled={offline || text.trim() === '' || malformed || needsNewCode}
        >
          {isAdminCode(text)?tr('app_open_console'):returning ? tr('app_reconnect') : <Copy name="f_connect" />}
        </button>
        {/* While a failure is showing, the same action lives inside it, next to the reason. */}
        {remembered !== '' && !failure && (
          <button className="ghost" onClick={forgetInvite}>
            {tr('app_forget_this_invite')}
          </button>
        )}
      </div>
      {remembered !== '' && !failure && forgetHint}

      {offline && <p className="notice" role="status">{tr('app_offline_card')}</p>}
      {!offline && failure && (
        <div className="failure" role="alert">
          <strong>{failure.title}</strong>
          <p>{failure.detail}</p>
          {failure.hostSaid && (
            <details className="host-said">
              <summary>{tr('app_details')}</summary>
              <p>{failure.hostSaid}</p>
            </details>
          )}
          <div className="connect-actions">
            {needsNewCode ? (
              <button className="primary" onClick={newCode}>
                {tr('app_paste_a_new_code')}
              </button>
            ) : (
              <button className="ghost" onClick={() => void connect()}>
                {tr('app_try_again')}
              </button>
            )}
            {remembered !== '' && !needsNewCode && (
              <button className="ghost" onClick={forgetInvite}>
                {tr('app_forget_this_invite')}
              </button>
            )}
          </div>
          {remembered !== '' && !needsNewCode && forgetHint}
        </div>
      )}

      {/* The one line that answers "I don't have a code" — the stranger's question, in the place
          they reach it: after the action that is no use to them yet (039 comfort 4). */}
      <p className="quiet" data-copy="f_quiet" dangerouslySetInnerHTML={{ __html: t.f_quiet.replaceAll('href="#"', `href="${HOST_URL}"`) }} />

      <CardFoot who={who} />

      {dev && (
        <label className="devmode">
          <input type="checkbox" checked={direct} onChange={(e) => setDirect(e.target.checked)} />
          {tr('app_direct_mode_dev', { url: __DEFAULT_DIRECT_URL__ })}
        </label>
      )}
    </Page>
  );
}

/**
 * Each connection state shares the hero and the site below it. On phones the card carries the
 * fold/language links; the page footer owns version and attribution at every width.
 * A logging host's explicit disclosure replaces the hero's default privacy promise.
 */
function Page({ children, promise }: { children: ReactNode; promise?: string }) {
  const { lang } = useLanguage();
  return (
    <div className={`page landing-page ${lang}`} lang={lang === 'zh' ? 'zh-Hans' : 'en'}>
    <div className="landing-hero">
      <header className="page-head page-only">
        <div className="page-head-in">
          <span className="page-brand">
            <Mark size={24} />
            <span className="wordmark">{PRODUCT_NAME}</span>
          </span>
          {/* Two destinations plus the page's language choice. */}
          <nav className="page-nav">
            <a href={HOST_URL}><Copy name="nav_host" /></a>
            <a href={SOURCE_URL}><Copy name="nav_src" /></a>
            <LanguageLinks />
          </nav>
        </div>
      </header>

      <main className="page-main">
        <div className="page-cols">
          {/* The whole argument, readable without touching the field: this is what the stranger
              from the launch post came for. Same composition as the social card. */}
          <section className="statement page-only">
            <Mark size={48} />
            <h1 className="display"><Copy name="h_display" /></h1>
            <p className="lead">
              <Copy name="h_lead" />
            </p>
            <p className="promise">{promise ?? <Copy name="h_promise" />}</p>
            <hr className="rule2" />
            <p className="facts"><Copy name="h_facts" /></p>
          </section>

          <div className="connect">
            <div className="connect-card">{children}</div>
          </div>
        </div>
      </main>

      <FoldStrip />
    </div>
      <Landing />
      <footer className="page-foot landing-footer">
        <div className="page-foot-in">
          <div className="foot-line">
            <Copy name="version" /> {VERSION} · MIT · <a href={SOURCE_URL}><Copy name="source" /></a> ·{' '}
            <About tail={<><span> · </span><Copy name="made" /></>} />
          </div>
          <LanguageLinks />
        </div>
      </footer>
    </div>
  );
}

/**
 * The mark, from the one file that owns it (public/favicon.svg, which web/dev/brand.mjs renders
 * every other raster from), so the mark contest still changes exactly one file. That file carries
 * its own `prefers-color-scheme` switch — ink on paper, paper on black — which is why it can be an
 * image here and still answer to the reader's scheme.
 */
function Mark({ size, extra = '' }: { size: number; extra?: string }) {
  // Decorative: the wordmark next to it, or the headline under it, is what says the name.
  return <img className={`mark${extra ? ` ${extra}` : ''}`} src="/favicon.svg" alt="" width={size} height={size} />;
}

/** The card's own head, which the page's left column says instead above 900 px. */
function CardHead() {
  return (
    <>
      <Mark size={48} extra="card-only" />
      <h1 className="card-only">{PRODUCT_NAME}</h1>
    </>
  );
}

/** The phone card carries the privacy line and the fold; the page footer owns the small print. */
function CardFoot({ who }: { who: string }) {
  return (
    <>
      <p className="privacy card-only">{who ? privacy(who, false) : <Copy name="h_promise" />}</p>
      <div className="about card-only">
        {/* On phones the fold links live in the card; the build and attribution are in the footer. */}
        <div className="build">
          <a href="#p-s1"><Copy name="h_down" /></a><LanguageLinks />
        </div>
      </div>
    </>
  );
}

/**
 * Whose library the tunnel is — "are you Tailscale?" is this audience's first question, and BSD-3
 * clause 3 says the answer must be reachable. It was a permanent third line on the card; as a
 * disclosure it is one keystroke from every screen and on none of them by default (039 comfort 5).
 *
 * The word is inline in the small print's line and the sentence is a block after it, which is why
 * the two are siblings rather than nested: a block inside an inline `<details>` splits the line in
 * two even while it is closed, and "· Made by 2185 Lab" would sit under the attribution instead of
 * beside About. The sibling combinator opens it from the same `[open]`, and `aria-controls` keeps
 * the pair one control and its content for anyone not reading it by eye.
 */
function About({ tail = '' }: { tail?: ReactNode }) {
  const body = `${useId()}about`;
  return (
    <>
      <details className="about-disc">
        <summary aria-controls={body}><Copy name="about" /></summary>
      </details>
      {/* Whatever the line still has to say goes through here, so the sentence stays its last
          element and opens under the whole line rather than in the middle of it. */}
      {tail}
      <p className="about-body" id={body}>
        <Copy name="ft_about" />
      </p>
    </>
  );
}

/**
 * The line under the field, in the three states of `inviteHint` and no others: it is a pure
 * function of what is in the field, so there is nothing here to get out of step with the parser
 * Connect itself runs (039 comfort 3, concept budget 0).
 */
function Hint({ text }: { text: string }) {
  const { t } = useLanguage();
  const { state, host } = inviteHint(text);
  if(isAdminCode(text)&&state==='valid')return <p className="code-hint">{tr('app_admin_hint',{host})}</p>;
  if (state === 'empty') return <p className="code-hint"><Copy name="f_hint_empty" /></p>;
  if (state === 'invalid') {
    return <p className="code-hint bad">{tr('app_invalid_invite_hint', { product: PRODUCT_NAME })}</p>;
  }
  return (
    <p className="code-hint">
      <span dangerouslySetInnerHTML={{ __html: t.f_hint.replace(/<span class="host">.*?<\/span>/, '') }} /><span className="host">{host}</span>
    </p>
  );
}

/**
 * The host runs with --log-prompts. BELIEFS.md says that flag "says so loudly": the reader learns it
 * here, in place of the promise this page just made them, and chooses before typing anything.
 *
 * "In place of" is literal, and above 900 px it takes saying: the statement column carries the
 * privacy sentence, so a page that only added the alert would state the promise and its correction
 * side by side, a few centimetres apart, at the moment of consent. The column's sentence is
 * therefore the `--log-prompts` variant of the same line — one sentence, opposite fact, said once
 * (007 promise 12; `privacyLine`). Below 900 px the card's own privacy line is not rendered at all
 * in this state, which is the same promise kept the way the card keeps it.
 */
function LogPromptsGate({ me, onAccept }: { me: Me; onAccept: () => void }) {
  return (
    <Page promise={privacy(hostName(me), true)}>
      <CardHead />
      <div className="failure" role="alert">
        <strong>{tr('app_host_records_input', { host: hostName(me) || tr('app_this_host') })}</strong>
        <p>
          {tr('app_this_host_is_running_with_prompt_logging_on_everything')}</p>
        <p className="dim">
          <Text name="app_connected_identity" values={{ name: <code>{me.key.name}</code>, id: me.key.id }} />
        </p>
        <div className="connect-actions">
          <button className="primary" onClick={onAccept}>
            {tr('app_i_understand_start_chatting')}
          </button>
          <button className="ghost" onClick={() => location.reload()}>
            {tr('app_not_now')}
          </button>
        </div>
      </div>
    </Page>
  );
}

function stepDetail(state: SessionState, i: number, at: number): string {
  if (i < at) return tr('app_done_lowercase');
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
function describeConnectError(err: unknown, at: SessionState['name'], host: string, log: string[]): FriendlyError {
  if (at === 'loadingWasm') {
    return {
      title: tr('app_could_not_load_the_tunnel'),
      detail: tr('app_reload_the_page_if_it_keeps_failing_this_copy'),
      ...(err instanceof Error && err.message.trim() !== '' ? { hostSaid: err.message.trim() } : {}),
    };
  }
  if (at === 'connecting') {
    // The bridge gave up before the bound did (its own 60 s, or an address it could not read):
    // its last word joins the log it wrote, and the copy is the one the bound would have used.
    const said = err instanceof Error ? err.message.trim() : '';
    return handshakeFailure(said === '' ? log : [...log, said], host);
  }
  return describeError(err, host);
}
