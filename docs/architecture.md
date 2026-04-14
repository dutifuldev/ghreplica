# Architecture

`ghreplica` should be built as an event-driven GitHub mirror, not as a thin HTTP wrapper around a single set of GitHub-shaped tables.

The core flow is:

1. ingest GitHub changes from webhooks and explicit backfills or repairs
2. persist raw source data
3. normalize into a canonical internal model
4. project into API-ready read models
5. serve GitHub-compatible responses through Echo
6. optionally export the same data into analytics systems

## Design Goals

- Mirror GitHub repository data with high fidelity.
- Stay backend-agnostic so storage can be swapped without rewriting domain logic.
- Support near-real-time sync via webhooks and explicit correctness repair via targeted jobs.
- Expose a GitHub-compatible API for tooling, agents, and triage systems.
- Make analytics exports first-class, but not the primary transactional store.

## Non-Goals

- Full GitHub parity on day one.
- Using a dataset store as the primary write path.
- Re-implementing every GitHub behavior before the core replication loop works.

## Core Recommendation

Use an event-driven replication pipeline with explicit storage ports.

That gives you:

- idempotent webhook ingestion
- replayability when parsers change
- repairability when webhooks are missing or out of order
- multiple downstream representations of the same source data
- a clean separation between "what GitHub said" and "how we serve it"

This is materially better than a direct `webhook -> database row -> API` design because GitHub webhooks can arrive out of order, can be redelivered, and do not always cover every repair scenario you need.

## Chosen Stack

The intended application stack is:

- `Echo` for the HTTP surface
- `GORM` for persistence models and database access
- `Postgres` as the primary transactional backend

How to apply that choice cleanly:

- use GORM models for canonical tables, raw sync tables, and projection tables
- keep model boundaries explicit instead of letting handlers query tables ad hoc
- keep schema changes versioned and reviewable even if GORM is used for model management
- reserve direct SQL for exceptional cases where GORM would make a query or migration path unclear

## Tables

Yes, the system should have tables when the backend is relational.

The important point is that not all tables serve the same purpose. You should avoid one flat schema that is both the ingestion store and the API surface.

Recommended table groups:

- raw ingestion tables
  - webhook deliveries
  - crawl responses
  - request metadata and headers
- canonical domain tables
  - repositories
  - users
  - issues
  - pull requests
  - comments
  - labels
  - commits
  - checks
- projection tables
  - issue list views
  - pull request detail views
  - label indexes
  - commit status summaries
- control tables
  - cursors
  - sync checkpoints
  - leases
  - outbox/jobs
  - health/status

So the design is still table-based. The difference is that tables are split by role in the replication pipeline.

## Why Not Use Datasets As The Primary Store

Datasets are a good export target and analytics substrate, not a good OLTP replication store.

The write path needs:

- low-latency upserts
- idempotency keys
- cursors and leases
- transactional projector updates
- efficient point reads and filtered queries

That is database work. If Hugging Face integration is useful, treat it as a sink:

- primary store: Postgres or another transactional backend
- blob store: S3, buckets, or filesystem
- analytics sink: Parquet or JSONL exports to datasets or buckets

## Architecture

### 1. Ingestion Layer

Two ingestion paths feed the same replication log:

- `Webhook ingester`
  - Receives GitHub webhook deliveries.
  - Verifies signatures.
  - Deduplicates by delivery ID.
  - Persists the raw payload before doing anything else.
- `Backfill and repair ingester`
  - Runs only when explicitly requested by policy or operator action.
  - Fetches bounded GitHub REST resources.
  - Repairs missed or insufficient webhook state.

Webhooks should be the default path. Backfills and repairs should be explicit and bounded.

### 2. Raw Replication Log

Store raw inputs exactly as received:

- webhook payloads
- crawl responses
- headers relevant to rate limits, pagination, and caching
- fetch metadata such as `observed_at`, source endpoint, delivery ID, and installation or repo scope

This log is the source material for the rest of the system. Never make the normalized tables your only copy.

### 3. Canonical Domain Model

Normalize GitHub data into an internal model that is stable across sources.

Examples:

- repositories
- users
- issues
- pull requests
- issue comments
- pull review comments
- pull reviews
- labels
- commits
- branches
- check suites
- check runs
- statuses
- releases

Important rule: keep GitHub identifiers and local identifiers separate.

- external IDs: GitHub numeric IDs, node IDs, full names, SHAs
- internal IDs: local surrogate keys if needed

The canonical model should also track:

- `source_version` or `observed_at`
- tombstones and deletions
- partial vs complete hydration state
- provenance: webhook, crawl, or manual import

### 4. Projectors

Project canonical entities into read models optimized for API serving.

Examples:

- issue list view with joins already resolved
- pull request detail view
- timeline and event view
- commit status summary
- label index per repo

Projectors must be:

- idempotent
- replayable
- version-aware
- independently runnable

This is what lets you change how responses are shaped without changing ingestion.

Webhook projectors should be event-specific:

- `repository`
- `issues`
- `issue_comment`
- `pull_request`
- `pull_request_review`
- `pull_request_review_comment`

The default should be:

- apply what the webhook already tells us
- schedule targeted repair only if the payload is insufficient
- never trigger a full repo bootstrap because a single event arrived

### 5. API Compatibility Layer

Serve GitHub-like endpoints from read models using Echo.

Keep this layer thin:

- parse GitHub-shaped requests
- translate filters and pagination into projection queries
- render GitHub-shaped JSON responses
- expose matching headers where practical

Handlers should depend on query services backed by read models. They should not reach directly into raw ingestion storage.

