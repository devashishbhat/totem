# Totem development entry points. Every target assumes a .env file; copy
# .env.example to .env first; every variable is documented there.

SHELL := /bin/bash
.DEFAULT_GOAL := help
.PHONY: help start tour stop chat up down reset migrate server dashboard check test test-go test-rust test-python \
        test-dashboard lint fmt sdk demo record-demo seed text-vectors

COMPOSE := docker compose -f local/docker-compose.yml
# Load .env into every recipe that needs it.
ENV := set -a && [ -f .env ] && . ./.env; set +a;

help: ## Show the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

start: ## Everything, then a conversation with the assistant
	@./scripts/start.sh

tour: ## The staged attack end to end, no API key needed
	@TM_DEMO_FAKE_MODEL=true ./scripts/start.sh

stop: ## Stop the server, the dashboard and Postgres
	@./scripts/stop.sh

chat: ## Talk to the assistant (needs the server already running)
	@$(ENV) cd demo && uv run python chat.py

up: ## Start local Postgres and apply migrations
	$(COMPOSE) up -d --wait
	@$(ENV) cd server && go run ./cmd/totem-server migrate

down: ## Stop local Postgres
	$(COMPOSE) down

reset: ## Drop the local database and recreate it empty
	$(COMPOSE) down -v
	$(MAKE) up

migrate: ## Apply migrations to TM_DATABASE_URL
	@$(ENV) cd server && go run ./cmd/totem-server migrate

server: ## Run the server in the foreground
	@$(ENV) cd server && go run ./cmd/totem-server serve

dashboard: ## Run the dashboard dev server
	cd dashboard && npm run dev

sdk: ## Build the Rust core and install the Python SDK into .venv
	cd sdk/python && uv sync --all-extras
	cd sdk/python && uv run maturin develop --release

check: lint test ## Lint and test every language

lint: ## Lint every language
	cd server && go vet ./...
	cd sdk/core && cargo clippy --all-targets -- -D warnings
	cd sdk/python && uv run ruff check . && uv run ruff format --check . && uv run pyright
	cd demo && uv run ruff check . && uv run ruff format --check .
	cd dashboard && npm run lint && npx tsc --noEmit

fmt: ## Format every language
	cd server && gofmt -w ./cmd ./internal ./migrations
	cd sdk/core && cargo fmt
	cd sdk/python && uv run ruff format .
	cd demo && uv run ruff format .
	cd dashboard && npm run format

test: test-go test-rust test-python test-dashboard ## Run every test suite

test-go: ## Go tests (uses TM_TEST_DATABASE_URL when set)
	@$(ENV) cd server && go test ./...

test-rust: ## Rust unit and property tests
	cd sdk/core && cargo test

test-python: ## Python SDK tests
	cd sdk/python && uv run pytest -q

test-dashboard: ## Dashboard component tests
	cd dashboard && npm run test -- --run

text-vectors: ## Regenerate the Go/Rust cross-language text vectors
	cd sdk/python && uv run python ../../scripts/generate_text_vectors.py

demo: ## Run both demo sessions against the local stack
	@$(ENV) cd demo && uv run python crm_agent.py both

record-demo: ## Reset the database, run the demo, replay, and dump the seed
	@$(ENV) ./demo/record.sh

seed: ## Load the recorded run into an empty database ('make reset' first)
	@$(COMPOSE) exec -T postgres psql -U totem -d totem -f - < local/recorded-run.sql
