import { tr } from './i18n/text';
// Invite parsing, mirroring internal/invite on the Go side (docs/ARCHITECTURE.md §Invite format):
//
//   ic1.<legacy tailcat address>.<secret> or ic2.<PSK tailcat address>.<secret>
//
// The blob and the secret are base64url and never contain a dot, so splitting on "." is exact.
// Whitespace around the whole string is trimmed; everything else is case-sensitive. The checks and
// their order match internal/invite.Decode exactly — prefix, part count, empty parts, address,
// secret — and each code below corresponds to one of its error values. src/invite.test.ts runs the
// Go test table verbatim; the one deliberate difference is `empty`, which the Go side reports as a
// prefix error and this side turns into "paste your invite" copy for an untouched field.
import { INVITE_PREFIX } from './product';

export type InviteErrorCode =
  | 'empty' // TS only: the field has not been filled in
  | 'missing_prefix' // invite.ErrPrefix
  | 'newer_version' // invite.ErrPrefix, wrapped with "needs a newer app"
  | 'wrong_part_count' // invite.ErrParts
  | 'empty_part' // invite.ErrEmptyPart
  | 'bad_addr' // invite.ErrAddr
  | 'bad_secret'; // invite.ErrSecretChars

export class InviteError extends Error {
  readonly code: InviteErrorCode;
  constructor(code: InviteErrorCode, message: string) {
    super(message);
    this.name = 'InviteError';
    this.code = code;
  }
}

export interface Invite {
  /** The tailcat ConnBlob to dial. */
  addr: string;
  /** The gateway bearer secret. */
  secret: string;
}

const BASE64URL = /^[A-Za-z0-9_-]+$/;
// The prefix family without its version number ("ic" of "ic1"), from the constant: the family
// changes with the product name (037), and the Go side derives it the same way.
const VERSION_TAG = new RegExp(`^${INVITE_PREFIX.replace(/\d+$/, '')}(\\d+)$`);
const PSK_PREFIX = INVITE_PREFIX.replace(/\d+$/, '2');
const NEWER = () => tr('app_this_invite_needs_a_newer_version_of_the_app');

export function encodeInvite(addr: string, secret: string, prefix = INVITE_PREFIX): string {
  return `${prefix}.${addr}.${secret}`;
}

export function decodeInvite(raw: string): Invite {
  const s = raw.trim();
  if (s === '') {
    throw new InviteError('empty', tr('app_paste_the_invite_code_your_host_sent_you'));
  }
  const parts = s.split('.');
  // The prefix is checked before the part count, so an invite from a newer app says so even if
  // that app's layout differs — same order as the Go side.
  const tag = parts[0] as string;
  if (tag !== INVITE_PREFIX && tag !== PSK_PREFIX) {
    if (isNewerVersion(tag)) throw new InviteError('newer_version', NEWER());
    throw new InviteError(
      'missing_prefix',
      tr('app_invite_bad_prefix', { prefix: INVITE_PREFIX, found: clip(tag) }),
    );
  }
  if (parts.length !== 3) {
    throw new InviteError(
      'wrong_part_count',
      tr('app_invite_part_count', { prefix: INVITE_PREFIX, count: parts.length }),
    );
  }
  const addr = parts[1] as string;
  const secret = parts[2] as string;
  if (addr === '' || secret === '') {
    throw new InviteError('empty_part', tr('app_this_invite_is_missing_a_part_it_looks_cut'));
  }
  if (!addr.startsWith('tc') || addr.length === 2 || !BASE64URL.test(addr)) {
    throw new InviteError('bad_addr', tr('app_the_address_in_the_middle_of_this_invite_is'));
  }
  if (!BASE64URL.test(secret)) {
    throw new InviteError(
      'bad_secret',
      tr('app_the_secret_at_the_end_of_this_invite_contains'),
    );
  }
  return { addr, secret };
}

/** "ic3" and up mean the host is ahead of us. "ic", "icx", "ic0", "ic01" and overflow do not. */
function isNewerVersion(tag: string): boolean {
  const digits = VERSION_TAG.exec(tag)?.[1];
  if (digits === undefined) return false;
  const n = Number(digits);
  return Number.isSafeInteger(n) && n > 2;
}

function clip(s: string): string {
  return s.length > 12 ? `${s.slice(0, 12)}…` : s;
}

/**
 * The invite in a link the host's CLI printed: `<app>/#ic1.…`. Returns '' for any fragment that is
 * not one, and never throws — a fragment is attacker-shaped input like any other. The caller wipes
 * it from the address bar afterwards: a secret does not belong in history or in a shared screenshot.
 */
export function inviteFromHash(hash: string): string {
  let raw = hash.replace(/^#/, '');
  try {
    raw = decodeURIComponent(raw);
  } catch {
    /* a malformed percent escape is not an invite */
  }
  raw = raw.trim();
  return raw.startsWith(`${INVITE_PREFIX}.`) || raw.startsWith(`${PSK_PREFIX}.`) ? raw : '';
}

/**
 * What the line under the field says about what is in the field, in three states, as a pure
 * function of the text through the parser above (039). `host` is the first six characters of the
 * address — enough for a reader to recognise the machine their friend named, and nothing a stranger
 * could dial: the address is public, the secret is the half this never touches.
 */
export type HintState = 'empty' | 'valid' | 'invalid';

export interface InviteHint {
  state: HintState;
  /** `tco2Fw…` when the text parses, '' otherwise. */
  host: string;
}

export function inviteHint(text: string): InviteHint {
  if (text.trim() === '') return { state: 'empty', host: '' };
  try {
    return { state: 'valid', host: `${decodeInvite(text.trim().startsWith('ia1.')?'ic1.'+text.trim().slice(4):text).addr.slice(0, 6)}…` };
  } catch {
    return { state: 'invalid', host: '' };
  }
}

/**
 * A remembered invite is still a secret sitting in a text box on a screen somebody may be sharing.
 * It is shown as `ic1.tco2…N96as` — enough for the reader to recognise as theirs, not enough for
 * anyone to use — until they ask for the rest (014 promise 9).
 */
export function maskInvite(invite: string): string {
  const parts = invite.trim().split('.');
  if (parts.length < 3) return invite.trim();
  return `${parts[0] ?? ''}.${(parts[1] ?? '').slice(0, 4)}…${(parts[2] ?? '').slice(-5)}`;
}
