export GOTOOLCHAIN := auto

# The binary name and the version live in one constant, internal/product/product.go (pm/BELIEFS.md),
# so the release reads them out of the source rather than repeating them — the same trick
# web/vite.config.ts already uses to pull the product name out of web/src/product.ts.
export CLI_NAME := $(shell sed -n 's/^[[:space:]]*CLIName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
export PRODUCT_VERSION := $(shell sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
# Stamped into the web bundle, so the app and the binary report the same version.
export VITE_APP_VERSION := $(PRODUCT_VERSION)
# The app's public address (product.WebURL; empty until hosting is decided), for the social-card
# image URL in index.html, which must be absolute to be picked up.
export VITE_WEB_URL := $(shell sed -n 's/^[[:space:]]*WebURL[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)

.PHONY: build test vet wasm web web-test check clean release-dry notices notices-check brand launch-check

build:
	go build -o bin/bunny-network ./cmd/bunny-network

test:
	go test ./...

vet:
	go vet ./...

check: vet test
	@echo "CHECK OK"

# wasm bridge (ticket 001 owns web/wasm/build.sh)
wasm:
	sh web/wasm/build.sh

web: wasm
	cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm build

web-test:
	cd web && pnpm test

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
