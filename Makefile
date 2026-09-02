export GOTOOLCHAIN := auto

.PHONY: build test vet wasm web web-test check clean

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

web:
	cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm build

web-test:
	cd web && pnpm test

clean:
	rm -rf bin dist web/dist
