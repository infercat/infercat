export GOTOOLCHAIN := auto

# The binary name and the version live in one constant, internal/product/product.go (pm/BELIEFS.md),
# so the release reads them out of the source rather than repeating them — the same trick
# web/vite.config.ts already uses to pull the product name out of web/src/product.ts.
export CLI_NAME := $(shell sed -n 's/^[[:space:]]*CLIName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
export PRODUCT_VERSION := $(shell sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/product/product.go)
# Stamped into the web bundle, so the app and the binary report the same version.
export VITE_APP_VERSION := $(PRODUCT_VERSION)

.PHONY: build test vet wasm web web-test check clean release-dry notices notices-check

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

clean:
	rm -rf bin dist web/dist
