// Mailing-list persistence only: no mail is sent from this endpoint.
// KV's eventually consistent cooldown is a simple abuse brake, not an exact global quota.
export async function onRequestPost({ request, env }) {
  const reply = (status) => Response.json({ ok: status === 200 }, {
    status, headers: { 'Cache-Control': 'no-store', ...(status === 429 ? { 'Retry-After': '60' } : {}) },
  });
  const origin = request.headers.get('Origin');
  if (origin && origin !== new URL(request.url).origin) return reply(403);
  let data;
  try {
    const body = await request.text();
    if (body.length > 2048) return reply(400);
    data = JSON.parse(body);
  } catch { return reply(400); }
  const email = typeof data?.email === 'string' ? data.email.trim().toLowerCase() : '';
  if (email.length > 254 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || !['en', 'zh'].includes(data.lang)) return reply(400);
  try {
    const key = `email:${await digest(email)}`;
    // A retry after a lost response keeps the first record and gives the same thanks state.
    if (await env.SIGNUPS.get(key)) return reply(200);
    const ip = request.headers.get('CF-Connecting-IP') || 'local';
    const rate = `rate:${await digest(ip)}`;
    if (await env.SIGNUPS.get(rate)) return reply(429);
    const record = {
      email, lang: data.lang, from: 'landing-roadmap',
      // The server supplies the receipt time; client clocks are not the signup order.
      ts: new Date().toISOString(),
    };
    await env.SIGNUPS.put(key, JSON.stringify(record), { metadata: record });
    await env.SIGNUPS.put(rate, '1', { expirationTtl: 60 });
    return reply(200);
  } catch { return reply(503); }
}

async function digest(value) {
  const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value));
  return Array.from(new Uint8Array(bytes), (b) => b.toString(16).padStart(2, '0')).join('');
}
