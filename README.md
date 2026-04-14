# ghreplica

`ghreplica` is a GitHub-shaped mirror for repository data.

It ingests GitHub webhooks, applies those events into a local canonical model, and serves a GitHub-compatible read API on top of that stored state. The project is built for tooling that needs reliable repository data without each consumer reimplementing its own crawler, cache, and webhook pipeline.

Current deployment:

- API: `https://ghreplica.dutiful.dev`
- Read CLI: `ghr`
- Upstream auth: GitHub App installation tokens
- Runtime: Go, Echo, GORM, Cloud SQL

## What It Does

- receives GitHub webhook deliveries and persists the raw payloads
- projects supported events directly into canonical GitHub-shaped tables
- serves mirrored repository, issue, pull request, and discussion endpoints
- supports explicit bootstrap and backfill flows when needed
- supports bounded issue and pull request repair flows
- keeps a thin read CLI over the mirrored API

## Current Surface

- repository view
- issue list
- issue view
- issue comments
- pull request list
- pull request view
- pull request reviews
- pull request review comments
- repo mirror status

The mirror preserves GitHub-native field names and response shapes wherever the data already exists on GitHub.

## Sync Model

`ghreplica` is webhook-first.

- supported webhook events are persisted and projected into canonical tables
- full bootstrap is an explicit operator action, not the default webhook path
- large repositories can be filled incrementally from received events
- repo sync behavior is governed by explicit sync policy rather than one global crawl mode

This keeps normal ingestion bounded while still allowing targeted repair and backfill when needed.

## CLI

`ghr` is a thin read client over the hosted mirror.

Examples:

```bash
ghr repo view openclaw/openclaw
ghr repo status -R openclaw/openclaw
ghr issue list -R openclaw/openclaw --state all
ghr issue view -R openclaw/openclaw 66797 --comments
ghr pr list -R openclaw/openclaw --state all
ghr pr view -R openclaw/openclaw 66863 --comments
```

Default target:

- `https://ghreplica.dutiful.dev`

So for normal use you do not need to pass `--base-url`.

## Local Development

```bash
make db-up
make migrate
make serve
```

Manual sync:

```bash
go run ./cmd/ghreplica sync repo dutifuldev/ghreplica
go run ./cmd/ghreplica sync issue openclaw/openclaw 66797
go run ./cmd/ghreplica sync pr openclaw/openclaw 66863
```

Build the read CLI:

```bash
go build ./cmd/ghr
```

## Deployment

The current hosted instance runs on GCP with:

- Caddy for public HTTPS
- `ghreplica` as the API process
- Cloud SQL for persisted mirror state
- GitHub App webhooks pointed at `https://ghreplica.dutiful.dev/webhooks/github`

## Docs

- [Architecture](docs/architecture.md)
- [Compatibility Strategy](docs/compatibility-strategy.md)
- [CLI](docs/cli.md)
- [GitHub API Surface Research](docs/github-api-surface.md)
- [GitHub App Event Inventory](docs/github-app-events.md)
- [Data Model For PR Triage](docs/data-model.md)
- [GCP Deployment](docs/deploy-gcp.md)
- [Local Development](docs/local-development.md)
- [Ship Readiness Plan](docs/ship-readiness-plan.md)
- [Supported Endpoints](docs/supported-endpoints.md)
- [Sync Policy And Jobs](docs/sync-policy-and-jobs.md)
- [Testing And Connectivity](docs/testing-and-connectivity.md)
