---
title: Git Ground Truth Implementation Plan
author: Onur Solmaz <info@solmaz.io>
date: 2026-04-15
---

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
- commit-parent file changes
- commit-parent diff hunks
- PR head/base SHA mappings
- PR merge-base SHA mappings
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

If the base branch moves:

- recompute the PR's current `merge_base_sha`
- invalidate the PR's current rolled-up change snapshot
- rebuild the PR-level derived rows even if the PR head SHA did not change

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

The trigger rules must also cover base-branch drift.

That means:

- when a tracked base ref moves, affected open PR snapshots must be marked stale
- the worker must schedule rebuilds for PRs that target that base ref
- a PR index cannot be treated as current just because the PR head SHA stayed the same

## Commit-Level Index

The commit-level index is the foundational derived layer.

For each indexed commit, store:

- repository
- commit SHA
- parent SHAs
- commit metadata
- per-parent diff rows for changed file paths
- file status: added, modified, removed, renamed
- previous path for renames
- patch text or patch-derived metadata
- parsed hunks with old and new line ranges

This is the durable, stable basis for all higher-level features.

Important detail:

- a commit does not have one universal diff
- merge commits can have multiple parents
- any commit-level change index must therefore be keyed by commit and parent edge, not just by commit SHA

## PR-Level Materialized Change View

For each PR, store a derived current change view keyed by:

- repository
- PR number
- current head SHA
- current base SHA
- current merge-base SHA

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

Noise control must be part of the ranking model.

Some paths will otherwise dominate candidate generation while adding little value, for example:

- lockfiles
- generated code
- vendored directories
- snapshot files
- repository-wide formatting churn

The search layer should therefore support path weighting or path suppression rules at index or ranking time.

## Query Strategy

The production query strategy should be:

1. path-first candidate generation
2. range-overlap refinement
3. optional fingerprint reranking

That means:

- every indexed PR contributes its changed paths to an inverted index
- normal-sized PRs also contribute parsed hunks and line ranges
- query execution first finds PRs that share paths
- then narrows those candidates to overlapping hunk ranges on the same paths
- then optionally reranks the top candidates with compact code fingerprints

This is the correct shape because:

- reads stay fast
- no query has to compare every PR to every other PR
- exact overlap signals stay explainable
- deeper code similarity remains optional and bounded

The serving order should be:

- exact same path overlap first
- overlapping hunk ranges on those paths second
- directory overlap and recency as ranking features
- fingerprint similarity only as a reranker, not the primary retrieval mechanism

Embeddings should not be the core retrieval path.

If semantic or code-shape similarity is added, it should sit behind the exact overlap index and only rerank a small top candidate set.

### Concrete Retrieval Shape

The retrieval layer should be built around these persisted rows:

- `pull_request_change_files`
  - one row per current PR file change
  - keyed by `repository_id`, `pull_request_number`, `head_sha`, `path`
- `pull_request_change_hunks`
  - one row per current PR hunk
  - keyed by `repository_id`, `pull_request_number`, `head_sha`, `path`, `hunk_index`
- optional `pull_request_change_fingerprints`
  - one row per PR or per changed path
  - stores compact signatures for reranking only

The critical columns for fast search are:

- `path`
- `previous_path`
- `status`
- `old_line_range`
- `new_line_range`
- `head_sha`
- `base_sha`
- `merge_base_sha`
- `state`
- `draft`
- `indexed_as`
- `index_freshness`
- `base_ref`

The critical indexes are:

- btree on `(repository_id, path)`
- btree on `(repository_id, pull_request_number)`
- btree on `(repository_id, state, draft)`
- GiST on `old_line_range`
- GiST on `new_line_range`

The default design should rely on index combination:

- btree narrows by repository and path
- GiST handles range overlap
- Postgres combines them with bitmap index scans

Do not make a multicolumn GiST index with `repository_id` as the first column the default plan.

PostgreSQL's multicolumn GiST behavior makes the first column the most important one for scan reduction, so a low-distinctness leading column like `repository_id` is a poor default.

