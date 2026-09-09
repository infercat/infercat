// Mirrors internal/invite.Decode. No UI strings or app imports enter the package.
export function decodeInvite(raw: string): { addr: string; secret: string } {
  const parts = raw.trim().split('.');
  if (parts[0] !== 'ic1') throw new TypeError('Expected an ic1 invite; newer formats require a newer client.');
  if (parts.length !== 3) throw new TypeError('An invite has three dot-separated parts.');
  const [ , addr, secret ] = parts;
  if (!addr || !secret) throw new TypeError('The invite has an empty part.');
  if (!/^tc[A-Za-z0-9_-]+$/.test(addr)) throw new TypeError('Invalid invite address.');
  if (!/^[A-Za-z0-9_-]+$/.test(secret)) throw new TypeError('Invalid invite secret.');
  return { addr, secret };
}
