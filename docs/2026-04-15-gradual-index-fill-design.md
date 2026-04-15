---
title: Gradual Index Fill Design
author: Onur Solmaz <info@solmaz.io>
date: 2026-04-15
---

# 2026-04-15 Gradual Index Fill Design

This document describes the target design for filling missing Git and PR change data gradually, without hammering GitHub.

It is the intended production design for:

- keeping active repositories fresh
- filling missing PR snapshots over time
- rebuilding stale snapshots after force-pushes or base-branch drift
- making search results more complete without blind full-recrawls

This design assumes the Git ground-truth architecture in [GIT_GROUND_TRUTH](./GIT_GROUND_TRUTH.md) and the implementation plan in [2026-04-15-git-ground-truth-implementation-plan](./2026-04-15-git-ground-truth-implementation-plan.md).

## Goal

The system should:

- update active repositories quickly when webhooks arrive
- fill missing PR change snapshots gradually in the background
- prioritize the most valuable data first
- avoid unbounded scans and API bursts
- remain resumable, observable, and cloud-agnostic

The system should not:

- crawl the entire repo history by default
- call GitHub APIs per commit
- run blind per-repo polling loops every few seconds
- hide whether search results are incomplete because data is missing

## Core Principle

The design should treat freshness and completeness as separate concerns.

Freshness is event-driven:

- a webhook says something changed
- the repo is marked dirty
- the worker fetches and rebuilds the affected snapshots quickly

Completeness is budgeted:

- the worker gradually fills missing open PRs first
- then recent closed or merged PRs
- then older history only if configured

This keeps active work accurate without forcing the system to index the world immediately.

## Work Types

The scheduler should distinguish these work types:

- `repo_fetch`
  - fetch new refs into the bare mirror
- `pr_snapshot_rebuild`
  - rebuild the PR-level change snapshot for one PR
- `repo_open_pr_backfill`
  - fill missing or stale snapshots for open PRs
- `repo_recent_pr_backfill`
  - fill recent closed or merged PRs
- `repo_repair_scan`
  - reconcile drift when webhooks were missed

These job types should be explicit and separately observable.

They should not be collapsed into one generic “refresh repo” job.

## Priority Model

The worker should not scan in chronological order only.

It should operate on a priority queue.

The priority order should be:

1. PRs or repos explicitly requested by a user
2. webhook-triggered work for repos marked dirty
3. missing or stale snapshots for open PRs
4. recently updated closed or merged PRs
5. historical backfill for older closed or merged PRs

Within the same priority level, prefer:

- repos with recent webhooks
- repos with more open PRs lacking snapshots
- PRs with a newer `updated_at`
- PRs that are currently `stale_*` instead of never indexed

This should be implemented as a scored priority queue, not as a fixed round-robin list.

## Dirty Repo Model

When a webhook arrives, the request path should:

1. validate and persist the delivery
2. project any GitHub-native payload fields as usual
3. mark the repo dirty for git fetch/index purposes
4. record the relevant cause:
  - `push`
  - `pull_request`
  - `repository`
  - `repair`
5. enqueue a repo-level fetch/index hint if one is not already pending

The repo dirty row should store:

- `repository_id`
- `dirty_since`
- `last_webhook_at`
- `last_requested_fetch_at`
- `last_started_fetch_at`
- `last_finished_fetch_at`
- `pending_causes`
- `pending_priority`

The key property is collapse.

If ten webhooks arrive for the same repo in three seconds, the system should do one fetch, not ten.

## Debounce And Fetch Guardrails

The elegant fetch model is:

- short debounce for burst collapse
- minimum interval per repo
- one active fetch per repo

Recommended defaults:

- `webhook_fetch_debounce = 3s`
- `repo_min_fetch_interval = 15s`
- `repo_lock_ttl = 5m`

Behavior:

- when a repo becomes dirty, the worker waits for the debounce window
- if the repo was fetched too recently, it defers until `repo_min_fetch_interval` expires
- if another worker already holds the repo lease, it skips

This keeps fetches responsive without creating storm behavior.

## Repo Fetch Flow

For one dirty repo:

1. acquire the repo fetch lease
2. open the bare mirror
3. run `git fetch` with the configured refspecs
4. detect moved refs
5. detect changed open PR heads and base refs
6. recompute `merge_base_sha` where needed
7. mark affected PR snapshots stale if the tracked tuple changed
8. enqueue targeted `pr_snapshot_rebuild` work for the affected PRs
9. clear the dirty state if no further causes arrived during the fetch

This stage should avoid calling GitHub APIs except where PR metadata is missing or stale.

Git should be the source for commit, file, and hunk change data.

## PR Snapshot Rebuild Flow

For one PR:

