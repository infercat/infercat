// GET /try — the one stable address for the public demo invite (docs: hosting/README.md).
// The current invite link lives in KV under `link:try`, so a remint is one `wrangler kv key put`,
// never a deploy and never a commit: nothing in git holds an invite. Browsers keep the fragment
// across a redirect, so the app opens with the code in the box. `?from=try` lets the first-party
// counter attribute the visit. Without a stored link, /try falls back to the landing page.
export async function onRequestGet({ env }) {
  let target = 'https://infercat.ai/?from=try';
  try {
    const link = await env.SIGNUPS.get('link:try');
    if (link && /^https:\/\/infercat\.ai\/?#ic[12]\./.test(link)) {
      target = link.replace(/^https:\/\/infercat\.ai\/?#/, 'https://infercat.ai/?from=try#');
    }
  } catch {}
  return new Response(null, { status: 302, headers: { Location: target, 'Cache-Control': 'no-store' } });
}
