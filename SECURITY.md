# Security

## Reporting

Email **security@2185lab.com** <!-- TODO(address): confirm security@2185lab.com exists before the repo goes public -->
or use GitHub's private vulnerability reporting on this repository (Security → Report a vulnerability).
Please do not open a public issue for something exploitable. You will get an answer within three
days, and a fix or a stated plan within fourteen; you are credited in the changelog unless you ask
not to be.

## What is in scope

The host binary and the web app, and specifically the four things the product promises to protect
(`pm/BELIEFS.md`, Protections):

1. **The host machine.** The tunnel exposes exactly one thing: the gateway. A way to reach anything
   else on the host — another port, a file, the admin socket — through an invite is a vulnerability.
2. **Invite secrets.** Stored hashed on the host, shown once at creation, never logged. A secret that
   lands in a log, an error body, or a usage record is a vulnerability.
3. **Friends' usage data.** The host sees counts and timings only, unless they ran `serve --log-prompts`.
   Prompt or completion text reaching disk without that flag is a vulnerability.
4. **The upstream engine.** A burst must degrade to `429`/`503` with `Retry-After`, never to an
   out-of-memory engine or a stalled queue. A request pattern that stalls or crashes the engine
   through the gateway's limits is a vulnerability.

Also in scope: the invite format and its parsing, the gateway's authentication, the wasm bridge in
the browser, and the release artifacts (checksums, notices, the Homebrew formula).

## What is not

- The inference engine itself (llama.cpp, vLLM, Ollama, LM Studio): report to its project.
- The relay (a DERP server) seeing ciphertext metadata — timing and sizes. Known and accepted for v1.
- Two people sharing one invite: bounded by that key's limits and visible in usage; by design.
- Denial of service against a public relay.

## Versions

Only the latest release is supported. There is no backport branch.
