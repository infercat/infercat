# Schema 1 decoder fixtures

Vendorable, standalone JSON envelopes constructed from the Go response types used by the field inventory. These are synthetic decoder examples, not captured host responses or usable invites. `empty` uses zero values; `populated` includes optional fields and collection elements. Illustrative string values exercise open enums; they do not assert a supported engine, state transition, or runnable operation. Null/omission follows each type's actual JSON behavior.

Every single-shot operation has both forms. Watch has standalone `hello`, `status`, `event`, `dropped`, and `gone` line objects, without a command envelope. `error.json` shows the common failure envelope. Service examples use 196's actual response type; no service or browser is started to generate these files.

`make cli-contract` regenerates and checks these files alongside the field inventory and operation docs. The subprocess goldens and live-handler parity tests separately prove executed output; these files make the payload types easy for the desktop app to vendor.
