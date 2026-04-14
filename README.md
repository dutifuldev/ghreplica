# ghreplica

`ghreplica` is a backend-agnostic GitHub mirror written in Go.

It ingests GitHub repository data through webhooks and crawls, reconstructs that data into a local model, and serves a GitHub-compatible API on top. The project is intended to support tooling such as triage systems, reviewer agents, and offline analysis without forcing each tool to build its own partial GitHub sync layer.

## Goals

- replicate GitHub repo data with high fidelity
- stay agnostic to the underlying storage backend
- expose a GitHub-compatible read API
- support both operational workloads and async analytics exports

## Approach

The system is built around:

- webhook and crawl ingestion
- a canonical internal model of GitHub objects
- GORM-backed persistence over relational storage
- storage adapters for different backends
- API-compatible read handlers built with Echo

## Docs

- [Architecture](docs/architecture.md)
- [Compatibility Strategy](docs/compatibility-strategy.md)
- [GitHub API Surface Research](docs/github-api-surface.md)
- [GitHub App Event Inventory](docs/github-app-events.md)
- [Data Model For PR Triage](docs/data-model.md)
- [GCP Deployment](docs/deploy-gcp.md)
- [Local Development](docs/local-development.md)
- [Ship Readiness Plan](docs/ship-readiness-plan.md)
- [Sync Policy And Jobs](docs/sync-policy-and-jobs.md)
- [Supported Endpoints](docs/supported-endpoints.md)
- [Testing And Connectivity](docs/testing-and-connectivity.md)

## Status

Current state:

- live staging deployment at `https://ghreplica.dutiful.dev`
- Cloud SQL-backed storage via GORM
- GitHub App installation-token auth for upstream GitHub access
- Echo read API for repository, issue, pull request, and discussion endpoints
- webhook receiver with signature validation, raw delivery persistence, and direct event projection into canonical tables
- explicit manual sync command for full bootstrap crawls
- local dev flow for Postgres, sqlite, and GCP deployment

What works well today:

- small repos can be bootstrapped and served end to end
- webhook-driven updates can populate repository metadata, issues, pull requests, and issue comments without triggering a full repo crawl
- the service can run locally and on GCP behind Caddy with a GitHub App webhook
- large repos like `openclaw/openclaw` can now be filled incrementally from received webhook events instead of forcing a full bootstrap

Current limitations:

- full bootstrap crawls are still expensive and should only be used deliberately
- large repos are only mirrored for the subset of data already seen through webhook events unless you run an explicit bootstrap
- repo-wide refresh jobs still exist for explicit backfills, even though webhook projection is now the default sync path
- unsupported webhook events are ignored, but they still add delivery volume and log noise until the GitHub App subscription set is narrowed
- review and review-comment coverage exists in the schema and API surface, but the live deployment does not yet have meaningful mirrored data for those paths

Practical takeaway:

- `ghreplica` is usable as a working prototype and staging service
- it is not yet a scalable full-fidelity mirror for very large repositories like `openclaw/openclaw`
