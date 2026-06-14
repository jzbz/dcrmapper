# Makefile for dcrmapper — common development tasks.
# Run `make help` to see available targets.

BINARY    := dcrmapper
CSS_IN    := tailwind.input.css
CSS_OUT   := public/css/tailwind.css
TEMPLATES := $(wildcard templates/*.html)

# Pinned Tailwind standalone CLI (no Node.js required). `make tools` fetches it.
TAILWIND_VERSION := v3.4.17
TAILWIND         := bin/tailwindcss

# Overridable on the command line, e.g. `make run LISTEN=127.0.0.1:9000`.
LISTEN ?= 127.0.0.1:8111
DOMAIN ?= localhost

.DEFAULT_GOAL := help

## help: list available targets
.PHONY: help
help:
	@echo "dcrmapper make targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## /  /'

## build: compile the stylesheet (if stale) and the Go binary
.PHONY: build
build: $(CSS_OUT)
	go build -o $(BINARY) .

## run: build then start the server (override LISTEN / DOMAIN as needed)
.PHONY: run
run: build
	./$(BINARY) -listen $(LISTEN) -domain $(DOMAIN)

## css: rebuild the Tailwind stylesheet when templates or input change
.PHONY: css
css: $(CSS_OUT)

# Real file target: only rebuilt when a dependency is newer. The Tailwind CLI
# is an order-only prerequisite (| ...) so its timestamp never forces a rebuild,
# but it is downloaded automatically if missing.
$(CSS_OUT): $(CSS_IN) tailwind.config.js $(TEMPLATES) | $(TAILWIND)
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --minify

## css-watch: rebuild the stylesheet on every change (development)
.PHONY: css-watch
css-watch: | $(TAILWIND)
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --watch

## css-check: rebuild CSS and fail if it differs from the committed file (CI)
.PHONY: css-check
css-check: | $(TAILWIND)
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --minify
	git diff --exit-code -- $(CSS_OUT)

## tools: download the pinned Tailwind CLI into ./bin (no Node.js required)
.PHONY: tools
tools: $(TAILWIND)

$(TAILWIND):
	@mkdir -p bin
	@os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	  case "$$os" in \
	    linux) os=linux ;; \
	    darwin) os=macos ;; \
	    *) echo "unsupported OS: $$os" >&2; exit 1 ;; \
	  esac; \
	  arch=$$(uname -m); \
	  case "$$arch" in \
	    x86_64|amd64) arch=x64 ;; \
	    aarch64|arm64) arch=arm64 ;; \
	    *) echo "unsupported arch: $$arch" >&2; exit 1 ;; \
	  esac; \
	  url="https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$$os-$$arch"; \
	  echo "Downloading $$url"; \
	  curl -fsSL "$$url" -o $(TAILWIND); \
	  chmod +x $(TAILWIND)

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
