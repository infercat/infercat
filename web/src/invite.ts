// Invite parsing, mirroring internal/invite on the Go side (docs/ARCHITECTURE.md §Invite format):
//
//   bn1.<tailcat ConnBlob>.<secret>
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
const VERSION_TAG = /^bn(\d+)$/;
const NEWER = 'This invite needs a newer version of the app.';

export function encodeInvite(addr: string, secret: string): string {
  return `${INVITE_PREFIX}.${addr}.${secret}`;
}

export function decodeInvite(raw: string): Invite {
  const s = raw.trim();
  if (s === '') {
    throw new InviteError('empty', 'Paste the invite code your host sent you.');
  }
  const parts = s.split('.');
  // The prefix is checked before the part count, so an invite from a newer app says so even if
  // that app's layout differs — same order as the Go side.
  const tag = parts[0] as string;
  if (tag !== INVITE_PREFIX) {
    if (isNewerVersion(tag)) throw new InviteError('newer_version', NEWER);
    throw new InviteError(
      'missing_prefix',
      `An invite starts with "${INVITE_PREFIX}." — this one starts with "${clip(tag)}".`,
    );
  }
  if (parts.length !== 3) {
    throw new InviteError(
      'wrong_part_count',
      `An invite has three dot-separated parts (${INVITE_PREFIX}.address.secret); this one has ${parts.length}.`,
    );
  }
  const addr = parts[1] as string;
  const secret = parts[2] as string;
  if (addr === '' || secret === '') {
    throw new InviteError('empty_part', 'This invite is missing a part — it looks cut off.');
  }
  if (!addr.startsWith('tc') || addr.length === 2 || !BASE64URL.test(addr)) {
    throw new InviteError('bad_addr', 'The address in the middle of this invite is not a host address.');
  }
  if (!BASE64URL.test(secret)) {
    throw new InviteError(
      'bad_secret',
      'The secret at the end of this invite contains characters that do not belong in it.',
    );
  }
  return { addr, secret };
}

/** "bn2" and up mean the host is ahead of us. "bn", "bnx", "bn0", "bn01" and overflow do not. */
function isNewerVersion(tag: string): boolean {
  const digits = VERSION_TAG.exec(tag)?.[1];
  if (digits === undefined) return false;
  const n = Number(digits);
  return Number.isSafeInteger(n) && n > 1;
}

function clip(s: string): string {
  return s.length > 12 ? `${s.slice(0, 12)}…` : s;
}

/**
 * The invite in a link the host's CLI printed: `<app>/#bn1.…`. Returns '' for any fragment that is
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
  return raw.startsWith('bn1.') ? raw : '';
}
