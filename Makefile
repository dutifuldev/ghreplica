COMPOSE_FILE := infra/local/docker-compose.yml

.PHONY: db-up db-down migrate serve sync refresh contract

db-up:
	sg docker -c "docker compose -f $(COMPOSE_FILE) up -d"

db-down:
	sg docker -c "docker compose -f $(COMPOSE_FILE) down -v"

migrate:
	go run ./cmd/ghreplica migrate up

serve:
	go run ./cmd/ghreplica serve

sync:
	@test -n "$(REPO)" || (echo "usage: make sync REPO=owner/repo" >&2; exit 1)
	go run ./cmd/ghreplica sync repo $(REPO)

refresh:
	@test -n "$(REPO)" || (echo "usage: make refresh REPO=owner/repo" >&2; exit 1)
	go run ./cmd/ghreplica refresh repo $(REPO)

contract:
	@test -n "$(REPO)" || (echo "usage: make contract REPO=owner/repo" >&2; exit 1)
	GHREPLICA_CONTRACT_REPO=$(REPO) go test ./internal/contract -run TestGitHubCompatibilitySubset -v
