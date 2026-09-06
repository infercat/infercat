# Contributing

Issues are welcome — a bug, a host that would not start, an invite that would not connect, a
feature you want. The templates ask for the output of `infercat status` and `infercat
version`; they save a round trip.

Pull requests: open an issue or a discussion first, so the change is agreed before the work.
Small fixes (a typo, a wrong sentence in `--help`, a broken link) can skip that. Every PR runs the
same checks CI runs:

```
make check                                        # go vet + go test
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

The `pm/` directory is the project's own planning record (tickets, rulings, the launch checklist).
It is internal process, kept in the open; you do not need to read it to contribute.

By contributing you agree that your contribution is licensed under the MIT License, like the rest.
