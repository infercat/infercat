---
id: 015
title: Capability seam — /me advertises capabilities, keys carry a tools allowlist, metered tool routes; first host tool = web search
kind: sensitive
size: 3
status: draft
updated: 2026-09-02
release: after-launch
---

# 015 — Capability seam and the first host tool (draft; after launch)

**Why.** The vision was reframed on 2026-09-02: the host shares an AI — a model plus named capabilities —
not just inference. The client runs the agent loop; the host runs tools behind the gateway. This ticket
is the seam everything else hangs on, plus the one tool with the best value-to-cost ratio.

**Binding (to be priced when dispatched).**
1. `/me.host.capabilities`: `[{name, kind: "tool", budget:{...}, remaining:{...}}]`; `keys.Limits.Tools`
   allowlist (empty = none; the host opts each friend in by name: `keys limits alice --tools search`).
2. Tool routes under the gateway, same key, same pipeline stages (admitKey → budget → settle), each a
   separate `outcome` row and usage endpoint: `POST /v1/tools/search` first. Protection 1 holds: the tunnel
   still exposes only the gateway; a tool is a metered route the host opted a friend into.
3. Web search with the host's own API key (`serve --search-provider brave|exa|tavily --search-key …`,
   persisted at 0600), per-key daily call budget, results returned in a stable shape the client can
   feed back as a tool result. No content logged unless `--log-prompts`.
4. Client: a small agent loop behind a `ModelClient`/`ToolRunner` seam (tool calls from the model →
   browser tools or host tool routes → results → next turn), a "Search" capability chip in the header
   when advertised, and the tool-call/result rendering in the thread. Browser-side tools first: document
   drop (File API), calculator/date.
5. Evidence: gateway tests for the route under the pipeline; client vitests for the loop; a live run
   where a friend's question triggers a search through the host's key and the budget decrements.

**Out of scope (own tickets, founder-gated):** code sandboxes (Docker-class isolation, per-friend
workspaces, CPU/time budgets), host-side browsing, MCP servers on the host.

## Log

## Report
