BINARY=extractor
GOCACHE?=$(CURDIR)/.gocache
SOURCE?=$(CURDIR)/checkout-service
OUT?=$(CURDIR)/.diffmind
REF?=HEAD
ADDR?=:8080
BUNDLE?=$(OUT)/bundle/intelligence_bundle.json
GRAPH_ROOT?=$(OUT)/graph

.PHONY: run snapshot scan parse analyze bundle query diff serve serve-out graph-build graph-serve graph-ui-build graph-m8-e2e corpus corpus-self golden golden-update bench build test lint fmt migrate up down logs clean-out setup-core setup-lsp setup doctor doctor-strict run-e2e run-full

run:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor --help

snapshot:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor snapshot --source "$(SOURCE)" --ref "$(REF)" --out "$(OUT)"

scan:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor scan --source "$(SOURCE)" --out "$(OUT)"

parse:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor parse --source "$(SOURCE)" --out "$(OUT)"

analyze:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor analyze --source "$(SOURCE)" --out "$(OUT)"

bundle:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor bundle --in "$(OUT)/analyzers/bundle.json" --out "$(OUT)"

query:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor query --bundle "$(BUNDLE)" --view all --format table

diff:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor diff --from "$(BUNDLE)" --to "$(BUNDLE)" --format table

serve:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor serve --addr "$(ADDR)" --bundle "$(BUNDLE)" --graph-root "$(GRAPH_ROOT)"

serve-out:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor serve --addr "$(ADDR)" --bundle "$(BUNDLE)" --graph-root "$(GRAPH_ROOT)"

graph-build:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor graph build --mode single --service-id local --service-name local --bundle "$(BUNDLE)" --analyzer-bundle "$(OUT)/analyzers/bundle.json" --out "$(OUT)"

graph-serve:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor serve --addr "$(ADDR)" --bundle "$(BUNDLE)" --graph-root "$(GRAPH_ROOT)"

graph-ui-build:
	@echo "Graph UI is embedded static assets under internal/httpapi/ui; no separate frontend build step required."

graph-m8-e2e:
	./scripts/e2e_m8_validation.sh --source ./checkout-service --clean true

setup-core:
	./scripts/install_tooling.sh core

setup-lsp:
	./scripts/install_tooling.sh lsp

setup: setup-core setup-lsp doctor

doctor:
	./scripts/verify_tooling.sh

doctor-strict:
	./scripts/verify_tooling.sh --strict-lsp

run-e2e:
	./scripts/reset_and_run_e2e.sh "$(SOURCE)" "$(OUT)"

run-full:
	./scripts/reset_and_run_full_semantic.sh "$(SOURCE)" "$(OUT)"

clean-out:
	rm -rf "$(OUT)"

corpus:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor corpus --manifest corpus/manifest.fixtures.json --out .diffmind/corpus-fixtures

corpus-self:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor corpus --manifest corpus/manifest.example.json --out .diffmind/corpus

golden:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor golden --report .diffmind/corpus-fixtures/report.json --golden corpus/golden/fixtures_summary.json

golden-update:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor golden --report .diffmind/corpus-fixtures/report.json --golden corpus/golden/fixtures_summary.json --update

bench:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go test ./internal/benchmark -bench . -benchmem

build:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go build -o bin/$(BINARY) ./cmd/extractor

test:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go test ./...

lint:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

migrate:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/migrate

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=100
