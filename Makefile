# gofaxserver — development workflow.
#
# Quick start (all-containerized):
#   make setup      # seed .env, config.json, volumes/ (never clobbers)
#   $EDITOR .env config.json volumes/freeswitch/vars.xml
#   make fs-build   # needs SIGNALWIRE_TOKEN in the environment (or .env)
#   make up
#
# Portal + reverse proxy:
#   make portal-env               # seed portal/.env (Caddyfile seeded by make setup)
#   $EDITOR portal/.env Caddyfile # HTTPS: set your DNS name in Caddyfile
#   make portal-up                # portal on :8081; Caddy runs with the main stack
#                                 # (make up) on :80 — HTTPS once a DNS name is set
#
# `make help` lists everything.

SHELL := /bin/bash

COMPOSE        := docker compose -f docker-compose.full.yml
PORTAL_COMPOSE := docker compose -f portal/docker-compose.yml
FS_IMAGE       := gofaxserver-freeswitch:latest
FS_CONFIG      := volumes/freeswitch
FS_EXAMPLES    := examples/freeswitch
GOFLAGS        :=

# Pull SIGNALWIRE_TOKEN (and friends) out of .env if it exists.
ifneq (,$(wildcard .env))
  include .env
  export
endif

.PHONY: help setup env config dirs fs-config fs-build \
        build docker-build portal-build test \
        up down logs ps fs-cli fs-reload clean \
        portal-env portal-up portal-down caddy-setup caddy-up caddy-down caddy-logs

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# First-time setup (all idempotent — existing files are never overwritten)
# ---------------------------------------------------------------------------

setup: env config dirs fs-config caddy-setup ## Full first-time bootstrap (env, config, volumes, FS config, Caddyfile)
	@echo ""
	@echo "Setup complete. Before 'make up':"
	@echo "  1. Fill in .env (POSTGRES_PASSWORD, SIGNALWIRE_TOKEN, ...)"
	@echo "  2. Edit config.json"
	@echo "  3. Set sofia_ip in $(FS_CONFIG)/vars.xml to this host's LAN IP"
	@echo "  4. Caddyfile defaults to plain HTTP on :80; set a DNS name for HTTPS"
	@echo "  5. Portal: make portal-env && edit portal/.env && make portal-up"

env: ## Create .env from sample.env if missing
	@if [[ ! -f .env ]]; then \
		cp sample.env .env && echo "created .env from sample.env"; \
	else \
		echo ".env exists — leaving it alone"; \
	fi

config: ## Create config.json from config.json.sample if missing
	@if [[ ! -f config.json ]]; then \
		cp config.json.sample config.json && echo "created config.json from config.json.sample"; \
	else \
		echo "config.json exists — leaving it alone"; \
	fi

dirs: ## Create shared volumes (gateways dir owned by the container uid)
	@mkdir -p volumes/gateways
	@if [[ "$$(uname)" == "Linux" ]]; then \
		sudo chown 1000:1000 volumes/gateways 2>/dev/null || chown 1000:1000 volumes/gateways; \
	fi
	@echo "volumes/gateways ready"

fs-config: ## Seed volumes/freeswitch from examples/freeswitch (never clobbers)
	@if [[ ! -d $(FS_CONFIG) ]]; then \
		mkdir -p volumes && \
		cp -R $(FS_EXAMPLES) $(FS_CONFIG) && \
		echo "copied $(FS_EXAMPLES) -> $(FS_CONFIG)"; \
	else \
		echo "$(FS_CONFIG) exists — leaving it alone"; \
	fi
	@if grep -q '172\.16\.25\.30\|X\.X\.X\.X\|CHANGEME' $(FS_CONFIG)/vars.xml 2>/dev/null; then \
		echo "NOTE: set sofia_ip in $(FS_CONFIG)/vars.xml to this host's LAN IP"; \
	fi

# ---------------------------------------------------------------------------
# Builds
# ---------------------------------------------------------------------------

fs-build: ## Build the FreeSWITCH image (requires SIGNALWIRE_TOKEN)
	@if [[ -z "$${SIGNALWIRE_TOKEN}" ]]; then \
		echo "SIGNALWIRE_TOKEN is not set."; \
		echo "Get a token from https://id.signalwire.com (personal access token),"; \
		echo "then put it in .env or: export SIGNALWIRE_TOKEN=..."; \
		exit 1; \
	fi
	$(COMPOSE) build freeswitch

build: ## Build the gofaxserver binary to bin/
	go build $(GOFLAGS) -o bin/gofaxserver ./gofaxserver/cmd/gofaxserver

docker-build: ## Build the gofaxserver Docker image
	docker build -t gofaxserver:latest -f Dockerfile .

portal-build: ## Build the portal (frontend dist + Go binary to bin/)
	cd portal/frontend && npm ci && npm run build
	cd portal && go build $(GOFLAGS) -o ../bin/gofaxportal ./cmd/portal

test: ## go test ./... for gofaxserver and the portal
	go test ./...
	cd portal && go test ./...

# ---------------------------------------------------------------------------
# Running the stack (docker-compose.full.yml: postgres + freeswitch + gofaxserver)
# ---------------------------------------------------------------------------

up: setup ## Start postgres + freeswitch + gofaxserver (runs setup first)
	$(COMPOSE) up -d

down: ## Stop the stack
	$(COMPOSE) down

logs: ## Tail stack logs (make logs S=freeswitch for one service)
	$(COMPOSE) logs -f $(S)

ps: ## Show stack status
	$(COMPOSE) ps

fs-cli: ## Attach to fs_cli inside the freeswitch container
	docker exec -it freeswitch fs_cli

fs-reload: ## reloadxml inside the freeswitch container (after config edits)
	docker exec freeswitch fs_cli -x reloadxml

# ---------------------------------------------------------------------------
# Portal (portal/docker-compose.yml) + Caddy reverse proxy (root compose)
# ---------------------------------------------------------------------------

portal-env: ## Create portal/.env from portal/sample.env if missing
	@if [[ ! -f portal/.env ]]; then \
		cp portal/sample.env portal/.env && echo "created portal/.env from portal/sample.env"; \
		echo "NOTE: set secrets with: openssl rand -hex 32"; \
	else \
		echo "portal/.env exists — leaving it alone"; \
	fi

portal-up: ## Start the portal (no TLS) on :8081
	$(PORTAL_COMPOSE) up -d

portal-down: ## Stop the portal stack
	$(PORTAL_COMPOSE) down

caddy-setup: ## Create Caddyfile from the sample if missing
	@if [[ ! -f Caddyfile ]]; then \
		cp Caddyfile.sample Caddyfile && \
		echo "created Caddyfile from Caddyfile.sample"; \
		echo "NOTE: default is plain HTTP on :80. For automatic HTTPS, switch"; \
		echo "to the commented hostname block (DNS must resolve here, 80/443"; \
		echo "reachable). Set PORTAL_COOKIE_SECURE=false in portal/.env while"; \
		echo "serving plain HTTP, or portal logins will not stick."; \
	else \
		echo "Caddyfile exists — leaving it alone"; \
	fi

caddy-up: ## Start the Caddy reverse proxy (:80, or :443 with a hostname set)
	$(COMPOSE) up -d caddy

caddy-down: ## Stop the Caddy reverse proxy (leaves the rest of the stack up)
	-$(COMPOSE) stop caddy
	-$(COMPOSE) rm -f caddy

caddy-logs: ## Tail Caddy logs
	$(COMPOSE) logs -f caddy

# ---------------------------------------------------------------------------

clean: ## Remove built Go binaries (never touches volumes/ or images)
	rm -rf bin
