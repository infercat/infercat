# What the market said about LM Link (read in the founder's browser, 2026-09-03)

LM Link (LM Studio + Tailscale, Feb 2026; iPhone client June 2026) is the nearest existing feature:
"use local models on remote devices", "your remote models, as if they were local", "share LLMs between
devices you control". One person, one account, LM Studio clients only. Three Reddit threads, read with
comment scores. Quotes are short and attributed to the thread, per the site's terms.

## r/LocalLLaMA "LM Link" (launch thread, 57 points, 45 comments)

Top comment (+30): the dream is native smartphone apps so local LLMs work "just the same as the ChatGPT
or Claude apps". Then, in score order, the objections that are our product's exact posture:
- (+13) an account requirement is "a no-go for me"; (+10) "sounds like a way to start profiling your
  activity"; (+10) a password for client confirmation would have sufficed; (+7) "I want them to connect
  without intermediaries" — should work without internet given an IP/port.
- (+6) "why do I need to use tailscale if I'm on my own network? Why does it need to go through them?"
- (+6) "why hide features behind sign ups?"
- (+9) someone had already left LM Studio for another client + Tailscale because it could not act as a
  remote client. (+12) "now they need a distributed inference add on" (not our lane). Several: "now they
  need a phone app"; people were using remote desktop before this.

## r/LocalLLaMA "is LM Link just too uncooked/experimental?" (7 comments)

The poster had SearXNG web search working locally; through the Link, "anything that would require
searching the web fails". The technically correct answer in the thread: tools/MCP "need to be installed
on the client... you're sending your prompt to an inference endpoint", i.e. **the link carries inference,
not tools** — exactly the gap host-side capabilities fill. Also: "lm link sucks, just use tailscale" (+3)
vs. "works great for me" for a machine the user would not trust with full network access (+?).

## r/apple "LM Studio now lets you use your iPhone to talk to local models on your Mac" (253 points, 47 comments)

Mainstream audience. Top serious comment (+86): "actually really great for Mac users", 16 GB machines run
small models. Then (+15): "Through a proprietary intermediary that requires a login. My Mac is already
on the same LAN, let me do this directly without logging in." (+6) "why adding required account
creation?" (+8) "using Tailscale or similar software wins every time". (+2) "it can't use any MCP I
configured. For example web search." (+2, twice) "what is this good for?" — the value is not obvious
to non-hobbyists.

## What this settles

1. **Demand is real and the vocabulary is theirs:** "use my local models on my phone / laptop",
   "remote", "local models", "self-hosted", "without logging in", "directly", "no intermediary".
2. **The three complaints are our three properties:** no account (invite code, not login); no
   intermediary sees anything (relay sees ciphertext; direct path when possible); and tools that
   travel with the AI, not the client (host-side search first).
3. **What they want next is a phone experience "like the ChatGPT app"** — our browser client is that
   on day one, with no install.
4. **Sharing with another person is not even in their frame yet** — LM Link is one account. That is the
   sentence our launch post has to say plainly: give a friend access, not a device.
5. **A mainstream audience asks "what is this good for?"** The post must lead with the job (use your
   home GPU from anywhere / give friends your AI), not the mechanism.