If profiling later shows that one combined index is materially better for the real workload, then:

- use `btree_gist`
- put the most selective scalar column first
- keep the plain btree path index anyway

### Concrete Query Flow

For `GET /v1/search/repos/{owner}/{repo}/pulls/{number}/related`:

1. load the source PR's current `head_sha`, `base_sha`, `merge_base_sha`, `state`, and `indexed_as`
2. load the source PR's changed paths from `pull_request_change_files`
3. generate candidates with a grouped path-overlap query against other open PRs in the same repository
4. cap that candidate set aggressively, for example top `500` by exact path overlap count
5. if the source PR and candidate PRs are `full` indexed, run a second query on `pull_request_change_hunks`
6. use range overlap operators on same-path hunk rows to compute exact overlap counts
7. rerank the top slice, for example top `50`, with optional fingerprints

The first candidate query should look conceptually like:

- join source PR file rows to candidate PR file rows on `(repository_id, path)`
- exclude the same PR number
- require candidate PRs to be open unless the caller asks otherwise
- optionally suppress globally noisy paths before grouping
- group by candidate PR number
- compute:
  - `shared_path_count`
  - `shared_additions`
  - `shared_deletions`
  - `rename_overlap_count`

The range refinement query should:

- join source and candidate hunk rows on `(repository_id, path)`
- compute overlap with `old_line_range && old_line_range` and `new_line_range && new_line_range`
- count overlapping hunks
- sum overlapping span length where useful

The ranking function should stay simple and explainable:

- high weight for exact same path overlap
- higher weight for overlapping hunk ranges on the same path
- medium weight for rename continuity
- lower weight for directory-only overlap
- low or zero weight for configured noisy paths
- small boost for open PRs on the same base branch
- small decay for older PRs

The result payload should explain itself with fields like:

- `score`
- `shared_paths`
- `overlapping_paths`
- `overlapping_hunks`
- `matched_ranges`
- `reasons`

### Fingerprint Algorithm

If we add code-shape similarity, it should use compact deterministic fingerprints, not embeddings.

The preferred algorithm is:

- normalize changed lines from the patch
- strip whitespace-only noise
- optionally strip comments where cheap and language-agnostic heuristics are safe
- compute `simhash64` or MinHash signatures

Store those signatures as:

- `scope`: `pr` or `path`
- `algorithm`: `simhash64` or `minhash`
- `signature`

Use them only after path-based retrieval has already produced a small candidate set.

### Path Noise Policy

The design must assume some files are high-frequency but low-signal.

The index should therefore support either:

- a static path suppression list
- path-class weighting rules
- or a learned popularity score that downweights extremely common paths

Examples of default low-signal classes:

- `package-lock.json`
- `pnpm-lock.yaml`
- `yarn.lock`
- generated SDK files
- vendored dependency trees
- snapshot directories

The important rule is:

- keep these paths in the raw truth tables
- do not let them dominate candidate generation or ranking

## Indexing Budgets And Degradation

The indexer must be adaptive.

Every PR should always receive:

- path-level indexing
- aggregate diff statistics
- current indexing status metadata

Normal-sized PRs should also receive:

- hunk-level indexing
- line-range indexing
- optional compact patch fingerprints

Oversized PRs should not force full fine-grained indexing.

Instead, the system should stop at a coarser representation and mark the PR accordingly.

The index state should be explicit, for example:

- `full`
- `paths_only`
- `oversized`
- `failed`

Hard budgets should be enforced before fine-grained indexing proceeds.

The concrete thresholds can be tuned, but the shape should be:

- maximum changed files
- maximum total changed lines
- maximum raw patch bytes
- maximum parsed hunk count
- maximum single-file patch bytes
- maximum single-file changed lines

A practical starting point is:

- up to `5,000` files changed for full indexing
- up to `200,000` total changed lines for full indexing
- up to `20 MB` raw patch text for full indexing
- up to `50,000` hunks for full indexing
- up to `1 MB` patch text for any one file before hunk parsing is skipped for that file
- up to `20,000` changed lines in any one file before hunk parsing is skipped for that file

