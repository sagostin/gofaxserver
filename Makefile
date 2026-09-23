# gofaxserver — development workflow.
#
# Quick start (all-containerized):
#   make setup      # seed .env, config.json, volumes/ (never clobbers)
#   $EDITOR .env config.json volumes/freeswitch/vars.xml
#   make fs-build   # needs SIGNALWIRE_TOKEN in the environment (or .env)
#   make up
#
# Portal + reverse proxy:
#   (portal/.env is seeded by make setup with generated secrets)
#   $EDITOR portal/.env Caddyfile # HTTPS: set your DNS name in Caddyfile
#   make up                       # starts everything: gofaxserver stack +
#                                 # portal db (:5433) + portal (:8081) + Caddy
#                                 # (make up) on :80 — HTTPS once a DNS name is set
#
# `make help` lists everything.

SHELL := /bin/bash

COMPOSE        := docker compose -f docker-compose.full.yml
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

setup: env config dirs fs-config caddy-setup portal-env ## Full first-time bootstrap (env, config, volumes, FS config, Caddyfile, portal env)
	@echo ""
	@echo "Setup complete. Before 'make up':"
	@echo "  1. Fill in .env (POSTGRES_PASSWORD, SIGNALWIRE_TOKEN, ...)"
	@echo "  2. Edit config.json"
	@echo "  3. Set sofia_ip in $(FS_CONFIG)/vars.xml to this host's LAN IP"
	@echo "  4. Caddyfile defaults to plain HTTP on :80; set a DNS name for HTTPS"
	@echo "  5. portal/.env was seeded with generated secrets (portal DB on :5433,"
	@echo "     portal on :8081 — both start with 'make up')"

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

fs-build: portal-env ## Build the FreeSWITCH image (requires SIGNALWIRE_TOKEN)
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

up: setup ## Start the whole stack: postgres + freeswitch + gofaxserver + portal db + portal + caddy
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
# Portal (included into the full stack via docker-compose.full.yml's
# `include:` of portal/docker-compose.yml) + Caddy reverse proxy
# ---------------------------------------------------------------------------

portal-env: ## Create portal/.env with generated secrets if missing
	@if [[ ! -f portal/.env ]]; then \
		cp portal/sample.env portal/.env; \
		apikey=$$(grep -o '"api_key": *"[^"]*"' config.json 2>/dev/null | head -1 | cut -d'"' -f4); \
		if [[ -z "$$apikey" || "$$apikey" == "apikeyhere" ]]; then \
			apikey=$$(openssl rand -hex 32); \
			if grep -q '"api_key": *"apikeyhere"' config.json 2>/dev/null; then \
				perl -pi -e "s/\"api_key\": *\"apikeyhere\"/\"api_key\": \"$$apikey\"/" config.json; \
				echo "generated web.api_key in config.json"; \
			fi; \
		fi; \
		bootpw=$$(openssl rand -hex 12); \
		perl -pi -e "s/^PORTAL_SESSION_SECRET=.*/PORTAL_SESSION_SECRET=$$(openssl rand -hex 32)/; \
			s/^PORTAL_ENCRYPTION_KEY=.*/PORTAL_ENCRYPTION_KEY=$$(openssl rand -hex 32)/; \
			s/^PORTAL_DB_PASSWORD=.*/PORTAL_DB_PASSWORD=$$(openssl rand -hex 24)/; \
			s/^PORTAL_BOOTSTRAP_PASSWORD=.*/PORTAL_BOOTSTRAP_PASSWORD=$$bootpw/; \
			s|^PORTAL_ADMIN_API_KEY=.*|PORTAL_ADMIN_API_KEY=$$apikey|" portal/.env; \
		echo "created portal/.env with generated secrets"; \
		echo "  first portal admin login: admin / $$bootpw  (reset after first login!)"; \
		echo "  PORTAL_ADMIN_API_KEY synced with web.api_key in config.json"; \
	else \
		echo "portal/.env exists — leaving it alone"; \
	fi

portal-up: portal-env ## Start just the portal services (postgres-portal :5433 + gofaxportal :8081)
	$(COMPOSE) up -d postgres-portal gofaxportal

portal-down: ## Stop the portal services (leaves the rest of the stack up)
	-$(COMPOSE) stop gofaxportal postgres-portal
	-$(COMPOSE) rm -f gofaxportal postgres-portal

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

caddy-up: portal-env ## Start the Caddy reverse proxy (:80, or :443 with a hostname set)
	$(COMPOSE) up -d caddy

caddy-down: ## Stop the Caddy reverse proxy (leaves the rest of the stack up)
	-$(COMPOSE) stop caddy
	-$(COMPOSE) rm -f caddy

caddy-logs: ## Tail Caddy logs
	$(COMPOSE) logs -f caddy

# ---------------------------------------------------------------------------

clean: ## Remove built Go binaries (never touches volumes/ or images)
	rm -rf bin
