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

Repo-wide inventory scans should happen on their own cadence, only when the open-PR inventory is old enough to need one.

Backfill should keep working through missing and stale PRs from the latest inventory snapshot until that snapshot is exhausted or old enough to require a fresh scan.

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
- the inventory is older than a configured freshness window
- an operator explicitly requests a scan
- repair logic decides the inventory cannot be trusted

This lane owns the reusable open-PR inventory snapshot.

### 3. Backlog Backfill

This lane is for coverage work.

It should:

- consume missing or stale PRs from the latest inventory
- rebuild PR change snapshots in bounded batches
- keep going until the inventory is exhausted or invalidated
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
- inventory freshness timestamp
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

In plain terms:

- if the inventory is still recent enough, keep backfilling
- do not keep rescanning the repo just because fresh webhooks keep arriving

## Inventory Freshness Window

The scheduler should define an explicit “inventory fresh enough to reuse” window.

The default production choice should be:

- `OPEN_PR_INVENTORY_MAX_AGE = 10m`

For example:

- if `last_open_pr_scan_at` is within the configured freshness window
- and no repair condition invalidated the inventory
- then backfill may continue against that inventory without being preempted by another repo-wide scan

The exact default can be tuned later.

The important design rule is that this must be explicit, not inferred indirectly from `dirty=true`.

## Inventory Invalidation

The inventory should only be invalidated when the open-PR set itself may have changed, or when the inventory aged out.

The default production rules should be:

- ordinary PR content changes do not invalidate the inventory
- webhook events that clearly point to one PR should enqueue targeted PR refresh only
- events that may change the open-PR set or repo-wide open-PR metadata should invalidate inventory

The production invalidation set should include at least:

- PR opened
- PR closed
- PR reopened
- base branch changed
- explicit repair or operator-requested rescan
- inventory age exceeding `OPEN_PR_INVENTORY_MAX_AGE`

This keeps repo-wide scans tied to actual repo-wide drift instead of treating every webhook as a reason to relist the whole repo.

## Configuration Model

The scheduler refactor should simplify the operator-facing configuration.

The current parameters reflect the older mixed model where fetch and backfill keep interrupting each other.

In the three-lane design, the operator-facing knobs should match the real work lanes more directly.

Keep:

- `WEBHOOK_REFRESH_DEBOUNCE`
- `BACKFILL_MAX_PRS_PER_PASS`
- `BACKFILL_MAX_RUNTIME`

Rename or replace:

- the old repo minimum fetch interval should become an inventory freshness setting
  - use `OPEN_PR_INVENTORY_MAX_AGE`
  - the real question is whether the current inventory is still fresh enough to reuse

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

If the webhook only implies repo-level drift, it may mark the inventory as needing a later scan.

But it should not immediately force the scheduler to redo a full repo-wide inventory pass before backfill can continue.

## Preemption Rule

Targeted webhook refresh work should be urgent, but it should not starve backlog backfill forever.

The default production rule should be:

- process targeted webhook refreshes in bounded bursts
- after one bounded burst, if backlog still exists and the inventory is still valid, let backfill run one slice

In plain terms:

- urgent PR refresh wins first
- but it does not get an unlimited right to keep cutting the line

This is the main scheduler behavior that prevents hot repos from repeatedly rescanning or repeatedly prioritizing fresh webhook work while the historical backlog barely moves.

## Lane Ownership

The three lanes should have explicit ownership over repo state.

The default production ownership should be:

- targeted webhook refresh lane owns per-PR urgent refresh work
- inventory scan lane owns repo-wide open-PR inventory and repo-wide totals
- backlog backfill lane owns the backfill cursor and coverage progression

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

## Cutover Strategy

This should be a direct cutover, not a dual-path rollout.

The implementation should:

1. keep the existing per-PR indexing and freshness logic
2. replace the mixed scheduler with the three-lane scheduler in one pass
3. replace the old public configuration names with the new lane-shaped names
4. update the status endpoint and docs at the same time
5. deploy the new scheduler directly
6. validate behavior on a hot repo such as `openclaw/openclaw`

The important rule is:

- do not keep legacy scheduler behavior around as a fallback path
- do not keep the old mixed-model knobs around once the cutover lands

This is still a scheduler refactor, not a ground-up indexing rewrite.

## Testing

The implementation should be validated with targeted tests for:

- webhook-triggered PR refresh not forcing an unnecessary repo-wide scan
- inventory scan aging out and becoming eligible again
- backfill continuing across webhook traffic while inventory is still fresh
- no simultaneous conflicting ownership of the same repo lane
- status counters remaining consistent while lanes alternate

The most important real-repo validation target is a hot public repo with a large open-PR backlog.

`openclaw/openclaw` is the obvious test case.

## Decision

This is the intended long-term production direction.

The indexing and freshness logic can remain mostly as it is.

The core change is to separate work selection into three explicit lanes so the system stops repeatedly choosing repo-wide scan work when it should be continuing backlog fill.
