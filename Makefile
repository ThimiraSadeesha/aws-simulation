# CloudSim developer tasks. Rust is built/tested inside Docker so no local toolchain is required.
SHELL := /bin/bash
RUST_IMAGE ?= rust:1.85-bookworm
CARGO = docker run --rm -v "$(CURDIR)/engine":/work -v cloudsim-cargo-registry:/usr/local/cargo/registry \
        -v cloudsim-engine-target:/work/target -w /work $(RUST_IMAGE) cargo

.PHONY: help up down logs test test-backend test-cli test-engine test-frontend e2e system-test bench bench-backend cli fmt clean

help:            ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

.env:
	python3 scripts/init_env.py

up: .env         ## Build and start the full platform
	docker compose up --build -d
	@echo "Console: http://localhost:3000"

down:            ## Stop the platform (data volume is kept)
	docker compose down

logs:            ## Follow logs
	docker compose logs -f

test: test-backend test-cli test-engine test-frontend  ## Run all unit/integration tests

test-backend:    ## Go backend unit + integration tests (race detector on)
	cd backend && go vet ./... && go test -race ./...

test-cli:        ## CLI tests
	cd cli && go vet ./... && go test ./...

test-engine:     ## Rust engine tests (in Docker)
	$(CARGO) test --release

test-frontend:   ## Frontend lint, typecheck, unit tests
	cd frontend && npm run lint && npx tsc --noEmit && npm test

e2e:             ## Playwright end-to-end tests against a running stack (make up first)
	cd frontend && npx playwright test

system-test: .env ## Full Docker system test (startup, services, persistence, restart recovery)
	python3 scripts/system_test.py

bench:           ## Rust engine benchmarks (criterion, in Docker)
	$(CARGO) bench

bench-backend:   ## Go micro-benchmarks + HTTP load test against a running stack
	cd backend && go test -run '^$$' -bench . -benchmem ./...
	python3 scripts/loadtest.py

cli:             ## Build the cloudsim CLI into ./bin
	cd cli && go build -o ../bin/cloudsim ./cmd/cloudsim

fmt:
	cd backend && gofmt -w .
	cd cli && gofmt -w .

clean:           ## Remove containers AND the data volume (destroys all simulated resources)
	docker compose down -v