The worker should also classify files before parsing.

The first-class file kinds are:

- text file
- binary file
- symlink change
- submodule change
- mode-only change

Only normal text files should go through full hunk parsing.

Binary files, symlink updates, submodule pointer changes, and mode-only changes should still get path-level rows and aggregate stats, but they should not go through line-range parsing.

If any of those limits are exceeded:

- do not build the fine-grained hunk index
- store path-level rows and aggregate stats only
- mark the PR as `paths_only` or `oversized`
- keep the API honest about the indexing level

### Preflight Budgeting

The worker should enforce budgets before expensive parsing begins.

The preflight flow should be:

1. fetch the repo mirror
2. compute the PR diff against `merge_base_sha...head_sha`
3. read file-level statistics first, without full patch parsing
4. total:
  - changed file count
  - additions
  - deletions
  - estimated patch bytes
5. decide the indexing level before materializing hunk rows

For preflight collection, prefer cheap Git commands first:

- `git diff --raw -z --no-ext-diff --no-textconv --find-renames=<threshold> -l<rename-limit> <merge-base>...<head>`
- `git diff --numstat -z --no-ext-diff --no-textconv --find-renames=<threshold> -l<rename-limit> <merge-base>...<head>`

Only if the PR stays under budget should the worker parse full patch text with:

- `git diff -z --no-ext-diff --no-textconv --find-renames=<threshold> -l<rename-limit> --unified=0 <merge-base>...<head>`

That keeps the expensive parse behind a cheap guardrail.

These flags are important:

- `-z` makes rename records machine-safe
- `--no-ext-diff` avoids environment-specific diff drivers
- `--no-textconv` avoids human-oriented conversions that are not stable machine truth

`--numstat` is the right preflight source for line counts because Git documents that binary files emit `-` and `-` instead of fake numeric line counts.

`--raw -z` is the right source for status and object identity because it carries:

- blob object IDs
- status letters such as `A`, `D`, `M`, `R`, and `T`
- preimage and postimage paths for renames

Rename detection must also be fixed by policy.

The worker should:

- always use the same rename detection mode
- always use the same similarity threshold
- always use the same rename candidate limit
- record whether a rename was inferred by Git or was not available

That avoids index drift across deployments.

The default policy should be:

- enable rename detection
- do not enable copy detection
- use an explicit similarity threshold rather than relying on process defaults
- use an explicit rename limit so the worker does not fall into unbounded quadratic matching

Copy detection should stay off by default because Git documents it as much more expensive, especially with `--find-copies-harder`.

A sane initial policy is:

- `--find-renames=50%`
- `-l1000`

If the candidate set exceeds the rename limit:

- fall back to non-rename indexing for that PR snapshot
- mark rename continuity as unavailable instead of pretending it was computed

### Partial Indexing Rules

Partial indexing should be explicit and per PR.

Examples:

- `full`
  - file rows, hunk rows, range rows, optional fingerprints
- `paths_only`
  - file rows only
- `mixed`
  - all files indexed at path level, but only some files got hunk rows because oversized files were skipped
- `oversized`
  - path rows only plus aggregate stats
- `failed`
  - indexing attempt did not complete

For `mixed` indexing, the file rows should carry per-file status such as:

- `full`
- `path_only`
- `skipped_patch_too_large`
- `skipped_line_count_too_large`
- `binary`
- `submodule`
- `symlink`
- `mode_only`

That lets the API explain what is exact and what is coarse.

This is required for production reliability.

If a PR touches an absurd amount of code, such as hundreds of thousands of changed lines or more, the system must remain queryable and operational instead of attempting an expensive full parse.

### Freshness And Invalidity

Indexing level is not enough. The worker must also track freshness.

The per-PR snapshot should carry a freshness state such as:

- `current`
- `stale_head_changed`
- `stale_base_moved`
- `stale_merge_base_changed`
- `rebuilding`
- `failed`

A PR snapshot is only current if all of these still match:

- repository
- PR number
- head SHA
- base SHA
- merge-base SHA

