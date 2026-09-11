export GOTOOLCHAIN := auto

# The binary name and the version live in one constant, internal/product/product.go (docs/PRINCIPLES.md),
# so the release reads them out of the source rather than repeating them — the same trick
# web/vite.config.ts already uses to pull the product name out of web/src/product.ts.
export CLI_NAME := $(shell sed -n 's/^[[:space:]]*CLIName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
export PRODUCT_VERSION := $(shell sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
# Stamped into the web bundle, so the app and the binary report the same version.
export VITE_APP_VERSION := $(PRODUCT_VERSION)
# The app's public address (product.WebURL; empty until hosting is decided), for the social-card
# image URL in index.html, which must be absolute to be picked up.
export VITE_WEB_URL := $(shell sed -n 's/^[[:space:]]*WebURL[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)

.PHONY: console-deps web-deps web-typecheck web-browser build test vet wasm web web-test web-lint size-check check clean release-dry notices notices-check brand launch-check deploy-web

build:
	go build -o bin/infercat ./cmd/infercat

test:
	# Race detection also runs in CI; cold instrumented builds can exceed a minute.
	# Warm full race runs take about 25 seconds on the development Mac.
	go test -race ./...
	node --test internal/agent/assets/adapter.test.mjs

vet:
	go vet ./...

check: size-check console-check vet test client-check bridge-check web-lint host-compat
	# host-compat built web/dist; run every no-invite launch assertion against that exact build.
	@set -eu; launch_shots=$$(mktemp -d); trap 'rm -rf "$$launch_shots"' EXIT; \
		cd web && env -u INVITE -u APP LAUNCH_SHOTS="$$launch_shots" pnpm launch-check
	node --test hack/runtime-releases.test.mjs
	sh hack/install_test.sh
	@if command -v shellcheck >/dev/null 2>&1; then shellcheck -s sh hack/install.sh; else echo "shellcheck: skipped (not installed)"; fi
	@echo "CHECK OK"

# Source caps live here (1000 lines) and in hack/size-allow.txt (per-file exceptions).
size-check:
	@find cmd internal web/src console bridge/src \
		\( -name node_modules -o -path console/dist \) -prune -o -type f \
		\( \( \( -path 'cmd/*.go' -o -path 'internal/*.go' \) ! -name '*_test.go' ! -path '*/testdata/*' \) -o \
		\( \( -path 'web/src/*' -o -path 'console/*' -o -path 'bridge/src/*' \) \( -name '*.ts' -o -name '*.tsx' \) \
		! -name '*.test.*' ! -path '*/test/*' ! -name '*.d.ts' ! -path 'web/src/i18n/*.ts' \) \) \
		-exec awk 'FILENAME == ARGV[1] { caps[$$1] = $$2; next } \
		FNR == 1 { check(); path = FILENAME } { lines = FNR } END { check(); exit failed } \
		function check() { cap = path in caps ? caps[path] : 1000; \
		if (lines > cap) { printf "%s: %d lines (cap %d)\n", path, lines, cap; failed = 1 } lines = 0 }' \
		hack/size-allow.txt {} +
	@echo "size-check: PASS"

# wasm bridge (ticket 001 owns web/wasm/build.sh)
wasm:
	sh web/wasm/build.sh

console-deps:
	cd console && pnpm install --frozen-lockfile

web-deps:
	cd web && pnpm install --frozen-lockfile

web-typecheck: web-deps
	cd web && pnpm typecheck

web-browser: web-deps
	cd web && pnpm exec playwright install chromium

web: console-check wasm web-typecheck
	cd web && pnpm build

web-test: web-browser
	cd web && pnpm test

web-lint: web-deps
	cd web && pnpm lint

# The icon set, the social-card image and GitHub's social preview, rendered from the SVG mark and
# the real connect screen (web/dev/brand.mjs). Re-run after a rename or a new mark; commit the PNGs.
brand: web
	cd web && pnpm brand

# What a stranger's browser sees on the built app: metas, manifest, icons, console, accessible
# names, contrast, focus order, and the launch screenshots (web/dev/launch-check.mjs).
launch-check: web
	cd web && pnpm launch-check

# Third-party notices: `notices` regenerates THIRD_PARTY_NOTICES.md, `notices-check` fails if it
# is stale or if any dependency's licence is unknown or not permissive.
notices:
	sh hack/notices.sh write

notices-check:
	sh hack/notices.sh check

# A full release build with nothing published: three OSes into dist/, checksummed, plus the web
# bundle as its own zip. Depends on the notices being current because a release that ships wrong
# notices is not legal, and on `web` because the bundle goes in the archive set.
release-dry: notices-check web
	goreleaser release --snapshot --clean --skip=publish
	@echo "--- artifacts ---"
	@ls -1 dist

# The real thing. Only .github/workflows/release.yml runs this, only on a v* tag, with GITHUB_TOKEN
# in the environment; from a laptop it refuses without the tag (goreleaser checks) — run it by hand
# only if the workflow is down, and only after docs/RELEASE.md.
release-publish: notices-check web
	goreleaser release --clean

clean:
	rm -rf bin dist web/dist

# Publish the web app to Cloudflare Pages (hosting/README.md): the built app minus the raw wasm,
# plus the headers, routes and the first-party counter. Needs `wrangler login` (OAuth).
deploy-web: web
	test "$(PRODUCT_VERSION)" = "$$(node -p "require('./packages/client/package.json').version")"
	rm -rf web/deploy && mkdir -p web/deploy && cp -R web/dist/. web/deploy/ && rm -f web/deploy/infercat.wasm
	node hack/runtime-releases.mjs web/deploy "$(PRODUCT_VERSION)"
	cp hack/install.sh web/deploy/install.sh
	cp docs/media/demo.mp4 docs/media/demo.zh.mp4 docs/media/demo-poster.png docs/media/demo-poster.zh.png web/deploy/
	cp hosting/cloudflare/_headers hosting/cloudflare/_redirects hosting/cloudflare/_routes.json hosting/cloudflare/404.html hosting/cloudflare/derpmap.json web/deploy/ && cp -R hosting/cloudflare/functions web/deploy/
	cd hosting/cloudflare && env -u CLOUDFLARE_API_TOKEN -u CLOUDFLARE_ACCOUNT_ID wrangler pages deploy --project-name infercat --branch main --commit-dirty=true

# Real three-scene launch recording; needs VHS, ffmpeg, IBM Plex Mono and the local model engine.
.PHONY: demo
demo:
	node docs/media/tapes/render.mjs

.PHONY: bridge-check
bridge-check:
	cd bridge && npm ci --legacy-peer-deps && npm run typecheck && npm test

.PHONY: client-check
client-check: web-deps
	test "$(PRODUCT_VERSION)" = "$$(node -p "require('./packages/client/package.json').version")"
	cd web && pnpm --filter @infercat/client build && pnpm --filter @infercat/client typecheck && pnpm --filter @infercat/client lint && pnpm --filter @infercat/client test

.PHONY: console-check
console-check: console-deps
	cd console && pnpm typecheck && pnpm lint && pnpm test && pnpm check-dist

.PHONY: console-build
console-build: console-deps
	cd console && pnpm build

# Shipped /me shapes must render Connect → Chat; install the pinned browser on clean machines.
.PHONY: host-compat
host-compat: web-lint web-test web
	cd web && pnpm exec node dev/host-compat.mjs
	cd web && pnpm exec node dev/console-chunk-check.mjs

# Opt-in real iOS proof; requires Xcode/runtime and an owned loopback engine.
.PHONY: ios-proof
ios-proof: build
	python3 web/dev/ios-proof/run.py
	python3 web/dev/ios-proof/export.py
