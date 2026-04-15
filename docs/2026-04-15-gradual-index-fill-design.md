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

## Implementation Direction

The production fix for gradual fill should stay inside `ghreplica`.

This design does not require introducing a separate workflow framework such as Temporal or a Redis-backed task queue.

The system should instead use a small explicit internal worker model with:

- a per-repo coordinator
- separate fetch and backfill run state
- explicit repo and PR leases
- durable cursors
- guaranteed lease cleanup
- lease heartbeats for long-running work

This is the right level of abstraction for the current system because the hard part is repo-specific orchestration, not generic background job dispatch.

If the internal worker model is later outgrown, the only serious queue candidate worth evaluating is a PostgreSQL-backed system such as River.

That is a future scaling decision, not the intended fix for the current backfill behavior.

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

## Plain-Language Summary

The next backfill refactor should do three simple things.

First, the worker should stop asking GitHub for the full open-PR list over and over again.

It should fetch that list once, store it locally, and then let later backfill batches walk through the stored list with a cursor.

Second, progress reporting should update while the job is running.

Right now the system can be doing real work while the status numbers look frozen.

Instead, every time one PR finishes indexing, the repo-level counters should move immediately so operators can see real progress.

Third, one PR sync should do less work at once.

At the moment one PR often pays for discovery, metadata refresh, and git indexing together even when some of that work was already done.

The refactor should separate those concerns so the worker does not keep repeating expensive steps that were already settled.

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

## Repo Coordinator

Each repository should be owned by one logical coordinator at a time.

That coordinator is responsible for:

- observing repo dirty state
- running fetch work
- advancing the backfill cursor
- scheduling PR snapshot rebuilds
- updating repo-level status

The coordinator should behave like a small state machine, not like a loose set of polling helpers.

The important separation is:

- fetch state controls freshness
- backfill state controls coverage

Those two concerns should not share one generic `in_progress` bit.

At minimum, the coordinator should track:

- `fetch_state`
  - `idle`
  - `debouncing`
  - `leased`
  - `running`
  - `failed`
- `backfill_state`
  - `idle`
  - `leased`
  - `running`
  - `paused`
  - `failed`

The public repo status endpoint can still expose a simplified view, but internally these states should be distinct.

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

The fetch path should only be responsible for:

- mirror freshness
- snapshot invalidation
- deciding what needs rebuild

It should not try to also own the whole open-PR backfill walk.

That is a separate loop with its own cursor and lease.

The fetch pass should also refresh the durable open-PR inventory for the repo.

That inventory should contain the fetched open PR set from the most recent successful fetch pass, including:

- `pull_request_number`
- `github_updated_at`
- `head_sha`
- `base_sha`
- `base_ref`
- `state`
- `draft`
- `last_seen_at`

The open-PR inventory is the set the coverage loop should walk.

The backfill loop should not re-list every open PR from GitHub before and after every batch.

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

The rebuild flow should also keep the per-PR unit of work as small as possible.

That means:

- do not refetch more GitHub metadata than the rebuild actually needs
- do not rebuild discussion data unless that sync path is explicitly part of the current job
- do not rescan unrelated refs or unrelated PRs while rebuilding one PR
- prefer one PR tuple per transaction and one bounded git index pass per PR

The practical goal is predictable PR-level work.

One slow or pathological PR should not make the rest of the repo appear idle or block the whole batch longer than its configured deadline.

## Backfill Coverage Strategy

The system should fill missing data gradually in this order:

### Open PR Coverage

This is the default background target.

The worker should ensure every open PR for a tracked repo eventually has:

- at least a current `paths_only` snapshot
- preferably a current `full` or `mixed` snapshot where budgets allow

This is the minimum completeness level needed for useful overlap search.

Webhook churn must not starve this loop.

If new webhook traffic arrives while open-PR coverage is in progress, the repo may become dirty again, but the stored open-PR cursor must remain intact.

The next fetch pass should update freshness state and then return control to the coverage loop instead of resetting coverage progress.

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

The cursor must be persisted after every successful batch, not only at the end of a whole repo sweep.

That is required so the system can survive:

- process restarts
- worker crashes
- lease expiry
- fetch churn on very active repos

The cursor should point into the stored open-PR inventory, not into a freshly fetched in-memory list that disappears after the batch.

That means the next batch can resume from durable repo-local state without first paying the cost of another full open-PR scan.

For open PR coverage, the practical cursor should therefore be keyed by:

- the current inventory generation or fetch watermark
- the last visited `github_updated_at`
- the last visited `pull_request_number`

If a newer fetch pass refreshes the inventory while backfill is running, the next batch should continue against the newest inventory generation without losing already completed work.

## Efficiency Direction

The current backfill design must prefer reusing durable repo-local state over repeatedly recomputing the candidate set.

The worker should therefore follow these rules:

- fetch open-PR metadata once per fetch pass
- persist the fetched open-PR inventory
- walk the stored inventory in later backfill batches
- update per-PR freshness and repo counters transactionally as each PR finishes
- avoid a second full open-PR scan at the end of every batch

The expensive operations should be:

