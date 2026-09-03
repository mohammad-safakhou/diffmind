.PHONY: build install test test-race test-packs test-integration test-distribution test-acceptance test-release-native ui-build ui-test ui-audit vulncheck verify run container-build company-up company-down clean

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

test-distribution:
	sh scripts/test-install.sh
	sh scripts/test-demo.sh
	GOCACHE=$(GOCACHE_DIR) go test ./scripts/release-formula
	GOCACHE=$(GOCACHE_DIR) go test ./scripts/release-check ./scripts/backup-maintenance
	ruby -c Formula/diffmind.rb

# Pass an already-built native archive and its explicit version; never publishes.
test-release-native:
	test -n "$(ARCHIVE)" && test -n "$(VERSION)"
	GOCACHE=$(GOCACHE_DIR) go run ./scripts/release-check --archive "$(ARCHIVE)" --version "$(VERSION)"

test-acceptance:
	GOCACHE=$(GOCACHE_DIR) go test ./internal/workspace/ui -run TestCompanyAcceptance -count=1 -v

ui-build:
	npm --prefix internal/workspace/ui/web ci
	npm --prefix internal/workspace/ui/web run build
	npm --prefix internal/extractor/ui/web ci
	npm --prefix internal/extractor/ui/web run build

ui-test:
	npm --prefix internal/workspace/ui/web test
	npm --prefix internal/extractor/ui/web test

ui-audit:
	npm --prefix internal/workspace/ui/web audit --audit-level=low
	npm --prefix internal/extractor/ui/web audit --audit-level=low

vulncheck:
	GOCACHE=$(GOCACHE_DIR) go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

verify: test test-race test-packs test-distribution ui-build ui-test ui-audit vulncheck

run:
	GOCACHE=$(GOCACHE_DIR) go run ./cmd/diffmind

container-build:
	docker build -t diffmind:dev .

company-up:
	docker compose up -d

company-down:
	docker compose down

clean:
	go clean -cache
	rm -rf bin .gocache
