// The connect screen: the landing page a stranger meets. One sentence about what this is, one
// field, one button, and progress that says what is actually happening. It owns no connection
// state of its own — it dispatches into the session machine (src/session.ts) and renders it.
//
// Two readers arrive at this URL and the screen is one page for both (039). A friend with a code
// wants the field and nothing else; a stranger from the launch post wants to know what this is and
// how to run their own, and a card alone on an empty page is a locked door with no sign. So from
// 900 px up the same card sits inside a page — a header of two links, the product's statement in
// the left column, a footer — and below it the page chrome is gone and the card is the screen, as
// it always was. One DOM, one media query: `Page` renders the frame, and every card state is passed
// through it as children. The left column not moving when the card changes is the stylesheet's half
// of that — the statement is centred on its own box, not on the pair (styles.css, `.statement`).
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { describeError, getMe, hostName, logsPrompts, type FriendlyError, type Me } from '../api';
import { decodeInvite, inviteFromHash, InviteError, inviteHint, maskInvite } from '../invite';
import { HOST_URL, PRODUCT_NAME, privacyLine, SOURCE_URL, VERSION } from '../product';
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

declare const __DEFAULT_DIRECT_URL__: string;

/**
 * An invite handed over as a link (`<app>/#ic1.…`, which the host's CLI prints). Read once, at
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
  /** The bridge's own progress lines for the attempt in flight: the witness the failure copy reads (033). */
  const handshake = useRef<string[]>([]);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    // A link is consent; so is a return visit, unless the reader's last move was Disconnect
    // (022 promise 5) — then the card waits for them.
    if (
      !autoconnected &&
      state.name === 'idle' &&
      text !== '' &&
      inviteProblem(text) === null &&
      dialsOnArrival(lastHost, HASH_INVITE !== '')
    ) {
      autoconnected = true;
      void connect();
      return;
    }
    field.current?.focus();
    // Mount only: this is the entry point, not a reactive form.
  }, []);

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
        // Only the last few lines: with the network down the bridge writes five a second.
        onLog: (line) => {
          if (mine === attempt.current) handshake.current = [...handshake.current.slice(-3), line];
        },
      });
      transport = opened.transport;
      reached = 'verifying';
      // Ended by the bound or Cancel meanwhile: closed here, never handed to a newer attempt.
      if (mine !== attempt.current) {
        transport.close();
        return;
      }
      // From here the machine owns it: every path out of `verifying` closes it.
      dispatch({ t: 'sessionUp', transport });
      const me = await getMe(transport, secret);
      if (mine !== attempt.current) return; // cancelled while verifying; the machine closed it
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
    <p className="field-hint">Forget removes the code and this device’s tunnel identity. Your chats stay.</p>
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
          {`${state.name === 'connecting' && state.slow ? 'Still connecting' : 'Connecting'}${who ? ` to ${who}` : ''}…`}
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
            Cancel
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
          <strong>Welcome back.</strong> Your {chats} {chats === 1 ? 'chat' : 'chats'} with {who || 'your host'}{' '}
          {chats === 1 ? 'is' : 'are'} still on this device.
        </p>
      ) : kept ? (
        <p className="pitch">
          Your {chats} {chats === 1 ? 'chat' : 'chats'} with {who || 'your host'} {chats === 1 ? 'is' : 'are'} still on
          this device.
        </p>
      ) : (
        // The one line the statement column already carries: on the page the reader meets it once,
        // on the left, and the card gets on with the task (039).
        <p className="pitch card-only">
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
        <>
          {/* Not a wrapping `<label>`: Paste lives inside the field's frame, and a button inside
              the label is part of the label, which made the field announce itself as "Invite code
              Paste". `htmlFor` associates the two without nesting one control in the other. */}
          <div className="field tight">
            <label className="field-label" htmlFor={fieldId}>
              Invite code
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
                  Paste
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
      {remembered !== '' && !failure && forgetHint}

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
              <button className="primary" onClick={newCode}>
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
          {remembered !== '' && !needsNewCode && forgetHint}
        </div>
      )}

      {/* The one line that answers "I don't have a code" — the stranger's question, in the place
          they reach it: after the action that is no use to them yet (039 comfort 4). */}
      <p className="quiet">
        No code? Ask a friend who runs {PRODUCT_NAME}, or{' '}
        <span className="nb">
          <a href={HOST_URL}>host your own</a>{' '}
          <span aria-hidden="true">→</span>
        </span>
      </p>

      <CardFoot who={who} />

      {dev && (
        <label className="devmode">
          <input type="checkbox" checked={direct} onChange={(e) => setDirect(e.target.checked)} />
          Direct mode (dev) — talk to {__DEFAULT_DIRECT_URL__} instead of the tunnel
        </label>
      )}
    </Page>
  );
}

