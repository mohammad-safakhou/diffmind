.PHONY: build install test test-race test-packs test-integration ui-build ui-test verify run clean

GOCACHE_DIR := $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE_DIR) go build -o bin/diffmind ./cmd/diffmind

install:
	go install ./cmd/diffmind

test:
	GOCACHE=$(GOCACHE_DIR) go test ./...

test-race:
	GOCACHE=$(GOCACHE_DIR) go test -race ./...

test-packs:
	GOCACHE=$(GOCACHE_DIR) go run ./cmd/diffmind pack lint ./packs
	GOCACHE=$(GOCACHE_DIR) go run ./cmd/diffmind pack test ./packs

test-integration:
	DIFFMIND_RUN_SCIP_INTEGRATION=1 GOCACHE=$(GOCACHE_DIR) go test ./internal/extractor/scip

ui-build:
	npm --prefix internal/workspace/ui/web ci
	npm --prefix internal/workspace/ui/web run build
	npm --prefix internal/extractor/ui/web ci
	npm --prefix internal/extractor/ui/web run build

ui-test:
	npm --prefix internal/extractor/ui/web test

verify: test test-packs ui-build ui-test

run:
	GOCACHE=$(GOCACHE_DIR) go run ./cmd/diffmind

clean:
	go clean -cache
	rm -rf bin .gocache