### 6. Optional Export/Sink Layer

Fan out the canonical model or projections into secondary systems:

- HF datasets for batch analysis
- buckets or S3 for archival JSON or Parquet
- DuckDB or Parquet for offline analytics
- Kafka or NATS if other services want change streams

These sinks should be asynchronous and disposable. They are not the system of record.

## Storage Model

The storage abstraction should be capability-based, not one giant repository interface.

Define narrow ports such as:

- `EventLog`
  - append raw ingress records
  - read by offset, time, or scope
- `CursorStore`
  - persist crawl cursors, ETags, sync checkpoints, and leases
- `CanonicalStore`
  - upsert normalized entities and relationships
- `ProjectionStore`
  - store and query API-ready read models
- `BlobStore`
  - persist large payloads, diffs, archives, and compressed JSON
- `Outbox`
  - drive projectors and async export workers reliably

This is what backend agnostic should mean in practice: the domain depends on these ports, and concrete adapters implement them for Postgres, SQLite, object storage, or HF-backed sinks where appropriate.

In the default implementation, the relational adapters should be GORM-backed.

## Recommended Default Backend

For the first real version:

- `Postgres` for cursors, canonical entities, projections, and outbox
- `GORM` for relational persistence and model mapping
- `S3`, `MinIO`, or buckets for large raw payloads and archives
- `Redis` only if distributed queues or caching become necessary

This gives you the simplest reliable baseline. Then add alternative adapters later:

- `SQLite` for local single-node development
- HF buckets as blob storage
- HF datasets as async export targets

## Sync Model

Every repo mirror should have explicit policy and explicit job types.

Per-repo policy should decide whether a repo is:

- `webhook_only`
- `webhook_plus_backfill`
- `manual_only`

Jobs should be typed and narrow. Prefer:

- `apply_webhook_delivery`
- `repair_issue`
- `repair_pull_request`
- `repair_issue_comments`
- `repair_pull_request_reviews`
- `repair_pull_request_review_comments`
- `backfill_issues_page`
- `backfill_pulls_page`

Avoid one generic `refresh repo` job as the main primitive.

When a broader sync is required, make it explicit:

- `backfill`
  - historical fetch for timelines, comments, reviews, or commits
- `repair`
  - targeted reconciliation of missing or suspicious objects
- `incremental_backfill`
  - bounded page-by-page catch-up for one resource family

Expose lag and health metadata:

- last successful webhook delivery time
- last successful repair or backfill time
- last projector lag
- last consistency repair result

Clients need to know how stale the mirror is.

## API Scope Strategy

Trying to replicate all of GitHub immediately will kill the project. Start with the repo-scoped read API that triage and agent systems actually use:

- `GET /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{number}`
- `GET /repos/{owner}/{repo}/issues/{number}/comments`
- `GET /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{number}`
- `GET /repos/{owner}/{repo}/pulls/{number}/comments`
- `GET /repos/{owner}/{repo}/pulls/{number}/reviews`
- `GET /repos/{owner}/{repo}/commits`
- `GET /repos/{owner}/{repo}/commits/{sha}`
- `GET /repos/{owner}/{repo}/labels`
- `GET /repos/{owner}/{repo}/check-runs`

Then add:

- timelines and events
- search-like local indexes
- GraphQL compatibility for the subset you actually need
- optional write-through endpoints later if ever needed

Read-only parity is the right first target.

## Contract Testing

Compatibility is the product, so test it directly.

For a fixed fixture repo:

- fetch from GitHub
- fetch from `ghreplica`
- compare status codes
- compare headers you care about
- compare pagination behavior
- compare normalized JSON fields

Build endpoint contract tests before claiming compatibility.

## Failure Model

Expect these failure cases:

- duplicate webhook deliveries
- missing webhook deliveries
- out-of-order deliveries
- force-pushes and rebases
- deleted branches or comments
- GitHub API pagination changes
- temporary rate limiting
- partial crawls that stop mid-stream

The architecture should survive all of them through raw logging, idempotent projectors, and scheduled repair crawls.

## Suggested Go Layout

```text
cmd/
  ghreplica/
    main.go

internal/
  api/
    echo/
    githubrest/
  auth/
  config/
  domain/
    model/
    normalize/
    syncstate/
  ingest/
    webhooks/
    crawler/
  project/
    canonical/
    projections/
  jobs/
  storage/
    ports/
    gorm/
    postgres/
    sqlite/
    blob/
  export/
    parquet/
    hf/
```

## Request Flow

```text
GitHub webhook or API
  -> ingesters
  -> raw event log
  -> normalizer
  -> canonical store
  -> outbox
  -> projectors
  -> projection store
  -> Echo API handlers
  -> GitHub-compatible responses
```

## Implementation Plan

1. Define canonical entities and storage ports.
2. Implement GORM-backed Postgres models plus blob storage adapters.
3. Implement webhook receiver and raw event persistence.
4. Implement repo bootstrap crawler.
5. Implement normalizers for repos, issues, PRs, comments, labels, and commits.
6. Implement projection workers for the first read endpoints.
7. Implement GitHub-compatible REST handlers.
8. Add contract tests against real GitHub fixture repos.
9. Add async export adapters for HF datasets and buckets.

## Bottom Line

The recommended architecture is:

- Echo for the HTTP surface
- GORM for relational persistence
- event-driven ingestion from both webhooks and crawls
- canonical internal model
- replayable projections
- capability-based storage ports
- Postgres plus object storage as the initial reliable backend
- HF datasets or buckets as optional downstream sinks, not the primary transactional database