If any of them change, the current snapshot must be superseded.

### Index Status API

The change-index layer must expose its own status explicitly.

This is required because a search miss is otherwise ambiguous.

Without status endpoints, clients cannot tell the difference between:

- no overlap exists
- the relevant PR was never indexed
- the snapshot exists but is stale
- the snapshot exists only as `paths_only`
- the snapshot failed and needs repair

The clean place for this is the change namespace, not the GitHub namespace.

The required routes are:

- `GET /v1/changes/repos/{owner}/{repo}/status`
- `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}/status`

These endpoints are `ghreplica` product surface, not GitHub compatibility surface.

The repo-level status should report:

- whether the repo has a mirror
- whether the mirror is healthy
- last fetch time
- last successful index time
- whether a fetch or rebuild is in progress
- counts of indexed PRs by state:
  - open
  - closed
  - merged
- counts of snapshots by indexing level:
  - `full`
  - `mixed`
  - `paths_only`
  - `oversized`
  - `failed`
- counts of snapshots by freshness:
  - `current`
  - `stale_head_changed`
  - `stale_base_moved`
  - `stale_merge_base_changed`
  - `rebuilding`
  - `failed`
- whether repo-wide coverage is complete for the configured target set, for example all open PRs

The PR-level status should report:

- `pull_request_number`
- `head_sha`
- `base_sha`
- `merge_base_sha`
- `indexed_as`
- `index_freshness`
- `indexed_at`
- `last_attempted_at`
- whether a rebuild is currently running
- per-file coarse coverage counts, for example:
  - `fully_indexed_file_count`
  - `path_only_file_count`
  - `skipped_file_count`
- aggregate diff counts:
  - `changed_files`
  - `additions`
  - `deletions`
  - `hunk_count`
- the reason for degraded indexing where relevant, for example:
  - `patch_too_large`
  - `line_count_too_large`
  - `rename_limit_exceeded`
  - `binary_only`

These endpoints should be cheap reads from the materialized PR snapshot tables and repo-level bookkeeping tables.

They should not trigger indexing work on read.

The primary motivation is operational honesty:

- search results become interpretable
- CLI output can explain missing matches
- operators can tell whether a repo needs backfill or repair
- downstream tools can decide when to trust range-overlap results versus path-only results

### Worker Concurrency And Safety

The worker design must assume concurrent events.

There must be a repo-scoped lock or lease so that:

- only one fetch/index job mutates a given bare mirror at a time
- only one snapshot rebuild for the same PR/head/base tuple runs at a time

Without that, concurrent webhook bursts will create avoidable races and wasted recomputation.

## Postgres Tables To Add

The exact names can be finalized during implementation, but the model should include tables equivalent to:

- `git_refs`
- `git_commits`
- `git_commit_parents`
- `git_commit_parent_files`
- `git_commit_parent_hunks`
- `pull_request_heads`
- `pull_request_change_snapshots`
- `pull_request_change_files`
- `pull_request_change_hunks`
- `pull_request_overlap_cache`

Important indexing requirements:

- repository plus commit SHA
- repository plus commit SHA plus parent edge
- repository plus path
- repository plus PR number
- GiST indexes for hunk line-range overlap

## Concrete Schema Rules

The schema should follow one simple rule:

- do not invent schemas where GitHub already defines one
- do not invent schemas where Git already defines one
- only define custom schema for derived query and search data

This keeps the storage model defensible and easy to reason about.

### 1. GitHub-Shaped Tables

These tables should stay close to GitHub's resource model.

Examples:

- `repositories`
- `users`
- `issues`
- `pull_requests`
- `issue_comments`
- `pull_request_reviews`
- `pull_request_review_comments`

Motivation:

- these objects already have an external contract
- clients already expect GitHub-like shapes and identities
- `ghreplica` should not create a second invented model for them

So for these tables:

- keep GitHub IDs
- keep GitHub field names where practical
- preserve GitHub relationships such as issue-to-PR linkage
- preserve URLs, state fields, and timestamps in GitHub terms

