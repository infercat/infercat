import { describe, expect, it } from 'vitest';
import { InviteError, decodeInvite, encodeInvite, inviteFromHash, maskInvite, type InviteErrorCode } from './invite';

// The error table below is internal/invite/invite_test.go's TestDecodeErrors, case for case, with
// each Go error value mapped to this side's code. ADDR is a real tailcat ConnBlob shape (base64url
// after "tc"); the Go test builds one from a fresh key, which we cannot do here.
const ADDR = 'tcAgIBAqQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8';
const SECRET = 'YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3';
const OK = `${ADDR}.s3cr3t-_OK`;

describe('invite', () => {
  it('round-trips', () => {
    const s = encodeInvite(ADDR, SECRET);
    expect(s).toBe(`bn1.${ADDR}.${SECRET}`);
    expect(s.split('.')).toHaveLength(3);
    expect(decodeInvite(s)).toEqual({ addr: ADDR, secret: SECRET });
  });

  it('trims surrounding whitespace and nothing else', () => {
    expect(decodeInvite(`  \n bn1.${ADDR}.${SECRET}\t\r\n`)).toEqual({ addr: ADDR, secret: SECRET });
  });

  it('is case-sensitive in the parts it keeps', () => {
    expect(decodeInvite(`bn1.${ADDR}.${SECRET.toUpperCase()}`).secret).toBe(SECRET.toUpperCase());
  });

  const table: [string, string, InviteErrorCode][] = [
    ['empty', '', 'empty'], // Go says ErrPrefix here; see the note in invite.ts
    ['whitespace_only', ' \n\t', 'empty'], // ditto
    ['no_dots', 'bn1', 'wrong_part_count'],
    ['missing_prefix', `${ADDR}.secret`, 'missing_prefix'],
    ['wrong_prefix', `xx1.${OK}`, 'missing_prefix'],
    ['prefix_case', `BN1.${OK}`, 'missing_prefix'],
    ['prefix_bn_only', `bn.${OK}`, 'missing_prefix'],
    ['prefix_bnx', `bnx.${OK}`, 'missing_prefix'],
    ['prefix_bn0', `bn0.${OK}`, 'missing_prefix'],
    ['prefix_bn01', `bn01.${OK}`, 'missing_prefix'],
    ['prefix_huge', `bn99999999999999999999.${OK}`, 'missing_prefix'],
    ['newer_bn2', `bn2.${OK}`, 'newer_version'],
    ['newer_bn10_four_parts', `bn10.${OK}.extra`, 'newer_version'],
    ['two_parts', `bn1.${ADDR}`, 'wrong_part_count'],
    ['four_parts', `bn1.${OK}.extra`, 'wrong_part_count'],
    ['trailing_dot', `bn1.${OK}.`, 'wrong_part_count'],
    ['leading_dot', `.bn1.${OK}`, 'missing_prefix'],
    ['empty_addr', 'bn1..secret', 'empty_part'],
    ['empty_secret', `bn1.${ADDR}.`, 'empty_part'],
    ['both_empty', 'bn1..', 'empty_part'],
    ['addr_no_tc', 'bn1.ABC.secret', 'bad_addr'],
    ['addr_tc_only', 'bn1.tc.secret', 'bad_addr'],
    ['addr_bad_char', 'bn1.tcAB+C.secret', 'bad_addr'],
    ['addr_space', 'bn1.tcAB C.secret', 'bad_addr'],
    ['addr_nul', 'bn1.tcAB\x00C.secret', 'bad_addr'],
    ['addr_unicode', 'bn1.tcABÇ.secret', 'bad_addr'],
    ['secret_plus', `bn1.${ADDR}.sec+ret`, 'bad_secret'],
    ['secret_slash', `bn1.${ADDR}.sec/ret`, 'bad_secret'],
    ['secret_padding', `bn1.${ADDR}.secret=`, 'bad_secret'],
    ['secret_inner_space', `bn1.${ADDR}.sec ret`, 'bad_secret'],
    ['secret_zero_width', `bn1.${ADDR}.sec\u200Bret`, 'bad_secret'],
    ['secret_newline', `bn1.${ADDR}.sec\nret`, 'bad_secret'],
  ];

  it.each(table)('%s', (name, input, code) => {
    let thrown: unknown;
    try {
      decodeInvite(input);
    } catch (err) {
      thrown = err;
    }
    expect(thrown, `${name} should have been rejected`).toBeInstanceOf(InviteError);
    const err = thrown as InviteError;
    expect(err.code).toBe(code);
    // The Go test asserts this both ways: only the "newer" cases may say so.
    expect(err.message.includes('newer version')).toBe(name.startsWith('newer'));
    expect(err.message.length).toBeGreaterThan(10);
  });

  it('accepts any base64url address and secret, as the Go property test does', () => {
    const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
    const pick = (n: number) =>
      Array.from({ length: n }, () => alphabet[Math.floor(Math.random() * alphabet.length)]).join('');
    for (let i = 0; i < 500; i++) {
      const addr = `tc${pick(1 + Math.floor(Math.random() * 400))}`;
      const secret = pick(1 + Math.floor(Math.random() * 100));
      expect(decodeInvite(encodeInvite(addr, secret))).toEqual({ addr, secret });
    }
  });
});

// Promise 14: the host's CLI prints `<app>/#bn1.…`; the page fills the field from it.
describe('inviteFromHash', () => {
  const invite = `bn1.${ADDR}.${SECRET}`;

  it('takes the invite out of a link fragment, with or without the #', () => {
    expect(inviteFromHash(`#${invite}`)).toBe(invite);
    expect(inviteFromHash(invite)).toBe(invite);
  });

  it('accepts a percent-encoded fragment and trims it', () => {
    expect(inviteFromHash(`#%20${encodeURIComponent(invite)}%20`)).toBe(invite);
  });

  it('ignores a fragment that is not an invite, and never throws', () => {
    for (const hash of ['', '#', '#section-2', '#bn2.something', '#%E0%A4%A', '#nope']) {
      expect(inviteFromHash(hash), hash).toBe('');
    }
  });
});

// 014 promise 9: a returning reader sees their code as theirs, not as a secret on display.
describe('maskInvite', () => {
  const invite = `bn1.tco2FwWCCefFIUTGpLZzxLjFkzfi5BnYk9Lr2zui9eL.${'D'.repeat(38)}N96as`;

  it('shows enough to recognise and not enough to use', () => {
    expect(maskInvite(invite)).toBe('bn1.tco2…N96as');
    expect(maskInvite(invite)).not.toContain('DDDD');
    expect(maskInvite(invite).length).toBeLessThan(20);
  });

  it('leaves anything that is not an invite alone rather than pretending to mask it', () => {
    expect(maskInvite('nonsense')).toBe('nonsense');
    expect(maskInvite('  bn1.only-two-parts  ')).toBe('bn1.only-two-parts');
  });
});
