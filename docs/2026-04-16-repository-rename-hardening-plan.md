---
title: Repository Rename Hardening Plan
date: 2026-04-16
status: proposed
---

# 2026-04-16 Repository Rename Hardening Plan

This document describes how `ghreplica` should harden repository identity handling so repository renames and transfers do not leave helper tables, jobs, or lookup paths in a partially stale state.

The core mirror data is already in decent shape. Canonical repository rows are keyed by stable GitHub repository ID, and most mirrored child objects hang off stable internal `repository_id` foreign keys. The remaining risk is in the non-canonical parts of the system that still use `full_name` or `owner/name` as their effective identity.

## Problem

Today, `ghreplica` still has several rename-sensitive paths:

- route lookup resolves repositories by current `owner_login + name`
- tracked repositories are keyed by `full_name`
- refresh jobs are keyed by `full_name`
- some webhook and search helper paths still locate repos by `full_name`

That means the core mirrored data survives renames, but operator-facing and background-job layers can still drift when the current human-facing repo name changes.

## Goal

The production goal should be:

- stable repository identity everywhere important
- current repo name treated as a locator, not the source of truth
- rename and transfer events update current locator fields cleanly
- background jobs and status rows remain attached to the same logical repository across renames
- old names can optionally resolve for a bounded window if we decide that is worth supporting

## Core Rule

Repository identity should be based on stable IDs that do not change on rename or transfer.

For `ghreplica`, that means:

- internal database identity: `repositories.id`
- stable GitHub identity: `repositories.github_id`
- current human-facing locator: `owner_login`, `name`, `full_name`

The important production rule is:

- use `repositories.id` for internal foreign keys
- use `repositories.github_id` when we need a durable cross-system GitHub identity
- treat `full_name` only as the current lookup string, not as the canonical identity

## Current Good Parts

The system already does these things correctly:

- canonical repository rows are upserted by `github_id`
- most mirrored GitHub-native child rows use `repository_id`
- PR and issue rows are keyed by stable `repository_id + number`

That means the fix is not a rewrite of the mirror itself. It is mostly a hardening pass on helper tables and lookup paths.

## What Should Change

### 1. `TrackedRepository` Should Anchor On Stable Repository Identity

`TrackedRepository` should not depend on `full_name` as its effective identity.

The production shape should be:

- keep `repository_id` as the main attachment point when a canonical repository row is known
- add `github_repository_id` if we need a durable GitHub-level identity before the canonical row exists
- keep `owner`, `name`, and `full_name` only as the current locator snapshot

The important behavioral rule is:

- after a repository row exists, tracking logic should follow `repository_id`
- `full_name` should be updated on rename, not treated as the durable key

### 2. Refresh Jobs Should Not Be Keyed Mainly By `full_name`

`RepositoryRefreshJob` should be anchored on stable repo identity too.

The production shape should be:

- `repository_id` when known
- optionally `github_repository_id` for pre-resolution cases
- `owner`, `name`, and `full_name` kept only as the locator used when the job was created

The deduplication rule should move from:

- `full_name + job_type`

to something closer to:

- `repository_id + job_type`

when the repository is already known.

If the repository is not known yet, a temporary name-based bootstrap path is acceptable, but that state should end as soon as the repo is resolved.

### 3. Lookup Helpers Should Prefer Stable Identity Internally

HTTP routing still naturally starts with `owner/repo`, because that is what users type. That is fine.

But once the route is resolved, the rest of the request path should move immediately onto stable repository identity.

The lookup rule should be:

1. resolve `owner/repo` to the current repository row
2. immediately switch to `repository_id`
3. never keep doing downstream work by re-looking up the repo name again

That avoids mid-request drift and keeps canonical queries stable.

### 4. Webhook Handling Should Reconcile Renames Explicitly

Rename and transfer events should update:

- `repositories.owner_login`
- `repositories.name`
- `repositories.full_name`
- any tracked locator snapshots that still store the current repo name
- any queued or active helper rows that still expose old locator fields for observability

The important rule is:

- helper rows stay attached to the same repository identity
- only the locator fields change

That prevents rename events from accidentally creating duplicate tracked rows or orphaned refresh jobs.

### 5. Optional Name Alias Support

If we want old repo names to keep working for a short time after rename, we should not overload the main repository table for that.

Instead, add a small alias/history table such as:

- `repository_name_aliases`
  - `repository_id`
  - `owner_login`
  - `name`
  - `full_name`
  - `valid_from`
  - `valid_to`
  - `redirect_mode`

This should be optional.

The simplest production-safe first step is:

- keep only the current name in canonical routes
- do not promise old-name lookup yet

If later we want redirect or alias behavior, do it explicitly with a separate alias table.

## Schema Direction

The minimal hardening changes should be:

### `tracked_repositories`

Make sure the durable identity is available and used:

- stable internal reference: `repository_id`
- optional durable GitHub reference: `github_repository_id`
- current locator snapshot: `owner`, `name`, `full_name`

Indexes should favor:

- unique or primary operational lookups by `repository_id`
- secondary lookup by current `full_name`

### `repository_refresh_jobs`

Add or standardize:

- `repository_id`
- optional `github_repository_id`
- current locator snapshot fields

Indexes and dedupe logic should favor:

- `repository_id`
- job status
- job type

not only `full_name`.

### Optional `repository_name_aliases`

Only if we want redirect or historical lookup behavior later.

## Lookup Policy

The most production-ready lookup policy is:

- external requests arrive by current `owner/repo`
- resolve that to one repo row
- move immediately onto `repository_id`
- perform all downstream reads, writes, indexing, and job coordination using stable repo identity

That should apply to:

- HTTP handlers
- change-index jobs
- search-index jobs
- webhook projection
- refresh scheduling

## Migration Plan

The safest rollout is:

1. add stable repo identity fields to helper tables where missing
2. backfill helper rows from canonical repository rows
3. switch dedupe and lookup logic to prefer `repository_id`
4. keep `full_name` as a secondary locator only
5. add rename-focused tests before removing old assumptions

This should be a compatibility-preserving internal change. Public API route shapes do not need to change.

## Testing

The test plan should explicitly cover:

- repository rename updates canonical repo locator fields
- rename does not create duplicate repository rows
- tracked repository rows remain attached to the same logical repository
- refresh jobs stay attached to the same repo after rename
- lookups by new `owner/repo` work immediately after rename
- child objects like issues and PRs remain accessible through the renamed route
- webhook events arriving after rename still project into the correct repo
- transfer between owners behaves like rename plus owner change

If alias support is added later, test:

- old name resolution behavior
- alias expiration behavior
- redirect versus direct resolution semantics

## Recommendation

The right production fix is not a large rewrite. The mirror core is already mostly right.

The real work is:

- make helper tables follow stable repo identity
- stop using `full_name` as the effective durable key in jobs and tracking rows
- treat current repo name as a locator snapshot
- optionally add alias support later if it is worth the complexity

That is enough to make repository renames and transfers operationally safe without changing the public API shape.
