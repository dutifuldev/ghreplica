# ghreplica

`ghreplica` is a GitHub-shaped mirror for repository data.

It ingests GitHub webhooks, applies those events into a local canonical model, and serves a GitHub-compatible read API on top of that stored state. The project is built for tooling that needs reliable repository data without each consumer reimplementing its own crawler, cache, and webhook pipeline.

Current ownership note: this project is currently being developed by Onur Solmaz and is expected to move to another organization once it is stable.

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

The intended long-term API structure is:

- `/v1/github/...` for GitHub-compatible mirrored resources
- `/v1/changes/...` for normalized Git-backed change data
- `/v1/search/...` for overlap and related-change queries

The currently implemented read API still lives on the legacy `/repos/...` surface while that transition is designed.

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

- [Architecture](docs/ARCHITECTURE.md)
- [Compatibility Strategy](docs/COMPATIBILITY_STRATEGY.md)
- [CLI](docs/CLI.md)
- [GitHub API Surface Research](docs/GITHUB_API_SURFACE.md)
- [GitHub App Event Inventory](docs/GITHUB_APP_EVENTS.md)
- [Git Ground Truth](docs/GIT_GROUND_TRUTH.md)
- [2026-04-15 Git Ground Truth Implementation Plan](docs/2026-04-15-git-ground-truth-implementation-plan.md)
- [Data Model For PR Triage](docs/DATA_MODEL.md)
- [GCP Deployment](docs/DEPLOY_GCP.md)
- [Local Development](docs/LOCAL_DEVELOPMENT.md)
- [Ship Readiness Plan](docs/SHIP_READINESS_PLAN.md)
- [Supported Endpoints](docs/SUPPORTED_ENDPOINTS.md)
- [Sync Policy And Jobs](docs/SYNC_POLICY_AND_JOBS.md)
- [Testing](docs/TESTING.md)
- [Testing And Connectivity](docs/TESTING_AND_CONNECTIVITY.md)