The important published shapes to preserve are:

- pull request file objects from GitHub's pull-files and compare responses
  - `sha`
  - `filename`
  - `previous_filename`
  - `status`
  - `additions`
  - `deletions`
  - `changes`
  - `blob_url`
  - `raw_url`
  - `contents_url`
  - `patch`
- pull request review objects
  - `id`
  - `node_id`
  - `body`
  - `state`
  - `html_url`
  - `pull_request_url`
  - `submitted_at`
  - `commit_id`
  - `author_association`
- pull request review comment objects
  - `pull_request_review_id`
  - `commit_id`
  - `original_commit_id`
  - `diff_hunk`
  - `path`
  - `position`
  - `original_position`
  - `line`
  - `original_line`
  - `start_line`
  - `original_start_line`
  - `side`
  - `start_side`
  - `in_reply_to_id`
  - `author_association`
  - `html_url`
  - `pull_request_url`

That means the current canonical tables should grow to hold these published GitHub fields before `/v1/github/...` is treated as stable.

### 2. Git-Shaped Tables

These tables should follow Git's own object model.

Examples:

- `git_refs`
- `git_commits`
- `git_commit_parents`

A concrete shape should look roughly like:

- `git_refs`
  - `repository_id`
  - `ref_name`
  - `target_oid`
  - `target_type`
  - `peeled_commit_sha`
  - `is_symbolic`
  - `symbolic_target`
  - `updated_at`

- `git_commits`
  - `repository_id`
  - `sha`
  - `tree_sha`
  - `author_name`
  - `author_email`
  - `authored_at`
  - `authored_timezone_offset`
  - `committer_name`
  - `committer_email`
  - `committed_at`
  - `committed_timezone_offset`
  - `message`
  - `message_encoding`

- `git_commit_parents`
  - `repository_id`
  - `commit_sha`
  - `parent_sha`
  - `parent_index`

Motivation:

- commits and refs are already defined by Git
- refs are broader than branch heads and can be symbolic or point at tag objects
- commit SHA is the stable identity for code state
- branch names and PR numbers are not stable enough to be the base truth

These tables should be a relational mirror of Git, not a new abstraction layer.

Two further rules matter here:

- do not model a commit as if it stored a built-in diff, because Git stores snapshots plus parent links
- do not create relational `git_trees` or `git_blobs` tables unless a product query truly needs them, because the bare mirror already stores that object graph

### 3. Derived Change Index Tables

This is where `ghreplica` should define its own schema.

These tables do not exist in GitHub or Git as first-class queryable entities, but they are needed for overlap and search features.

Examples:

- `git_commit_parent_files`
- `git_commit_parent_hunks`
- `pull_request_heads`
- `pull_request_change_snapshots`
- `pull_request_change_files`
- `pull_request_change_hunks`
- `pull_request_overlap_cache`

A concrete shape should look roughly like:

- `git_commit_parent_files`
  - `repository_id`
  - `commit_sha`
  - `parent_sha`
  - `parent_index`
  - `path`
  - `previous_path`
  - `status`
  - `blob_sha`
  - `previous_blob_sha`
  - `additions`
  - `deletions`
  - `changes`
  - `patch_text`

- `git_commit_parent_hunks`
  - `repository_id`
  - `commit_sha`
  - `parent_sha`
  - `parent_index`
  - `path`
  - `hunk_index`
  - `old_start`
  - `old_count`
  - `new_start`
  - `new_count`
  - `diff_hunk`
  - `old_line_range`
  - `new_line_range`

- `pull_request_heads`
  - `repository_id`
  - `pull_request_number`
  - `head_sha`
  - `base_sha`
  - `merge_base_sha`
  - `updated_at`

- `pull_request_change_files`
  - `repository_id`
  - `pull_request_number`
  - `head_sha`
  - `base_sha`
  - `merge_base_sha`
  - `path`
  - `previous_path`
  - `status`
  - `head_blob_sha`
  - `base_blob_sha`
  - `additions`
  - `deletions`
  - `changes`
  - `patch_text`