- the fetch pass that refreshes the open-PR inventory
- the per-PR index rebuild itself

The worker should not also pay the cost of:

- listing all open PRs twice per batch
- recomputing repo summary counts from scratch after every small batch
- treating repo status as a batch-end report only

The practical result should be:

- one GitHub open-PR listing per repo fetch pass
- many backfill batches reusing that stored inventory
- cheap status reads from repo state
- cheap cursor resume after interruption

The backfill worker should also distinguish between:

- work needed to discover candidate PRs
- work needed to refresh canonical PR metadata
- work needed to rebuild the git-change snapshot

Those should not be collapsed into one monolithic per-PR routine if cheaper subpaths are possible.

Examples:

- if the stored open-PR inventory already has the needed tuple fields, the worker should not need another full PR listing to rediscover candidates
- if a PR is known current in canonical GitHub-shaped tables, the worker should not always need to refetch more metadata before rebuilding the change index
- if a PR is path-only and still within budget, the worker should be able to promote it to fuller indexing without paying unrelated repo-level costs again

The point is not to invent many tiny jobs for their own sake.

The point is to avoid repeating expensive discovery and metadata work when only the final git-index step is actually needed.

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

## Lease Cleanup And Heartbeats

The worker must assume repo work can die mid-pass.

That means every leased run must have:

- guaranteed cleanup in a `defer`
- an explicit failed-or-finished terminal write
- lease heartbeat updates while the run is still active

The lease model should be:

- claim lease
- mark run `running`
- heartbeat `lease_until` periodically
- write progress after each batch
- on success:
  - clear the lease
  - write completion timestamps
- on failure:
  - clear the lease
  - write last error
  - preserve cursor if partial progress was made

If the worker dies and cannot execute cleanup, another worker should be able to reclaim the repo after lease expiry and continue from the last durable cursor.

This is the main correctness requirement for production backfill.

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

Repo-level status counters must remain internally consistent while a backfill batch is running.

That means:

- `open_pr_total`
- `open_pr_current`
- `open_pr_stale`
- `open_pr_missing`

should not only be refreshed at batch boundaries.

Instead, the system should update them incrementally in the same transaction that:

- writes the PR snapshot
- changes that PR's freshness classification in the open-PR inventory
- advances the durable cursor when applicable

This keeps the status endpoint truthful during long-running batches and avoids the current failure mode where real snapshot writes happen while the public counters appear frozen.

The clean model is:

- the open-PR inventory is the source set
- each row in that set has a current freshness classification
- repo status counters are maintained as counts over that set

In practice the implementation can either:

- maintain the counts transactionally as delta updates, or
- recompute them from the inventory in a cheap bounded query after each successful PR update

But it should not wait for the end of a whole batch to publish new counts.

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

In practice, fetch and backfill should not reuse the same control bit.

They need:

- separate lease columns
- separate started/finished timestamps
- separate last-error reporting where useful

Otherwise one stuck fetch can make the entire repo look permanently busy even when backfill could continue safely after recovery.

## Suggested Tables

The exact schema can be refined during implementation, but the scheduler needs rows equivalent to:

- `repo_change_sync_state`
  - repo-level dirty state, fetch timing, leases, and backfill cursors
- `repo_open_pull_inventory`
  - durable fetched open-PR set for one repo and one fetch generation
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

Recommended additional bookkeeping fields:

- `fetch_state`
- `backfill_state`
- `fetch_lease_owner`
- `backfill_lease_owner`
- `last_progress_at`
- `last_progress_kind`
- `current_pull_request_number`
- `current_pull_request_head_sha`

The open-PR inventory rows should minimally include:

- `repository_id`
- `pull_request_number`
- `github_updated_at`
- `head_sha`
- `base_sha`
- `base_ref`
- `draft`
- `inventory_generation`
- `freshness_state`
- `last_seen_at`

This table exists so the fetch pass can pay the GitHub listing cost once and the backfill worker can reuse the result across many batches.

These fields are not strictly required for the public API, but they are useful for operator visibility and for debugging stuck runs.

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

## Framework Decision

The intended implementation should not adopt a large workflow framework by default.

Reasons:

- the work is strongly repo-scoped
- the important complexity is state-machine correctness, not generic queue fan-out
- `ghreplica` already has PostgreSQL-backed state and leases
- adding a second durable execution layer now would increase operational surface without solving the core stuck-pass behavior

The intended approach is:

- keep the worker internal
- refactor it into a cleaner repo coordinator with explicit fetch and backfill sub-states
- keep all durable state in PostgreSQL

If the internal worker later proves too limited, evaluate a PostgreSQL-native queue such as River before considering heavier workflow engines.
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

The observability surface should also make one running backfill batch legible to operators.

At minimum, it should expose:

- the current inventory generation
- the current cursor position
- the current PR number being processed
- the current PR head SHA when available
- per-PR duration
- per-PR timeout count
- last successful cursor advance
- whether the worker is currently discovering candidates, refreshing metadata, or rebuilding the git snapshot

Without those fields, operators are forced to infer progress from table writes and lease timestamps, which is not good enough for a long-running production backfill system.

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
