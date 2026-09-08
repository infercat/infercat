# Contributing

Issues are welcome — a bug, a host that would not start, an invite that would not connect, a
feature you want. The templates ask for the output of `infercat status` and `infercat
version`; they save a round trip.

Pull requests: open an issue or a discussion first, so the change is agreed before the work.
Small fixes (a typo, a wrong sentence in `--help`, a broken link) can skip that. Every PR runs the
same checks CI runs:

```
make check                                        # Go vet/tests + installer fixtures
cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm test && pnpm lint
make notices-check                                # THIRD_PARTY_NOTICES.md is current
make release-dry                                  # every artifact builds, nothing is published
```

A few things this codebase holds to, so a review goes quickly:

- **Surfaces tell the truth.** Copy that says "relayed via nyc · 64 ms" is preferred to a green dot.
  Degraded states show as degraded, with the reason.
- **One concept per thing.** New CLI verbs, flags, config keys, and state files are the scarcest
  budget here; reuse before you add.
- **Fix the cause, keep the failure's shape.** A bugfix comes with the test that would have caught it.
- **Prompts are never logged by default.** Anything that touches what friends send is looked at twice.
- **The product name lives in one constant** on each side (`internal/product/product.go`,
  `web/src/product.ts`). Do not spell it anywhere else.

The project’s engineering rules and decisions are in [docs/PRINCIPLES.md](docs/PRINCIPLES.md).

By contributing you agree that your contribution is licensed under the MIT License, like the rest.

## Building

Run these commands from the repository root:

```
make check        # Go vet/tests + installer fixtures
make build        # bin/infercat
make wasm         # web/public/infercat.wasm (+ wasm_exec.js), needed by the web app
make web          # web/dist (builds the wasm first)
make notices      # regenerate THIRD_PARTY_NOTICES.md; make notices-check verifies it
make release-dry  # goreleaser snapshot for darwin/linux/windows into dist/ (publishes nothing)
make brand        # the icon set and the social-card images, from the shared SVG mark and self-hosted fonts
make launch-check # what a stranger's browser sees: metas, manifest, console, a11y, contrast, shots
```

Web checks: `cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm test && pnpm lint`.
Every binary and archive ships `LICENSE` and `THIRD_PARTY_NOTICES.md`; `infercat version`
prints the stamped version, commit and date, and the web app shows the same version under Settings.
Releases: [docs/RELEASE.md](docs/RELEASE.md). Principles and decisions: [docs/PRINCIPLES.md](docs/PRINCIPLES.md); the seam contract: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

See [web/README.md](web/README.md) for the browser layout, development harness and language tables.
