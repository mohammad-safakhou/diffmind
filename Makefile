BINARY=extractor
GOCACHE?=$(CURDIR)/.gocache

.PHONY: run snapshot scan parse analyze bundle query diff serve corpus corpus-self golden golden-update bench build test lint fmt migrate up down logs

run:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor --help

snapshot:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor snapshot --source . --ref HEAD --out .diffmind

scan:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor scan --source . --out .diffmind

parse:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor parse --source . --out .diffmind

analyze:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor analyze --source . --out .diffmind

bundle:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor bundle --in .diffmind/analyzers/bundle.json --out .diffmind

query:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor query --bundle .diffmind/bundle/intelligence_bundle.json --view all --format table

diff:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor diff --from .diffmind/bundle/intelligence_bundle.json --to .diffmind/bundle/intelligence_bundle.json --format table

serve:
	mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) go run ./cmd/extractor serve --addr :8080 --bundle .diffmind/bundle/intelligence_bundle.json

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
