# Makefile for dcrmapper — common development tasks.
# Run `make help` to see available targets.

BINARY := dcrmapper

# Overridable on the command line, e.g. `make run LISTEN=127.0.0.1:9000`.
LISTEN ?= 127.0.0.1:8111
DOMAIN ?= localhost

.DEFAULT_GOAL := help

## help: list available targets
.PHONY: help
help:
	@echo "dcrmapper make targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## /  /'

## build: compile the Go binary (templates and CSS are embedded)
.PHONY: build
build:
	go build -o $(BINARY) .

## run: build then start the server (override LISTEN / DOMAIN as needed)
.PHONY: run
run: build
	./$(BINARY) -listen $(LISTEN) -domain $(DOMAIN)

## fmt: format Go code in place
.PHONY: fmt
fmt:
	gofmt -w .

## vet: run go vet
.PHONY: vet
vet:
	go vet ./...

## lint: run golangci-lint (install separately, or via the CI action)
.PHONY: lint
lint:
	golangci-lint run

## test: run the Go test suite
.PHONY: test
test:
	go test ./...

## check: gofmt, vet, lint and tests — the pre-commit gate
.PHONY: check
check: vet lint test
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY)
