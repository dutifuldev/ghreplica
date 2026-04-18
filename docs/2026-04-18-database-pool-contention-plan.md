---
title: Database Pool Contention Mitigation Plan
date: 2026-04-18
status: proposed
---

# Problem

Production is now correctly accepting GitHub webhooks on the request path and processing them in background jobs, but a new contention problem showed up under live load.

Observed symptoms on `openclaw/openclaw`:

- `POST /webhooks/github` still returns `202`
- `last_webhook_at` advances
- during a live inventory scan, `inventory_scan_running` stays `true` while `backfill_running` stays `false`
- `open_pr_current` can stay flat during that window
- the status endpoint itself can become slow
- logs show:
  - `leadership.Elector: Error attempting reelection ... context deadline exceeded`
  - `producer: Error fetching queue settings`
  - `context deadline exceeded` on simple repository and tracked-repository queries

This means the background-job cutover is working functionally, but the service is not yet in the desired production state of “syncs continuously without errors.”

# Current Likely Cause

The most likely cause is that too many different subsystems are competing for the same small Postgres connection pool at the same time:

- HTTP request handling
- River leadership and producer activity
- River webhook-processing workers
- change-sync inventory scans
- backfill work
- refresh/search helpers

The current shared pool defaults are:

- `DB_MAX_OPEN_CONNS = 10`
- `DB_MAX_IDLE_CONNS = 5`

Those values were acceptable for the earlier cutover, but they are not holding up when a live inventory scan and background webhook processing overlap on a hot repo.

# Goal

The service should:

- keep accepting webhooks quickly
- keep background webhook projection healthy
- keep change sync progressing during inventory scans
- avoid River leadership and producer timeout churn
- avoid status endpoint timeouts during normal sync activity

# Preferred Direction

The cleanest next step is to stop making every subsystem compete on one shared database pool.

The preferred shape is:

- one control pool for:
  - HTTP handlers
  - webhook acceptance
  - River leadership
  - River producer work
  - webhook-processing workers
- one sync pool for:
  - inventory scans
  - backfill
  - heavy indexing work
  - refresh worker jobs

Both pools should use the same database, but they should not share the same `sql.DB` handle or the same pool limits.

That keeps batch sync work from starving the control-plane traffic that needs to stay responsive.

# Why This Is Better

This is better than just raising one shared limit because:

- it preserves isolation between generic job execution and repo-sync policy
- it makes capacity decisions easier to reason about
- it keeps the operational model simple
- it matches the current architecture, where River is generic infrastructure and change sync is custom domain logic

The intended long-term shape is:

- River owns webhook job execution
- `ghreplica` owns repo-sync policy
- each layer gets explicit database capacity instead of accidental contention

# Initial Plan

## 1. Split Database Handles

Create separate database open paths for:

- control DB
- sync DB

They may share the same DSN, but they should have separate pool sizes.

## 2. Pin Down Separate Pool Defaults

Start with explicit independent limits rather than one shared limit.

Example starting point:

- control DB:
  - `DB_CONTROL_MAX_OPEN_CONNS = 6`
  - `DB_CONTROL_MAX_IDLE_CONNS = 3`
- sync DB:
  - `DB_SYNC_MAX_OPEN_CONNS = 6`
  - `DB_SYNC_MAX_IDLE_CONNS = 2`

These are starting values, not a promise that the exact numbers are final.

## 3. Keep Worker Concurrency Conservative

Do not increase webhook worker concurrency again until the pools are separated and measured.

For the current production shape, keep:

- `WEBHOOK_JOB_QUEUE_CONCURRENCY = 3`

until the pool split is in place.

## 4. Add Visibility

Expose and log enough information to see whether the new split is actually working:

- database pool stats for each pool
- River queue depth
- River retry rate
- status endpoint latency during inventory scans
- inventory scan duration
- backfill restart timing after an inventory scan

## 5. Re-verify On `openclaw/openclaw`

After the pool split:

- deploy to production
- send a signed webhook
- confirm `last_webhook_at` advances
- confirm no new River leadership or producer timeout errors
- confirm inventory scan can finish without stalling the rest of the service
- confirm backfill resumes after inventory completion

# Non-Goals

This plan does not change:

- the three-lane scheduler model
- the GitHub-compatible read API
- the background-job webhook boundary
- the bounded commit-detail indexing rules

Those were the right changes. This plan is about making the production runtime resilient under concurrent load.

# Success Criteria

This issue is resolved when all of the following are true in production:

- signed webhooks are still accepted with `202`
- `last_webhook_at` advances after delivery
- inventory scans complete without repeated River leadership or producer timeout errors
- `open_pr_current` continues advancing over time
- backfill resumes after inventory scans
- the status endpoint remains responsive during normal sync activity
- recent logs show no recurring `context deadline exceeded` errors caused by pool starvation
