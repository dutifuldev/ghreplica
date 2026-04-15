# 2026-04-15 Git Ground Truth Implementation Plan

This document describes the full intended implementation for Git-backed ground truth in `ghreplica`.

This is not a phased roadmap. It describes the target architecture and the concrete work required to build it.

## Goal

`ghreplica` should treat Git itself as the deepest source of truth for repository change data.

That means:

- each tracked repository has a bare Git mirror on durable storage
- commit SHAs are the stable identity for change history
- Postgres stores derived indexes for fast queries
- GitHub webhooks and GitHub metadata fill in PR, review, comment, and issue state

The end result should support queries like:

- all open PRs touching these files
- all PRs touching overlapping line ranges
- all commits that changed this path
- related PRs based on exact file overlap, hunk overlap, and later code similarity

## Target Runtime Shape

The cloud-agnostic runtime is:

- stateless API service
- stateful git/index worker
- Postgres
- durable filesystem storage for bare mirrors

The API:

- reads Postgres
- serves GitHub-shaped endpoints and search endpoints

The git/index worker:

- owns the bare Git mirrors
- runs `git fetch`
- reads commits, trees, and diffs locally
- updates derived indexes in Postgres

Postgres:

- stores canonical GitHub-shaped entities
- stores commit-level and PR-level change indexes
- stores queryable overlap and similarity features

## Storage Model

### Bare Git Mirrors

Each tracked repository should have one bare mirror on durable disk, for example:

- `/var/lib/ghreplica/git-mirrors/<owner>/<repo>.git`

The worker owns that mirror.

The mirror must be durable across restarts. It must not live only on ephemeral instance storage.

### Postgres

Postgres stores the query layer.

It should contain:

- repositories
- refs
- commits
- commit parents
- commit file changes
- commit diff hunks
- commit line ranges
- PR head/base SHA mappings
- PR-level rolled-up change indexes
- search and overlap tables derived from those indexes

## Source Of Truth Rules

These rules should be absolute:

- branch names are not stable identities
- PR numbers are not stable identities for code state
- commit SHA is the stable identity for code state
- PR-level change state is a materialized view derived from the PR's current head SHA and base SHA

If a branch is force-pushed or rebased:

- fetch the new refs
- detect the new head SHA
- rebuild the affected PR-level derived rows
- mark prior PR-level derived rows as stale or replaced

## Ingestion Model

The worker should not fetch commit metadata one commit at a time through the GitHub API.

The correct ingestion flow is:

1. receive a webhook or detect a changed ref through repair polling
2. enqueue a repository fetch/index job
3. run `git fetch` against the bare mirror
4. identify new or changed refs
5. walk the new commits locally from the mirror
6. compute file changes, hunks, and line ranges
7. write commit-level indexes into Postgres
8. update PR-level materialized change views

GitHub API should still be used for:

- PR metadata
- issue metadata
- review metadata
- review comments
- issue comments
- labels and other GitHub-native state

GitHub API should not be the primary source for raw Git history.

## Trigger Model

The normal trigger should be event-driven.

That means:

- GitHub webhooks trigger fetch/index work quickly when refs or PRs change
- a slower repair loop runs in the background to catch missed events or drift

The worker should not poll every repository every second.

GitHub's documented repository limits allow a much higher Git read rate than one fetch per minute, but the correct behavior is still:

- fetch on change
- repair periodically
- avoid blind constant polling

## Commit-Level Index

The commit-level index is the foundational derived layer.

For each indexed commit, store:

- repository
- commit SHA
- parent SHAs
- commit metadata
- changed file paths
- file status: added, modified, removed, renamed
- previous path for renames
- patch text or patch-derived metadata
- parsed hunks
- touched line ranges

This is the durable, stable basis for all higher-level features.

## PR-Level Materialized Change View

For each PR, store a derived current change view keyed by:

- repository
- PR number
- current head SHA
- current base SHA

This view should roll up:

- changed paths
- touched directories
- touched line ranges
- diff statistics
- current state such as open, closed, merged, draft

Most product queries should hit this PR-level view first.

## Search And Overlap Layer

Once commit-level and PR-level indexes exist, add direct query support for:

- all open PRs touching these files
- all open PRs touching these directories
- all open PRs with overlapping line ranges in the same file
- all PRs related to this PR based on overlap features

The main features to compute are:

- exact file overlap
- rename-aware file continuity
- directory overlap
- hunk overlap
- line-range overlap
- commit ancestry and branch proximity where useful

Similarity should be derived from these raw change indexes. It should not be the primary stored truth.

## Postgres Tables To Add

The exact names can be finalized during implementation, but the model should include tables equivalent to:

- `git_refs`
- `git_commits`
- `git_commit_parents`
- `git_commit_files`
- `git_commit_hunks`
- `git_commit_line_ranges`
- `pull_request_heads`
- `pull_request_change_sets`
- `pull_request_change_files`
- `pull_request_change_hunks`
- `pull_request_overlap_cache`

Important indexing requirements:

- repository plus commit SHA
- repository plus path
- repository plus PR number
- GiST indexes for line-range overlap

## Worker Responsibilities

The git/index worker should do all of the following:

- clone missing bare mirrors
- fetch changed refs
- prune or garbage collect mirrors safely
- parse diffs from local Git
- build commit-level indexes
- rebuild PR-level change views when head/base changes
- update overlap and related-PR caches
- retry safely after transient failures

This worker is stateful because it owns durable mirrors.

## API Responsibilities

The API should stay focused on reading and serving.

It should:

- serve GitHub-compatible endpoints from canonical tables
- serve Git-backed query features from derived Postgres indexes
- avoid reading directly from Git mirrors during normal requests

Git should be local to the worker, not the primary request-path datastore.

## Handling Rewrites

The implementation must explicitly support:

- force-push
- rebase
- branch deletion
- branch recreation
- PR head changes

That means:

- never key change state only by branch name
- always anchor change state to commit SHA
- rebuild PR-level rows when the head/base pair changes

## Operations

The implementation needs:

- one stateful worker role with durable storage
- one stateless API role
- Postgres backups
- mirror storage monitoring
- mirror fetch and index metrics
- repair-loop metrics
- stale-index detection

## Testing Requirements

The implementation should include deterministic tests for:

- force-push and head-SHA replacement
- rename continuity
- exact file overlap
- line-range overlap
- PR head/base recomputation
- overlap queries over real stored fixture data

Real stored GitHub payloads and real diff-derived fixtures should be used where practical.

## Implementation Order

Even though this document describes the full target, the work itself should be executed in this order:

1. add durable bare mirror management
2. add commit-level indexing from local Git diffs
3. add PR head/base mapping and PR-level rolled-up change views
4. add direct file and line-range overlap queries
5. add related-PR ranking on top of those overlap features
6. connect webhook and repair triggers to the git/index worker
7. expose query endpoints and CLI support

## Final Shape

When this is complete, `ghreplica` should have:

- Git as the ground truth for code change history
- Postgres as the fast query layer
- webhook-driven fetch and indexing
- repair polling for drift recovery
- exact file and line-range overlap search
- a clean base for later code similarity features

That is the intended production architecture.
