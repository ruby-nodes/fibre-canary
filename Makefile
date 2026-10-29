BINARY := fibre-canary
CMD := ./cmd/fibre-canary
PACKAGES := ./...

.PHONY: all build test test-race test-live vet fmt fmt-check tidy check clean release release-linux-amd64 release-linux-arm64 help

all: build

## build: compile the fibre-canary binary into the repository root.
build:
	go build -trimpath -buildvcs=false -o $(BINARY) $(CMD)

## test: run the unit test suite (the live Mocha smoke test stays skipped unless authorized).
test:
	go test $(PACKAGES)

## test-race: run the unit test suite with the Go race detector.
test-race:
	go test -race $(PACKAGES)

## test-live: run the opt-in live Mocha smoke test; requires FIBRE_CANARY_LIVE_MOCHA=1 and an absolute FIBRE_CANARY_LIVE_CONFIG path, both already exported by the caller.
test-live:
	go test $(CMD) -run '^TestLiveMochaSmoke$$' -count=1 -v

## vet: run go vet across all packages.
vet:
	go vet $(PACKAGES)

## fmt: reformat all Go source files in place.
fmt:
	gofmt -l -w .

## fmt-check: fail if any Go source file is not gofmt-formatted.
fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needs to be run on:"; echo "$$unformatted"; exit 1; \
	fi

## tidy: synchronize go.mod/go.sum with current imports.
tidy:
	go mod tidy

## check: run the formatting, vet, and unit test gates expected before committing.
check: fmt-check vet test

## clean: remove local build artifacts.
clean:
	rm -f $(BINARY)
	rm -rf dist

## release: build reproducible, checksummed release binaries for every supported platform.
release: release-linux-amd64 release-linux-arm64

## release-linux-amd64: build a reproducible, checksummed linux/amd64 release binary.
release-linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o dist/fibre-canary-linux-amd64 ./cmd/fibre-canary
	sha256sum dist/fibre-canary-linux-amd64 > dist/fibre-canary-linux-amd64.sha256

## release-linux-arm64: build a reproducible, checksummed linux/arm64 release binary.
release-linux-arm64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o dist/fibre-canary-linux-arm64 ./cmd/fibre-canary
	sha256sum dist/fibre-canary-linux-arm64 > dist/fibre-canary-linux-arm64.sha256

## help: list available targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/^## /  /'
