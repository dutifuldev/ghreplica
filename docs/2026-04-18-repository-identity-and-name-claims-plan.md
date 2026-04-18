---
title: Repository Identity And Name Claims Plan
date: 2026-04-18
status: proposed
---

# Repository Identity And Name Claims Plan

## Problem

`ghreplica` currently treats both of these as if they were stable repository identity:

- `github_id`
- `full_name`

That is not correct for GitHub.

- `github_id` is the durable repository identity.
- `full_name` is the current owner/name string, which can change and can later be reused.

This mismatch is now showing up in production as `repositories_full_name_key` conflicts during projection and targeted sync.

## Goal

Make repository identity explicit and durable, while letting repository names move cleanly over time.

The mirror should be able to handle:

- repository renames
- forks that later rename
- name reuse after a rename or delete
- stale rows that still remember an old `full_name`

without treating those as irreconcilable identity conflicts.

## Long-Term Rule

The canonical rule should be:

- repository identity is `github_id`
- repository names are movable lookup claims

That means:

- one repository row is owned by one `github_id`
- `full_name` is not the permanent identity
- lookups by `owner/repo` should resolve through a name-claim layer

## Proposed Storage Model

### Canonical Repository Row

`repositories` should continue to store the canonical GitHub-shaped repository object, keyed by `github_id`.

That row represents the durable repository identity.

### Name Claims

Add a separate table for repository name claims, for example:

- `repository_name_claims`

with fields like:

- `repository_id`
- `full_name`
- `active`
- `claimed_at`
- `released_at`
- timestamps

The active row is the current lookup name for the repository. Older rows are historical claims.

## Lookup Rule

Any request that starts with `owner/repo` should resolve the repository like this:

1. look up the active name claim for `full_name`
2. resolve that to `repository_id`
3. load the canonical repository row

That keeps the GitHub-shaped API surface clean while separating identity from name history internally.

## Upsert Rule

Repository projection should upsert by `github_id`, not by `full_name`.

When a repository payload arrives:

1. load or create the canonical repository row by `github_id`
2. update its GitHub-shaped fields
3. assign the payload `full_name` as the active claim for that repository
4. if another repository previously held that active claim, deactivate that older claim

The key point is that a name handoff should be a normal state transition, not a uniqueness error.

## Cutover Shape

This should be a direct cutover, not a long-lived dual-path model.

The migration shape should be:

1. add the name-claims table
2. backfill one active claim per existing repository row
3. switch reads from `repositories.full_name` to active name claims
4. switch repository projection to claim reassignment
5. keep historical claims for observability and future debugging

## API Compatibility

This does not require a GitHub-incompatible API change.

The API should still return GitHub-shaped repository objects, including `full_name`, exactly as GitHub does. The identity/name split is an internal storage and lookup fix, not a new API model for clients.

## Short-Term Production Fix

Before the full name-claims cutover, `ghreplica` may still need a smaller defensive fix in the current `upsertRepository` path so production sync does not fail on known `full_name` collisions.

That short-term fix should still follow the same rule:

- prefer `github_id` as identity
- treat `full_name` conflicts as name-handoff problems, not as duplicate identities

## Summary

The intended long-term model is:

- `github_id` is the permanent repository identity
- `full_name` is the current name claim
- names can move between identities over time

That is the cleanest and most production-ready way to handle renames, forks, and name reuse without breaking the GitHub-shaped read surface.
