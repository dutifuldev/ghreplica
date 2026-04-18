---
title: Three-Lane Change Sync Plan
author: Onur Solmaz <info@solmaz.io>
date: 2026-04-18
---

# 2026-04-18 Three-Lane Change Sync Plan

This document describes the intended production refactor for `ghreplica` change sync on hot repositories.

The immediate problem is simple:

- webhook traffic keeps marking the repo dirty
- fetch work keeps regaining priority
- open-PR backfill progresses, but too slowly
- the worker keeps paying for repo-wide scan work while a large backlog still exists

The right long-term fix is to separate three different kinds of work that are currently too entangled.

## Goal

`ghreplica` should stop treating:

- “a webhook arrived”
- “the open-PR inventory is old”
- “many PR snapshots are still missing or stale”

as the same scheduling signal.

Those are different kinds of work with different urgency.

The target production model is:

- targeted webhook refresh lane
- periodic inventory scan lane
- backlog backfill lane

## Plain Summary

When a repo is busy, the system should not keep choosing “scan the whole repo again” before continuing to fill missing PRs.

Webhook-triggered updates should refresh the exact PRs that changed.

Repo-wide inventory scans should happen on their own cadence, only when the open-PR inventory is missing, clearly too old, or needs explicit repair.

Backfill should keep working through missing and stale PRs from the latest inventory snapshot for a long time before another full scan is allowed.

The inventory should therefore be treated as a committed generation, not as a temporary scratch result that becomes unusable the moment one more webhook arrives.

## Current Problem

Today the worker effectively does this:

1. if the repo is dirty, run fetch/inventory work first
2. only then run backfill

That works for small or quiet repos, but it is inefficient for hot repos with a large backlog.

On a repo such as `openclaw/openclaw`, new webhook activity keeps the repo dirty. Fetch work therefore regains priority repeatedly, and backfill only advances in small windows between fetch passes.

The problem is not the per-PR indexing logic.

The problem is the job selection policy.

## Target Work Lanes

The refactor should create three explicit lanes.

### 1. Targeted Webhook Refresh

This lane is for urgent, object-specific refresh work.

It should:

- consume webhook-derived PR hints
- refresh the exact PRs that changed
- update PR change snapshots quickly
- avoid triggering a full repo-wide open-PR scan unless that is separately needed

This lane owns freshness for recently changed PRs.

### 2. Periodic Inventory Scan

This lane is for repo-wide open-PR inventory refresh.

It should:

- list the open PR set from GitHub
- refresh stored inventory rows for those PRs
- compute freshness states against the stored snapshots
- update repo-level totals and cursor baselines

This lane should not run every time a webhook arrives.

It should run only when:

- the inventory is missing
- the inventory is older than a long repair window
- an operator explicitly requests a scan
- repair logic decides the inventory cannot be trusted

This lane owns the reusable open-PR inventory snapshot.

## Inventory Generations

The open-PR inventory should be generation-based.

That means:

- the current completed inventory generation stays usable while a newer one is being built
- a new scan builds the next generation separately
- backfill keeps using the latest committed generation until the newer one is fully ready
- once the newer generation is complete, the scheduler atomically switches to it

This is important for hot repos.

The system should not stop trusting the current inventory just because more webhook traffic arrived.

The right model is:

- current generation remains usable
- newer generation is needed
- backfill continues on the current generation until the switch

The direct-cutover generation commit rule should be strict:

- only commit a newer generation if the scan completed successfully
- never partially commit a building generation
- if the scan fails, keep the previous committed generation active
- leave `inventory_needs_refresh = true`
- record `last_error`
- retry with a later inventory scan

### 3. Backlog Backfill

This lane is for coverage work.

It should:

- consume missing or stale PRs from the latest inventory
- rebuild PR change snapshots in bounded batches
- keep going until the backlog is mostly drained, a newer generation is committed, or a real repair reason appears
- avoid being preempted by every new webhook when the inventory is still fresh enough to reuse

This lane owns coverage progression.

## Ownership Model

The lanes must not fight over the same repo state blindly.

The clean ownership split is:

- targeted webhook refresh lane owns per-PR urgent refresh work
- inventory scan lane owns repo-wide open-PR listing state
- backlog backfill lane owns cursor-driven coverage progress over the latest inventory

Shared state should be coordinated with explicit leases.

At minimum:

- one repo inventory scan at a time per repo
- one repo backfill pass at a time per repo
- one per-PR targeted refresh at a time for the same PR

The system does not need a global serialized repo lock.

It needs precise ownership of the specific resource each lane updates.

## Shared Repo State

