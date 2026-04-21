---
title: Sync Architecture Redesign
date: 2026-04-21
status: proposed
---

# Sync Architecture Redesign

## Why This Exists

`ghreplica` is working, but the sync system is carrying too much policy in one place.

Today the codebase still mixes:

- GitHub API mechanics
- stale-object detection
- full-object refresh
- projection writes
- queue or lease behavior
- repo scheduling policy
- operator-facing status

That makes it harder than it should be to reason about repair, full-history sync, starvation, and production behavior under load.

This document describes the long-term target shape.

## Core Rule

GitHub objects are the source of truth.

Everything else is a projection.

That means:

- the canonical GitHub object should be stored durably
- current-state relational rows should be derived from that canonical object
- repair and backfill should be different ways to discover what needs a refresh
- the read API should serve GitHub-shaped data from stored canonical state, not reconstruct it from scattered internal state unless unavoidable

## Design Goals

- keep the system webhook-first
- keep repair bounded
- make full-history sync explicit
- make scheduling policy easy to explain
- make operator status honest and easy to read
- make production diagnosis possible from metrics, not just logs

## Target Architecture

The sync system should be split into four explicit layers.

### 1. GitHub Adapter

This layer is responsible for talking to GitHub.

It should own:

- pagination
- retries
- rate-limit handling
- conditional requests
- ETag handling where useful
- request-level observability

It should not decide scheduling policy.

It should not decide whether a repo needs repair or backfill.

### 2. Planner

This layer decides what work should run next.

It should choose:

- which repository to work on
- which phase to run
- what cursor or page range to use
- what work slice budget to apply

It should be the only place where scheduling priority and starvation policy live.

### 3. Object Store

This layer persists canonical GitHub objects.

The pinned-down storage choice for this design is:

- keep only the latest canonical snapshot per object
- do not store full historical snapshot chains by default

For each GitHub-native object we care about, the store should preserve:

- stable GitHub identity
- repository identity
- GitHub `updated_at`
- fetch time
- content hash
- raw JSON
- optional ETag or equivalent fetch metadata

The important rule is:

- one latest canonical GitHub object per resource
- derived current-state relational rows are built from that latest object

This keeps the source of truth clean without letting storage grow with every historical object revision.

### 4. Projector

This layer updates fast current-state tables and indexes from canonical objects.

It should own:

- current repository rows
- current issue rows
- current pull rows
- secondary relational indexes
- read-model support tables

Search indexing should be downstream of projection, not bundled into bounded repair.

## Unified Sync Pipeline

All sync modes should use the same inner pipeline:

1. scan candidate objects
2. detect staleness
3. fetch full objects only where needed
4. persist canonical objects
5. update current projections

The modes should differ only in how they produce candidates.

### Candidate Sources

- webhook mode
  - candidate objects come from webhook-delivered identities
- targeted refresh
  - candidate objects come from explicit operator or downstream requests
- recent repair
  - candidate objects come from recent issue and pull list pages
- full-history repair
  - candidate objects come from cursor-driven historical list pages
- inventory or backfill
  - candidate objects come from repo-level completeness scans

The engine should stay the same even when the scan strategy changes.

## Pull Requests As Aggregates

A pull request should be treated as a small GitHub object family, not just one row.

At minimum, a PR repair unit includes:

- the pull resource
- the issue resource

That is important because a PR can look partially stale if only one side is refreshed.

The planner should think in terms of:

- candidate PR numbers
- stale pull side
- stale issue side

Then it should fetch and apply only the sides that are actually stale.

## Explicit State Machine

The current sync runtime should be replaced, over time, by an explicit state machine.

The important distinction is that repo policy, repo runtime, and run history are different things.

### Repo Policy

Repo policy should define durable intent, for example:

- webhook ingestion enabled
- recent repair cadence
- recent repair window
- full-history mode enabled or disabled
- inventory cadence
- scheduler priority

`openclaw/openclaw` should be special only because of policy data, not because of hidden worker branching.

### Repo Runtime

Repo runtime should describe current execution state, for example:

- active phase
- active lease
- current cursor
- last heartbeat
- pending task indicators

### Run History

Run history should describe what already happened, for example:

- phase start time
- phase finish time
- success or failure
- items scanned
- items changed
- next cursor

## Scheduling Model

Scheduling should become a first-class module, not an emergent property of `RunOnce`.

The scheduler should answer:

- what work item should run next
- why this item won
- what lower-priority work was deferred

At minimum, it should arbitrate between:

- targeted refresh
- recent repair
- full-history repair
- inventory work
- open-PR backfill

The scheduler should also encode explicit repo-aware fairness rules, including the current production requirement that targeted refresh should not indefinitely starve repair and vice versa.

## Read Model Rule

For GitHub-native read APIs:

- store the canonical GitHub JSON
- project the current row for filtering and indexing
- return the stored GitHub JSON directly whenever possible

This keeps the mirror honest to GitHub and keeps the read path cheap.

## Status Model

Operator status should answer a small, stable set of questions:

- what phase is running now
- what phase finished last
- what phase failed last
- what is pending
- what cursor is next
- whether a repo is behind because of stale recent objects, repo-wide inventory drift, or explicit targeted refresh work

Status should avoid exposing a confusing pile of low-level flags unless those flags are intentionally part of the operator contract.

## Metrics Model

The sync system should be diagnosable primarily from metrics.

At minimum, we should expose:

- objects scanned
- stale objects detected
- full objects fetched
- objects unchanged and skipped
- projection writes
- phase duration
- queue wait
- lease acquisition latency
- GitHub latency
- DB timeout rate

These should be split by:

- repo
- phase
- mode

## Naming Rules

The codebase should use these words consistently:

- `repair`
  - bounded stale correction
- `refresh`
  - explicit object or repo re-read triggered by a concrete request
- `backfill`
  - completeness work over known object sets
- `inventory`
  - repo-level open-set or completeness scan
- `full_history`
  - historical cursor walk, not a synonym for generic repair

These terms should not overlap casually.

## Migration Direction

The redesign does not need to land in one cutover.

A realistic migration order is:

1. extract a shared `scan -> detect -> fetch -> apply` repair engine
2. introduce latest-canonical-object storage beside current serving tables
3. move scheduler policy behind an explicit selector
4. split GitHub adapter concerns from planner concerns
5. make projection writes visibly separate from canonical object persistence
6. improve status and metrics around the new boundaries

## Non-Goals

- Do not invent a custom API for GitHub-native resources.
- Do not turn repair back into an unbounded crawler.
- Do not bundle comments, reviews, review comments, or search indexing into the bounded repair path.
- Do not build a generic sync framework that hides the real GitHub-specific concepts.

## Success Criteria

This redesign is successful when:

- the sync engine is easy to explain in one page
- repair, backfill, and full-history work are visibly different concepts
- scheduling policy lives in one place
- GitHub I/O and projection logic are cleanly separated
- operators can tell what is happening without tailing logs for every incident
- the GitHub-compatible read surface stays simple and cheap
