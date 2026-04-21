---
title: Control Plane Database Hardening Plan
date: 2026-04-21
status: proposed
supersedes:
  - docs/2026-04-18-database-pool-contention-plan.md
---

# Problem

`ghreplica` is functionally working in production, but readiness can flap under live webhook and background-job activity even when Postgres is not at its hard connection limit.

Observed behavior:

- `/healthz` stays healthy
- `/readyz` can intermittently return `503`
- River leadership and maintenance paths log `context deadline exceeded`
- the control plane can look unhealthy while reads and repair still make progress

Recent debugging showed:

- readiness failures can happen while Postgres is still well below `max_connections`
- the problem is not only "the database is full"
- the control path, queue path, and sync path can still starve each other through pool sizing and write-path cost

The main contributors are:

- pool budgets that are too large relative to the database size
- a hot webhook accept path that writes large raw payload rows
- River housekeeping and webhook queue work competing at the same time
- a growing `webhook_deliveries` table increasing write and WAL cost on the control path

# Goal

Make the service operationally stable across different machines and database sizes without changing the working GitHub-compatible read and repair behavior.

The desired production shape is:

- reads stay fast
- readiness reflects actual serving health
- webhook bursts do not make the whole service flap
- background work stays bounded
- database sizing is explicit and configurable per deployment

# Design Principles

## 1. Budget Database Capacity Explicitly

Do not let deployment-time pool sizes accidentally exceed what the database can support.

Each deployment should treat connection capacity as a configured budget:

- database hard limit
- operator-reserved headroom
- application pool budget

The application pool budget should be split across independent pools rather than assigned to one undifferentiated total.

## 2. Keep The Control Path Small And Protected

The control path should remain responsive during background activity.

Control-path work includes:

- readiness
- ordinary API reads
- webhook acceptance
- River leadership
- River producer and light queue control work

This path should use a small, protected pool with predictable capacity.

## 3. Keep Heavy Sync Work Off The Control Pool

Heavier work should not compete directly with the control path.

Heavy work includes:

- repair passes
- inventory scans
- targeted refresh bursts
- backfill or full-history work
- indexing helpers

This work should stay on separate sync-oriented capacity.

## 4. Treat Webhook Storage As Durable But Bounded

Webhook payload storage is useful for durability and replay, but it should not grow forever on the hot path.

The system should:

- preserve raw webhook payloads long enough for debugging and replay
- mark processed deliveries explicitly
- support retention or archival of older processed deliveries
- avoid turning `webhook_deliveries` into an unbounded hot write sink

# Production-Ready Direction

## A. Use Four Configurable Database Pools

Use explicit pools for the distinct serving and background paths:

- control pool
- webhook pool
- queue pool
- sync pool

The exact sizes must stay configurable per environment.

Required settings:

- `DB_CONNECTION_BUDGET`
- `DB_CONTROL_MAX_OPEN_CONNS`
- `DB_CONTROL_MAX_IDLE_CONNS`
- `DB_WEBHOOK_MAX_OPEN_CONNS`
- `DB_WEBHOOK_MAX_IDLE_CONNS`
- `DB_QUEUE_MAX_OPEN_CONNS`
- `DB_QUEUE_MAX_IDLE_CONNS`
- `DB_SYNC_MAX_OPEN_CONNS`
- `DB_SYNC_MAX_IDLE_CONNS`

The application should not assume one fixed production size.

Different installations may have:

- different database limits
- different webhook traffic
- different read traffic
- different repair or indexing workloads

## B. Add An Explicit Pool-Budget Rule

Pool sizing should follow one simple deployment rule:

- start from the database hard connection limit
- subtract headroom for operators, migrations, diagnostics, and non-`ghreplica` clients
- split the remaining capacity across control, queue, and sync pools

This rule should be documented as guidance, not baked in as one constant ratio.

A deployment should be able to choose conservative or aggressive numbers depending on:

- database size
- colocated services
- expected webhook rate
- expected sync intensity

## C. Keep Readiness On The Protected Control Path

Readiness should continue to be tied to the serving path, but it should not fail because heavy sync work took over the same capacity.

That means:

- readiness should use the control pool
- heavy sync and repair work must not share that pool
- webhook accept writes must not share that pool
- queue and sync bursts must not be able to consume the entire serving budget

## D. Make Webhook Acceptance Cheaper And More Isolated

Webhook acceptance should remain durable, but the request path should stay as small as possible.

The acceptance path should:

- write the durable delivery record
- enqueue follow-up processing
- commit quickly

It should not do unrelated heavy work while the request transaction is open.

This path should use the dedicated webhook pool so webhook bursts do not starve ordinary reads or readiness checks.

## E. Add Retention Or Archival For `webhook_deliveries`

`webhook_deliveries` should have an explicit lifecycle.

The system should support configurable retention for processed deliveries, with choices such as:

- keep recent processed deliveries in the primary database
- delete older processed deliveries after a configured retention period
- optionally archive older payloads elsewhere before deletion

The initial production baseline should be:

- delete processed deliveries after `6h`
- keep unprocessed or failed deliveries until they are handled
- treat any later dedupe redesign as a separate follow-up

This must remain configurable because different installations may want different tradeoffs between:

- debugging depth
- storage cost
- write amplification

# Configurability Requirements

This hardening plan must work across:

- small Cloud SQL instances
- larger dedicated Postgres instances
- single-service deployments
- shared-machine deployments with other services using the same database

So the plan should not assume one machine shape.

The following choices should remain configurable:

- pool sizes for each DB handle
- operator headroom policy
- webhook worker concurrency
- repair and sync intensity
- webhook delivery retention period
- whether archival is enabled before compaction or deletion

# Rollout Strategy

## Phase 1: Pool Budget Hardening

First fix the deployment-time connection budget.

Success means:

- the total configured pool budget fits comfortably under the database limit
- readiness stops flapping under ordinary traffic
- River leadership and housekeeping stop timing out repeatedly

## Phase 2: Webhook Path Hardening

Then verify the webhook accept path remains small and bounded.

Success means:

- webhook inserts commit quickly
- queue enqueue still succeeds reliably
- webhook bursts do not cause readiness flaps

## Phase 3: Retention Or Archival

Then add an explicit lifecycle for processed webhook deliveries.

Success means:

- `webhook_deliveries` growth is bounded
- hot-path write cost does not grow indefinitely with historical payload retention

# Non-Goals

This plan does not:

- change the GitHub-compatible read API
- change repair semantics
- require the larger sync-architecture redesign
- require historical canonical object storage

This is operational hardening for the current working architecture.

# Success Criteria

This plan is successful when:

- `/readyz` stays stable under normal webhook and background activity
- River leadership and maintenance logs no longer show recurring timeout churn
- database pool sizes are explicitly configurable and sized per deployment
- heavy repair or sync work cannot starve the serving path
- `webhook_deliveries` has a documented and configurable retention strategy