The repo-level change sync state should stop collapsing all work into one dirty bit.

It should explicitly track at least:

- targeted refresh backlog present or absent
- current inventory generation id
- current inventory freshness timestamp
- next inventory generation building or absent
- inventory last started and finished times
- backfill last started and finished times
- backfill cursor
- backfill totals
- backfill missing count
- backfill stale count

The existing counters can stay.

What changes is how they are driven and which lane is allowed to update them.

## Scheduling Rules

The worker should prefer work in this order:

1. targeted webhook refresh for exact PRs
2. backlog backfill from the latest usable inventory
3. periodic inventory scan, but only if the inventory is too old or invalid to keep reusing

The key rule is:

- inventory freshness should gate repo-wide scan work
- webhook dirtiness alone should not automatically force a full repo scan ahead of backlog backfill
- once a full scan finishes, the worker should keep using that committed inventory for many backfill slices before another full scan is allowed

In plain terms:

- if the inventory is still usable, keep backfilling
- do not keep rescanning the repo just because fresh webhooks keep arriving
- a new webhook should usually mean “refresh this later,” not “scan again right now”

The scheduler should therefore distinguish between:

- inventory is usable
- inventory needs refresh

Those are not the same thing.

## Inventory Reuse Window

The scheduler should define an explicit window during which a committed inventory generation stays usable for backfill.

The default production choice should be:

- `OPEN_PR_INVENTORY_MAX_AGE = 6h`

The direct-cutover default operator-facing values should be:

- `WEBHOOK_REFRESH_DEBOUNCE = 15s`
- `OPEN_PR_INVENTORY_MAX_AGE = 6h`
- `BACKFILL_MAX_PRS_PER_PASS = 1000`
- `BACKFILL_MAX_RUNTIME = 30m`

For example:

- if `last_open_pr_scan_at` is within the configured repair window
- and no repair condition requires a newer generation
- then backfill should keep using that inventory and should not be preempted by another repo-wide scan

The important design rule is that the inventory reuse window is long.

The system should not interpret one more webhook as proof that the current inventory generation is no longer usable.

## Inventory Refresh Triggers

The system should not use “invalidation” in the strong sense of “stop trusting the current inventory immediately.”

Instead, the system should keep using the current committed generation while separately marking that a newer generation is needed.

The default production rules should be:

- ordinary PR content changes do not require a newer inventory generation
- webhook events that clearly point to one PR should enqueue targeted PR refresh only
- events that may change the open-PR set or repo-wide open-PR metadata should mark inventory as needing refresh

The production refresh trigger set should include at least:

- PR opened
- PR closed
- PR reopened
- PR edited when the base branch changed
- explicit repair or operator-requested rescan
- inventory age exceeding `OPEN_PR_INVENTORY_MAX_AGE`

The default direct-cutover rule should explicitly not mark inventory refresh needed for:

- PR synchronize
- comments
- reviews
- labels
- assignees
- issue-only events

This keeps repo-wide scans tied to real open-set drift without making the current generation unusable in the meantime.

The scheduler should therefore keep two ideas separate:

- inventory is still usable for backfill
- inventory should be refreshed later

A busy repo will often be in both states at once.

## Configuration Model

The scheduler refactor should simplify the operator-facing configuration.

The current parameters reflect the older mixed model where fetch and backfill keep interrupting each other.

In the three-lane design, the operator-facing knobs should match the real work lanes more directly.

Keep:

- `WEBHOOK_REFRESH_DEBOUNCE`
- `BACKFILL_MAX_PRS_PER_PASS`
- `BACKFILL_MAX_RUNTIME`

The direct-cutover defaults should be:

- `WEBHOOK_REFRESH_DEBOUNCE = 15s`
- `BACKFILL_MAX_PRS_PER_PASS = 1000`
- `BACKFILL_MAX_RUNTIME = 30m`

Rename or replace:

- the old repo minimum fetch interval should become an inventory freshness setting
  - use `OPEN_PR_INVENTORY_MAX_AGE`
  - the real question is whether the current inventory still deserves another repair scan yet

Remove from the operator-facing model:

- an explicit open-PR backfill interval between passes
  - if backlog exists and inventory is still valid, backfill should keep running
  - the scheduler should not need an artificial pause between backfill passes

Keep internal-only if they still exist:

- low-level poll interval
- lease TTL
- heartbeat timings

The clean long-term operator-facing configuration should therefore be shaped around:

- `WEBHOOK_REFRESH_DEBOUNCE`
- `OPEN_PR_INVENTORY_MAX_AGE`
- `BACKFILL_MAX_PRS_PER_PASS`
- `BACKFILL_MAX_RUNTIME`

