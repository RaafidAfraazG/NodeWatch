GO ?= go
GO_SOURCES := $(shell find cmd internal -name '*.go')

.PHONY: build test lint fmt run-agent run-collector migrate demo test-integration

build:
	$(GO) build -o bin/agent ./cmd/agent
	$(GO) build -o bin/collector ./cmd/collector

test:
	$(GO) test -timeout 60s ./...

lint:
	@test -z "$$(gofmt -l $(GO_SOURCES))" || (echo "Run make fmt to format Go sources"; exit 1)
	$(GO) vet ./...

fmt:
	gofmt -w $(GO_SOURCES)

run-agent:
	$(GO) run ./cmd/agent

run-collector:
	$(GO) run ./cmd/collector

migrate:
	$(GO) run ./cmd/collector migrate

demo:
	$(GO) run ./cmd/demo

test-integration:
	$(GO) test -tags=integration -count=1 -timeout 60s ./internal/storage/... ./internal/collector