- `pull_request_change_hunks`
  - `repository_id`
  - `pull_request_number`
  - `head_sha`
  - `base_sha`
  - `merge_base_sha`
  - `path`
  - `hunk_index`
  - `diff_hunk`
  - `old_start`
  - `old_count`
  - `new_start`
  - `new_count`
  - `old_line_range`
  - `new_line_range`

Motivation:

- these are the actual query surfaces for file and range overlap
- Git itself does not provide a relational query model for these concepts
- GitHub does not provide a reusable search schema for them either
- this is the right place to define `ghreplica`-specific storage

The important design constraint is:

- do not introduce a separate `line_ranges` table unless profiling proves the hunk tables are not enough
- the hunk rows already carry the real overlap boundaries, and Postgres range indexes can live directly on those rows

### 4. PR-Level Materialized View Tables

PR-level query state should be treated as a materialized view over Git truth.

Examples:

- `pull_request_heads`
- `pull_request_change_snapshots`
- `pull_request_change_files`
- `pull_request_change_hunks`

Motivation:

- product queries are often phrased in PR terms
- commits are the stable truth, but PRs are the main user-facing query object
- force-push and rebase support requires current PR state to be rebuilt from current head/base SHAs

The rule here is:

- commit-level tables are the durable raw layer
- PR-level tables are the current rolled-up layer

### 5. Naming Guidance

Use naming that makes the layer obvious:

- GitHub resources: plain names or `github_*` only if needed for clarity
- Git truth: `git_*`
- derived index/search tables: `change_*`, `overlap_*`, or PR-specific names

Recommended pattern:

- `repositories`, `issues`, `pull_requests`
- `git_refs`, `git_commits`, `git_commit_parents`
- `git_commit_parent_files`, `git_commit_parent_hunks`
- `pull_request_heads`, `pull_request_change_snapshots`, `pull_request_change_files`, `pull_request_change_hunks`

This keeps the storage model readable:

- what came from GitHub
- what came from Git
- what `ghreplica` derived for search

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

## API Shape

The API should be separated into three surfaces.

### 1. GitHub-Compatible Surface

This surface should remain GitHub-shaped.

All of these endpoints should live under `/v1/github/...`.

Examples:

- `/v1/github/repos/{owner}/{repo}`
- `/v1/github/repos/{owner}/{repo}/issues/{number}`
- `/v1/github/repos/{owner}/{repo}/pulls/{number}`

This surface is for normal mirrored GitHub resources and should continue to follow GitHub's contracts closely.

### 2. Change Surface

This surface should expose normalized Git-backed change truth.

All of these endpoints should live under `/v1/changes/...`.

Examples:

- `/v1/changes/repos/{owner}/{repo}/commits/{sha}`
- `/v1/changes/repos/{owner}/{repo}/commits/{sha}/files`
- `/v1/changes/repos/{owner}/{repo}/pulls/{number}`
- `/v1/changes/repos/{owner}/{repo}/pulls/{number}/files`
- `/v1/changes/repos/{owner}/{repo}/compare/{base}...{head}`

This surface is for exact Git-derived change data that is not necessarily a GitHub API contract but is still objective and explainable.

### 3. Search Surface

This surface should expose overlap and related-change queries.

All of these endpoints should live under `/v1/search/...`.

Examples:

- `/v1/search/repos/{owner}/{repo}/pulls/{number}/related?mode=path_overlap`
- `/v1/search/repos/{owner}/{repo}/pulls/{number}/related?mode=range_overlap`
- `/v1/search/repos/{owner}/{repo}/pulls/by-paths`
- `/v1/search/repos/{owner}/{repo}/pulls/by-ranges`

This separation keeps the API clean:

- GitHub-native resources stay under `/v1/github/...`
- Git-backed normalized change data lives under `/v1/changes/...`
- overlap and similarity queries live under `/v1/search/...`

Search responses should be explainable. They should include not only the related PRs, but also why they matched, for example:

- matched file paths
- overlapping line ranges
- score
- match mode

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