That is a better mental model and a better production model because it matches the three work lanes directly.

## Webhook Behavior

A webhook should do two different things depending on what it knows.

If the webhook tells us which PR changed, it should enqueue targeted PR refresh work.

If the webhook implies repo-level open-PR drift, it should mark inventory as needing a newer generation.

But it should not immediately force the scheduler to redo a full repo-wide inventory pass before backfill can continue.

In the normal case, it should only mark that a later inventory refresh is needed.

## Preemption Rule

Targeted webhook refresh work should be urgent, but it should not starve backlog backfill forever.

The default production rule should be:

- process targeted webhook refreshes in bounded bursts
- after one bounded burst, if backlog still exists and the inventory is still usable, let backfill run one slice
- if no other repo needs a turn, let the same repo keep taking more backfill slices from the same committed inventory

The direct-cutover default burst policy should be:

- process up to `50` distinct PR refreshes per burst
- or spend up to `30s` in targeted refresh work
- whichever limit is reached first

In plain terms:

- urgent PR refresh wins first
- but it does not get an unlimited right to keep cutting the line
- and it does not automatically buy the repo another full scan

This is the main scheduler behavior that prevents hot repos from repeatedly rescanning or repeatedly prioritizing fresh webhook work while the historical backlog barely moves.

## Lane Ownership

The three lanes should have explicit ownership over repo state.

The default production ownership should be:

- targeted webhook refresh lane owns per-PR urgent refresh work
- inventory scan lane owns building and committing repo-wide open-PR inventory generations and repo-wide totals
- backlog backfill lane owns the backfill cursor and coverage progression against the latest committed inventory generation

All three lanes should share one freshness function so they agree on what counts as:

- `current`
- `stale`
- `missing`

## Status Surface

The public status surface should stay legible for operators.

The scheduler refactor should preserve or improve the existing `/v1/changes/.../status` endpoint by making these distinctions easier to see:

- targeted refresh work pending
- inventory fresh or stale
- inventory scan running or idle
- backfill running or idle
- current, stale, and missing open-PR counts

The important thing is that operators can see why the system is choosing its current work.

The target status surface should expose these ideas directly rather than forcing operators to infer them from one generic dirty bit.

The direct-cutover default field set should include:

- `targeted_refresh_pending`
- `targeted_refresh_running`
- `inventory_generation_current`
- `inventory_generation_building`
- `inventory_needs_refresh`
- `inventory_last_committed_at`
- `inventory_scan_running`
- `backfill_running`
- `backfill_generation`
- `backfill_cursor`
- `open_pr_total`
- `open_pr_current`
- `open_pr_stale`
- `open_pr_missing`
- `last_error`

## Cutover Strategy

This should be a direct cutover, not a dual-path rollout.

The implementation should:

1. keep the existing per-PR indexing and freshness logic
2. replace the mixed scheduler with the three-lane scheduler in one pass
3. replace the old public configuration names with the new lane-shaped names
4. update the status endpoint and docs at the same time
5. implement committed inventory generations as part of the scheduler cutover
6. deploy the new scheduler directly
7. validate behavior on a hot repo such as `openclaw/openclaw`

The important rule is:

- do not keep legacy scheduler behavior around as a fallback path
- do not keep the old mixed-model knobs around once the cutover lands

This is still a scheduler refactor, not a ground-up indexing rewrite.

## Testing

The implementation should be validated with targeted tests for:

- webhook-triggered PR refresh not forcing an unnecessary repo-wide scan
- inventory scan building a new generation while the previous generation remains usable
- inventory generation switch happening atomically once the new generation is complete
- inventory scan aging out and becoming eligible again
- backfill continuing across webhook traffic while inventory is still fresh
- dirty webhook traffic marking inventory for later refresh without immediately forcing another full scan
- backfill continuing to use the same committed inventory for many slices
- backfill slice stopping at `1000 PRs` or `30m`, whichever happens first
- targeted refresh burst stopping at `50 PRs` or `30s`, whichever happens first
- no simultaneous conflicting ownership of the same repo lane
- status counters remaining consistent while lanes alternate
- failed inventory generation build leaving the previous committed generation active

The most important real-repo validation target is a hot public repo with a large open-PR backlog.

`openclaw/openclaw` is the obvious test case.

## Decision

This is the intended long-term production direction.

The indexing and freshness logic can remain mostly as it is.

The core change is to separate work selection into three explicit lanes so the system stops repeatedly choosing repo-wide scan work when it should be continuing backlog fill.
