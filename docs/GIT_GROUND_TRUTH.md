# Git Ground Truth

This document describes the long-term storage model for Git-backed features in `ghreplica`.

The core principle is:

- Git is the ground truth.
- Everything else is a derived index.

`ghreplica` should not treat branch names, PR numbers, or cached patch blobs as the deepest source of truth. Those are useful views, but the stable truth is the Git object graph: commits, trees, blobs, refs, and the relationships between them.

## Core Model

The most durable design is:

- keep a bare Git mirror for each tracked repository
- fetch refs and objects into that mirror
- index everything important by immutable commit SHA
- build relational query layers on top of that Git state

That gives `ghreplica` a stable base even when:

- a PR is force-pushed
- a branch is rebased
- a branch is deleted and recreated
- repository history is partially rewritten
- GitHub API responses change shape or become temporarily incomplete

## What Lives Where

### Git Mirror

The bare mirror is the authoritative repository state.

It should contain:

- refs
- commits
- trees
- blobs reachable from fetched refs

It should live on durable storage and be managed by `ghreplica` itself or by a stateful indexing worker that `ghreplica` owns.

The mirror is not primarily the serving layer. It is the canonical source used to derive serving indexes.

### Postgres

Postgres is the query and projection layer.

It should store:

- tracked repositories
- refs and their current SHAs
- commits
- commit parent relationships
- commit-parent changed files
- commit-parent diff hunks with old and new line ranges
- rename links
- PR-to-head-SHA mappings
- PR-to-base-SHA mappings
- PR-to-merge-base-SHA mappings
- materialized PR-level rolled-up change indexes

Postgres should answer product queries like:

- which open PRs touch these files
- which PRs touched overlapping line ranges
- which commits modified this path recently
- which PRs are similar to this PR based on changed code

## Commit-Level Truth

The most important modeling rule is:

- raw change truth belongs at the commit level

Why:

- commits are immutable
- branches move
- PR heads move
- PR numbers are not the stable change identity

So the foundational indexed unit should be:

- commit SHA

For change rows, that means:

- commit SHA plus parent edge

This matters because merge commits can have multiple parents, and there is no single built-in "commit diff" object in Git.

From there, `ghreplica` can build:

- the current change snapshot for a PR head
- the combined touched paths for a PR
- similarity and overlap features between PRs

## PR-Level Views

PR-level data should exist as a derived materialized view.

That means:

- a PR points to a current head SHA
- a PR points to a current base SHA
- a PR points to a current merge base SHA
- the PR change index is rebuilt or incrementally updated when those SHAs change

This gives two useful levels:

- commit-level truth
- PR-level current view

The PR view is what most product features should query first.

## Change Index

For each commit-parent edge, `ghreplica` should derive and store:

- changed file paths
- file status: added, modified, removed, renamed
- previous path for renames
- patch text or parsed patch metadata
- hunk boundaries
- old and new line ranges on those hunks
- optional tokenized patch features

For each PR, `ghreplica` should derive and store:

- rolled-up changed file paths for the current head/base/merge-base tuple
- rolled-up touched line ranges
- current diff statistics
- state such as open, closed, merged, draft

This is the right substrate for:

- overlap search
- conflict surfacing
- related PR discovery
- “find all open PRs touching these files”
- later semantic ranking

## Force Pushes And Rewrites

The system must assume refs are unstable.

So the required behavior is:

- track PRs by current head SHA and base SHA
- detect when either changes
- recompute the derived PR change index
- mark old PR-level derived rows as stale or replaced
- never use branch name as the primary change identity

If a repo is heavily rewritten, the mirror fetch updates refs, and the derived indexes are refreshed from the new reachable commits.

## Ingesting Git Data

`ghreplica` should not call the GitHub API once per commit.

The correct ingestion flow is:

- keep a bare local mirror of the repository
- update it with `git fetch`
- read commits, trees, and diffs from that local mirror
- write the derived indexes into Postgres

GitHub webhooks should be the main trigger for fetching.

That means:

- when a webhook says a PR or branch changed, fetch that repository
- update the local mirror
- rebuild or refresh the affected commit-level and PR-level indexes

There should also be a slower repair poll in the background to catch missed events, but normal ingestion should be event-driven, not timer-driven.

## Fetch Frequency

The mirror should not blindly poll every second.

The intended model is:

- webhooks trigger immediate fetches when something changes
- a slower repair loop checks for drift or missed events

GitHub's documented repository limits allow a much higher rate than one fetch per minute for Git read operations, but `ghreplica` should still avoid wasteful polling. The right behavior is to fetch on change, not fetch constantly just in case.

## Serving Strategy

The serving path should normally query Postgres, not Git directly.

That means:

- Git mirror for canonical repository truth
- Postgres for indexed queryability
- API features served from Postgres

Git should still be available for on-demand recomputation or deep inspection, but it should not be the default request-path database.

## Similarity Features On Top

Similarity should be built on top of the change index, not stored as the primary truth.

That means:

- first-class storage for files, hunks, ranges, SHAs
- derived similarity features computed from them

Examples:

- exact file overlap
- line-range overlap
- directory overlap
- patch token overlap
- commit ancestry or branch proximity

Semantic similarity can be added later, but it should remain an additional layer over Git-derived truth.

## Summary

The clean long-term model is:

- bare Git mirrors as ground truth
- commit-level indexing as the stable raw layer
- PR-level rolled-up indexes as the serving layer
- similarity and overlap features built on top of those indexes

That is the most production-ready way to support both exact Git-aware queries and higher-level change-discovery features without losing correctness when repository history moves.
