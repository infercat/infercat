[English](CLI-JSON.md) · 简体中文

# CLI JSON operations

<!-- cli-operations:start -->
| 操作 | `infercat` 后的 argv | 管理路由 | Schema |
|---|---|---|---|
| `status` | `status --json` | GET /status | 1 |
| `watch` | `watch --json [--interval 2s]` | GET /status; GET /events | 1 |
| `keys.list` | `keys list --json` | GET /keys | 1 |
| `keys.get` | `keys show ID --json` | GET /keys/{id} | 1 |
| `keys.add` | `keys add --json [limits] -- NAME` | POST /keys | 1 |
| `keys.limits` | `keys limits ID --json [limits]` | PATCH /keys/{id} | 1 |
| `keys.pause` | `keys pause ID --json` | POST /keys/{id}/pause | 1 |
| `keys.resume` | `keys resume ID --json` | POST /keys/{id}/resume | 1 |
| `keys.revoke` | `keys revoke ID --yes --json` | POST /keys/{id}/revoke | 1 |
| `keys.rotate` | `keys rotate ID --json` | POST /keys/{id}/rotate | 1 |
| `usage` | `usage --json --window today\|week [--key ID]` | GET /usage | 1 |
| `engine` | `engine --json` | GET /engine | 1 |
| `settings.get` | `settings --json` | GET /settings | 1 |
| `settings.set` | `settings set --json -- k=v…` | PATCH /settings | 1 |
| `runs.list` | `runs list --json [--key ID]` | GET /runs | 1 |
| `stored.get` | `stored ID --json` | GET /stored | 1 |
| `stored.clear` | `stored clear ID --expect CURSOR --yes --json` | GET /stored; DELETE /stored | 1 |
| `remote.status` | `remote status --json` | GET /status | 1 |
| `remote.on` | `remote on --json` | POST /remote/enable | 1 |
| `remote.off` | `remote off --json` | POST /remote/off | 1 |
| `remote.rotate` | `remote rotate --json` | POST /remote/rotate | 1 |
| `expose.status` | `expose --json` | GET /status | 1 |
| `expose.on` | `expose --on --json` | POST /reload | 1 |
| `expose.off` | `expose --off --json` | POST /reload | 1 |
| `version` | `version --json` | — | 1 |
| `api` | `api --json` | — | 1 |
<!-- cli-operations:end -->
