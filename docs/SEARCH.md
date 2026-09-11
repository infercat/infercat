# Search in a chat

English · [简体中文](SEARCH.zh-CN.md)

The host can give an explicitly opted-in chat two tools: `web_search` and, when an
image engine is available to that key, `make_image`. Search uses the operator's
[Exa](https://exa.ai/) account. It sends the query to Exa, not the conversation,
invite, friend name or key id. Exa's own account charges apply.

Store the Exa API key in an owner-only file outside the repository. Add this member
to the host's existing `config.json`, preserving its other settings:

```json
{"search":{"key_file":"/absolute/path/to/exa.key"}}
```

A relative path is relative to the host's data directory. `infercat serve` reads
the file once at startup; restart after rotating it. A configured unreadable,
empty or invalid file prevents startup with a sanitized error. Removing `search`
disables the tool. The credential itself is never persisted in host config or
sent to friends. A search-only host needs no image engine.

A client requests tools explicitly on `POST /v1/chat/completions`:

```json
{"model":"your-model","messages":[{"role":"user","content":"What did llama.cpp release this week? Then draw me a fox."}],"stream":true,"host_tools":["web_search","make_image"]}
```

The host offers only requested names that are available; absent/empty opt-in leaves
ordinary chat unchanged. `/me.host_tools` advertises available names independently
of the turn's selection and remaining budget. Caller `tools` or `tool_choice`
cannot be combined with a nonempty host-tools selection. This adds no tools to
Codex, OpenCode or other clients that manage their own tool calls.

Each turn allows three tool rounds, at most two dispatched searches, and at most
four separately metered model calls. `web_search` takes a nonempty `query` and an
optional integer `count` from 1 to 5 (default 3). A final call is asked to answer
without tools. Malformed, unknown or multiple calls execute nothing and give the
model a tool-error result before that final answer. A further tool request is
refused; prose already streamed remains visible.

Search has a separate per-key daily budget: 50 by default, zero also means 50,
and a negative value means unlimited. For example:

```sh
infercat keys add alice --search-per-day 20
infercat keys limits alice --search-per-day 100
```

The budget resets at UTC midnight. A dispatched provider request costs one search,
including an empty result, provider error or timeout. Cancellation before dispatch
costs none. An exhausted budget returns `budget_exhausted` (429) with Retry-After
until UTC midnight. `/me` exposes `limits.search_per_day` and
`usage.today_searches`. Model calls retain their normal token/RPM charges; searches
add neither tokens nor RPM.

Each provider call has a 10-second deadline, with no retries or redirects. The host
asks only for search highlights, never fetches result pages, bounds the provider
body to 128 KiB, and gives the model at most 16 KiB of plain text: titles, URLs and
snippets. Empty results and provider failures become tool results so the model can
continue. The visible search step's captured output is exactly that same text.
The `search` usage row records only accounting and timing, never the query or
provider response. Search text belongs to the retained run record. The operator's explicit
`--log-prompts` opt-in retains its disclosed behavior: tool queries and results
appear in prompt logs like any other message text.

Stopping the chat cancels its pending search/model work. Image jobs already
submitted by `make_image` remain independent and finish under their own limits.

App friends can ask in chat: the app requests every host tool the host offers, including search on a search-only host, and Details opens the captured search results.