1. load the current PR metadata:
  - `head_sha`
  - `base_sha`
  - `base_ref`
  - `state`
  - `draft`
2. recompute or confirm `merge_base_sha`
3. run preflight diff budgeting against `merge_base_sha...head_sha`
4. decide the indexing level:
  - `full`
  - `mixed`
  - `paths_only`
  - `oversized`
5. materialize:
  - snapshot row
  - file rows
  - hunk rows where allowed
  - optional fingerprints where allowed
6. mark the snapshot freshness:
  - `current`
  - or `failed` if the build did not complete

The rebuild should be idempotent for the tuple:

- `repository_id`
- `pull_request_number`
- `head_sha`
- `base_sha`
- `merge_base_sha`

If that tuple is unchanged, the rebuild should replace or upsert the same logical snapshot, not create duplicate current state.

## Backfill Coverage Strategy

The system should fill missing data gradually in this order:

### Open PR Coverage

This is the default background target.

The worker should ensure every open PR for a tracked repo eventually has:

- at least a current `paths_only` snapshot
- preferably a current `full` or `mixed` snapshot where budgets allow

This is the minimum completeness level needed for useful overlap search.

### Recent Closed Or Merged PR Coverage

This should be optional per repo.

If enabled, the worker should fill a bounded recent window, for example:

- last `N` days by `updated_at`
- or last `M` PRs by recency

This matters because many useful overlap matches come from recently merged work, not only currently open PRs.

### Historical Coverage

This should be slowest and lowest priority.

It should only run if the repo policy enables it.

It should always yield to:

- dirty repos
- open PR backfill
- explicit user-triggered requests

## Cursor Model

Backfill jobs must be resumable.

Repo-level backfill should therefore use stored cursors.

For open PR backfill, the cursor should capture:

- the sort basis, for example `updated_at desc`
- the last visited PR API page or cursor
- the last PR number processed
- the last successful run time

For recent-history backfill, the cursor should also capture:

- the configured history window
- the cut-off timestamp used during the current sweep

If a run stops because it hit its time or work budget, the next run should continue from the stored cursor instead of restarting from the beginning.

## Time And Work Budgets

Backfill must be budgeted by wall time and work units.

Every scheduler run should stop after hitting any configured limit.

Recommended per-run budgets:

- `repo_backfill_max_runtime = 10m`
- `repo_backfill_max_prs = 50`
- `repo_backfill_max_api_pages = 20`
- `repo_backfill_max_failed_prs = 10`

Recommended global limits:

- `global_worker_concurrency = 4`
- `repo_fetch_concurrency = 1 per repo`
- `pr_rebuild_concurrency = 1 per repo`
- `github_metadata_concurrency = 8 global`

These are defaults, not absolutes.

The key design rule is:

- every unit of work should be interruptible
- every run should leave a durable cursor
- every repo should yield after consuming its budget

This prevents one very active or very large repo from starving the rest of the system.

## GitHub API Usage Policy

GitHub API use should be narrowly scoped.

Use the GitHub API for:

- listing PR metadata when deciding which PRs exist and which are open
- refreshing PR metadata that affects the snapshot key:
  - `head_sha`
  - `base_sha`
  - `base_ref`
  - `state`
  - `draft`
- discussion state:
  - reviews
  - review comments
  - issue comments

Do not use the GitHub API for:

- raw commit history walks
- per-commit diff enumeration
- per-PR file overlap computations if Git already has the answer

The worker should rely on the bare mirror for:

- commits
- parent edges
- file changes
- hunks
- line ranges
- compare calculations

## Rate Limiting

The scheduler should have explicit rate limiting, not just concurrency limiting.

Use a token-bucket model for GitHub API calls.

At minimum, configure:

- `github_rest_requests_per_minute`
- `github_rest_burst`

Also track per-repo fetch timing:

- last fetch start
- last fetch finish
- fetch frequency over the trailing minute

Git fetches are not the same as REST API requests, but they still need backpressure.

The simplest policy is:

- never fetch a repo more often than `repo_min_fetch_interval`
- never run more than one fetch for the same repo at once
- never let background backfill preempt a dirty repo fetch

## User-Driven Fast Path

If a user asks for a PR and it is missing or stale, that PR should be promoted immediately.

That request should:

- return current known status to the caller
- enqueue a high-priority `pr_snapshot_rebuild`
- optionally wait briefly if the caller explicitly asked for a blocking repair

This is important because user intent is the strongest relevance signal.

The backfill design should not force a user to wait behind low-value historical work.

## Status Model

This design depends on exposing status clearly.

Repo-level status should answer:

- is the mirror present
- is the repo dirty
- when was the last fetch
- when was the last successful index update
- how many open PRs are indexed
- how many open PRs are still missing
- how many stale snapshots exist
- what backfill mode is enabled
- whether a repair or backfill run is in progress

