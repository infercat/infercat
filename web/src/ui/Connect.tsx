// The connect screen: the landing page a stranger meets. One sentence about what this is, one
// field, one button, and progress that says what is actually happening.
import { useEffect, useRef, useState } from 'react';
import type { Live } from '../App';
import { describeError, getMe, type FriendlyError } from '../api';
import { decodeInvite, InviteError } from '../invite';
import { PRODUCT_NAME } from '../product';
import { forget, KEYS, load, save } from '../storage';
import { openTransport, type ConnectStage } from '../transport';

declare const __DEFAULT_DIRECT_URL__: string;

type StepId = 'wasm' | 'relay' | 'handshake' | 'verify';

const STEPS: { id: StepId; label: string }[] = [
  { id: 'wasm', label: 'Loading the tunnel' },
  { id: 'relay', label: 'Connecting to the relay' },
  { id: 'handshake', label: 'Encrypted handshake' },
  { id: 'verify', label: 'Checking your invite' },
];

interface Props {
  notice: string | null;
  onConnected: (live: Live) => void;
}

export default function Connect({ notice, onConnected }: Props) {
  const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const dev = import.meta.env.DEV;
  const [remembered, setRemembered] = useState(() => load<string>(KEYS.invite, ''));
  const [text, setText] = useState(() => (dev ? (params.get('invite') ?? '') : '') || remembered);
  const [direct, setDirect] = useState(() => dev && params.has('direct'));
  const [formatError, setFormatError] = useState<string | null>(null);
  const [stage, setStage] = useState<ConnectStage | null>(null);
  const [failure, setFailure] = useState<FriendlyError | null>(null);
  const stageRef = useRef<StepId>('wasm');
  const once = useRef(false);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    field.current?.focus();
    if (dev && params.has('autoconnect') && !once.current) {
      once.current = true;
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
    setFailure(null);
    const mode = direct ? 'direct' : 'tunnel';
    stageRef.current = mode === 'direct' ? 'verify' : 'wasm';
    setStage(mode === 'direct' ? { name: 'verify' } : { name: 'wasm', pct: null });

    try {
      const saved = load<string>(KEYS.privateKey, '');
      const { transport, path, privateKeyJSON } = await openTransport(addr, {
        mode,
        directURL: __DEFAULT_DIRECT_URL__,
        ...(saved ? { privateKey: saved } : {}),
        onStage: (s) => {
          if (s.name !== 'connected') stageRef.current = s.name;
          setStage(s);
        },
      });
      stageRef.current = 'verify';
      setStage({ name: 'verify' });
      const me = await getMe(transport, secret);
      save(KEYS.invite, raw);
      if (privateKeyJSON) save(KEYS.privateKey, privateKeyJSON);
      setRemembered(raw);
      setStage({ name: 'connected' });
      onConnected({ transport, secret, me, path, mode });
    } catch (err) {
      setStage(null);
      setFailure(describeConnectError(err, stageRef.current));
    }
  }

  const busy = stage !== null;
  const steps = direct ? STEPS.filter((s) => s.id === 'verify') : STEPS;
  const at = stage ? steps.findIndex((s) => s.id === stage.name) : -1;

  return (
    <main className="connect">
      <div className="connect-card">
        <h1>{PRODUCT_NAME}</h1>
        <p className="pitch">
          Chat with a friend’s GPU. They send you one code; you paste it here. No account, no
          install, nothing to set up.
        </p>

        {notice && <p className="notice">{notice}</p>}

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
              if (e.key === 'Enter' && !e.shiftKey) {
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
          {remembered !== '' && !busy && (
            <button
              className="ghost"
              onClick={() => {
                forget(KEYS.invite, KEYS.privateKey);
                setRemembered('');
                setText('');
                setFailure(null);
                field.current?.focus();
              }}
            >
              Forget this invite
            </button>
          )}
        </div>

        {busy && (
          <ol className="steps" aria-live="polite">
            {steps.map((step, i) => (
              <li key={step.id} className={i < at ? 'done' : i === at ? 'now' : 'next'}>
                <span>{step.label}</span>
                <span className="step-detail">{stepDetail(stage, i, at)}</span>
              </li>
            ))}
          </ol>
        )}

        {failure && (
          <div className="failure" role="alert">
            <strong>{failure.title}</strong>
            <p>{failure.detail}</p>
            <button className="ghost" onClick={() => void connect()}>
              Try again
            </button>
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

function stepDetail(stage: ConnectStage | null, i: number, at: number): string {
  if (i < at) return 'done';
  if (i !== at || !stage) return '';
  if (stage.name === 'wasm') return stage.pct === null ? '…' : `${stage.pct}%`;
  if (stage.name === 'handshake' && stage.path) return `${Math.round(stage.path.rttMs)} ms`;
  return '…';
}

/** The same failure means different things depending on how far we got; say the useful thing. */
function describeConnectError(err: unknown, at: StepId): FriendlyError {
  if (at === 'wasm') {
    return {
      title: 'Could not load the tunnel',
      detail:
        err instanceof Error
          ? `${err.message}. Reload the page; if it keeps failing, this copy of the app was published without its tunnel module.`
          : 'Reload the page and try again.',
    };
  }
  if (at === 'relay' || at === 'handshake') {
    return {
      title: 'The host did not answer',
      detail:
        'The invite looks well-formed, so either the host’s machine is asleep or offline, or the relay could not be reached from this network. Ask them to check that the host is running.',
    };
  }
  return describeError(err);
}
