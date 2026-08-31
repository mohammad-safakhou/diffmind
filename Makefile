.PHONY: build test test-integration ui-build run clean

GOCACHE_DIR := $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE_DIR) go build -o bin/diffmind ./cmd/diffmind

test:
	GOCACHE=$(GOCACHE_DIR) go test ./...

test-integration:
	DIFFMIND_RUN_SCIP_INTEGRATION=1 GOCACHE=$(GOCACHE_DIR) go test ./internal/extractor/scip

ui-build:
	npm --prefix internal/workspace/ui/web ci
	npm --prefix internal/workspace/ui/web run build
	npm --prefix internal/extractor/ui/web ci
	npm --prefix internal/extractor/ui/web run build

run:
	GOCACHE=$(GOCACHE_DIR) go run ./cmd/diffmind

clean:
	go clean -cache
	rm -rf bin .gocache