PR-level status should answer:

- is the PR indexed
- what tuple was indexed:
  - `head_sha`
  - `base_sha`
  - `merge_base_sha`
- is the snapshot current or stale
- what indexing level is available
- how many files are fully indexed versus path-only
- why any degradation happened

These status reads must be cheap and must not trigger work on read.

## Failure Handling

The scheduler must assume failures are normal.

Examples:

- mirror fetch transiently fails
- GitHub API page fetch fails
- one PR diff exceeds budgets
- one PR rebuild fails because the head moved mid-run

The system should:

- retry transient repo fetch failures with backoff
- mark PR rebuild failures explicitly
- keep other PR work moving
- never leave a repo permanently blocked by one bad PR

Recommended retry model:

- exponential backoff with jitter
- bounded max attempts per run
- dead-letter or “needs operator attention” only after repeated failures

## Leases And Concurrency

Use leases for both repo and PR work.

Required leases:

- repo fetch lease
- repo backfill lease
- PR rebuild lease

Rules:

- one repo fetch at a time per repo
- one backfill sweep at a time per repo
- one rebuild at a time for one PR tuple

If a worker dies, the lease should expire and another worker should resume.

This is required for a cloud-agnostic worker fleet and for correctness under bursty webhooks.

## Suggested Tables

The exact schema can be refined during implementation, but the scheduler needs rows equivalent to:

- `repo_change_sync_state`
  - repo-level dirty state, fetch timing, leases, and backfill cursors
- `pull_request_change_snapshots`
  - current PR snapshot, indexing level, and freshness
- `pull_request_change_snapshot_runs`
  - optional build attempt history for observability
- `repo_change_backfill_cursors`
  - explicit durable cursors if they do not fit cleanly in repo state

Minimum repo state fields should include:

- `repository_id`
- `dirty`
- `dirty_since`
- `last_webhook_at`
- `last_fetch_started_at`
- `last_fetch_finished_at`
- `last_successful_fetch_at`
- `last_backfill_started_at`
- `last_backfill_finished_at`
- `open_pr_cursor`
- `recent_pr_cursor`
- `backfill_mode`
- `backfill_priority`
- `fetch_lease_until`
- `backfill_lease_until`

## Recommended Config Surface

Global config:

- `webhook_fetch_debounce`
- `repo_min_fetch_interval`
- `repair_poll_interval`
- `global_worker_concurrency`
- `github_rest_requests_per_minute`
- `github_rest_burst`
- `repo_backfill_max_runtime`
- `repo_backfill_max_prs`
- `repo_backfill_max_api_pages`

Per-repo policy:

- `backfill_mode`
  - `off`
  - `open_only`
  - `open_and_recent`
  - `full_history`
- `recent_history_window_days`
- `priority`
- `auto_repair_enabled`

Recommended defaults:

- `webhook_fetch_debounce = 3s`
- `repo_min_fetch_interval = 15s`
- `repair_poll_interval = 10m`
- `repo_backfill_max_runtime = 10m`
- `repo_backfill_max_prs = 50`
- `repo_backfill_max_api_pages = 20`
- `backfill_mode = open_only`

## Observability

The design is not production-ready without metrics.

Track at least:

- repo fetch count
- repo fetch latency
- repo fetch failures
- dirty repo count
- open PRs missing snapshots
- stale PR snapshot count
- PR rebuild latency
- PR rebuild failures
- PR rebuilds by indexing level:
  - `full`
  - `mixed`
  - `paths_only`
  - `oversized`
  - `failed`
- GitHub API requests by route and result
- backfill sweeps started, completed, and budget-exhausted

Also log:

- repo id or `owner/repo`
- work type
- snapshot tuple
- lease owner
- stop reason:
  - `budget_exhausted`
  - `cursor_complete`
  - `rate_limited`
  - `lease_lost`
  - `error`

## Concrete Desired Behavior

For an active repo like `openclaw/openclaw`:

- a `pull_request` webhook marks the repo dirty
- within a few seconds, the repo is fetched once
- the changed PR snapshot is rebuilt
- other open PRs that still have no snapshot are picked up gradually in the background
- if the base branch moves, affected PRs become `stale_base_moved` and are rebuilt under budget
- if a user searches for a PR that has no snapshot yet, that PR is promoted ahead of background work

This gives:

- fast freshness for active work
- gradual completeness growth
- bounded load on GitHub
- understandable status when search misses happen

## Non-Goals

This design does not try to:

- fully index every historical PR immediately
- serve strongly consistent global completeness at all times
- hide partial coverage from clients
- use GitHub APIs as the primary source for diff truth

The system should be explicit, budgeted, and durable rather than pretending to be instantly complete.