/**
 * The frame every card state renders inside. Above 900 px it is the page — a header of exactly two
 * links, the statement on the left, the card on the right, a footer under both; below, it collapses
 * to the card alone and the chrome is not rendered at all (CSS, one media query). The card's
 * content is the only stateful part, which is what keeps the left column still while the card goes
 * from empty to connecting to revoked (039 promise 3).
 *
 * `promise` is the one line of the statement that is not a constant: on a host that logs prompts
 * the sentence in this column is false, and 007 promise 12 says the disclosure *replaces* it rather
 * than sitting next to it. The caller passes the corrected sentence; nothing else about the column
 * changes.
 */
function Page({ children, promise = privacyLine('', false) }: { children: ReactNode; promise?: string }) {
  return (
    <div className="page">
      <header className="page-head page-only">
        <div className="page-head-in">
          <span className="page-brand">
            <Mark size={24} />
            <span className="wordmark">{PRODUCT_NAME}</span>
          </span>
          {/* Two, and never a third: the header is chrome, not a site map (BELIEFS: few concepts). */}
          <nav className="page-nav">
            <a href={HOST_URL}>Host your own</a>
            <a href={SOURCE_URL}>Source</a>
          </nav>
        </div>
      </header>

      <main className="page-main">
        <div className="page-cols">
          {/* The whole argument, readable without touching the field: this is what the stranger
              from the launch post came for. Same composition as the social card. */}
          <section className="statement page-only">
            <Mark size={48} />
            <h1 className="display">Chat with a friend’s GPU.</h1>
            <p className="lead">
              They send you one code; you paste it here. No account, no install, nothing to set up.
            </p>
            <p className="promise">{promise}</p>
            <hr className="rule2" />
            <p className="facts">Self-hosted · end-to-end encrypted · MIT</p>
          </section>

          <div className="connect">
            <div className="connect-card">{children}</div>
          </div>
        </div>
      </main>

      <footer className="page-foot page-only">
        <div className="page-foot-in">
          <div className="foot-line">
            Version {VERSION} · MIT · <a href={SOURCE_URL}>Source on GitHub</a> ·{' '}
            <About tail=" · Made by 2185 Lab" />
          </div>
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

/**
 * The card's foot: the privacy promise and the small print. Both are the page's job above 900 px —
 * the statement carries the promise and the footer carries the small print — so below it the card
 * carries them itself and above it they are not rendered twice.
 */
function CardFoot({ who }: { who: string }) {
  return (
    <>
      <p className="privacy card-only">{privacyLine(who, false)}</p>
      <div className="about card-only">
        {/* A build number and a licence are machine-issued, so they are set in mono (038 Type). */}
        <div className="build">
          Version {VERSION} · MIT · <a href={SOURCE_URL}>Source on GitHub</a> · <About />
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
function About({ tail = '' }: { tail?: string }) {
  const body = `${useId()}about`;
  return (
    <>
      <details className="about-disc">
        <summary aria-controls={body}>About</summary>
      </details>
      {/* Whatever the line still has to say goes through here, so the sentence stays its last
          element and opens under the whole line rather than in the middle of it. */}
      {tail}
      <p className="about-body" id={body}>
        Built on tailcat, Tailscale’s open-source library. {PRODUCT_NAME} is not affiliated with or
        endorsed by Tailscale Inc.
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
  const { state, host } = inviteHint(text);
  if (state === 'empty') return <p className="code-hint">Paste the code your friend sent you.</p>;
  if (state === 'invalid') {
    return <p className="code-hint bad">That doesn’t look like an {PRODUCT_NAME} invite yet.</p>;
  }
  return (
    <p className="code-hint">
      <span className="ck">✓</span> Reads as an invite · host <span className="host">{host}</span>
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
    <Page promise={privacyLine(hostName(me), true)}>
      <CardHead />
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
    </Page>
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
function describeConnectError(err: unknown, at: SessionState['name'], host: string, log: string[]): FriendlyError {
  if (at === 'loadingWasm') {
    return {
      title: 'Could not load the tunnel',
      detail: 'Reload the page; if it keeps failing, this copy of the app was published without its tunnel module.',
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
