---
title: AST-Grep Structural Search
author: Onur Solmaz <info@solmaz.io>
date: 2026-04-15
---

# 2026-04-15 AST-Grep Structural Search

This document describes the intended design for syntax-aware code search in `ghreplica` using `ast-grep`.

## Goal

`ghreplica` already supports:

- GitHub-shaped mirrored reads under `/v1/github/...`
- git-backed change truth under `/v1/changes/...`
- derived overlap and text search under `/v1/search/...`

The missing search surface is structural code search.

The goal is to let callers ask:

- where in this repo does this code pattern exist
- does this PR introduce or touch this syntax shape
- which files match this structural rule at this exact commit

This should be:

- reproducible
- Git-grounded
- explicit about what commit was searched
- separate from discussion-text search

## Fit With The Project

`ast-grep` belongs under `/v1/search/...`.

It is not:

- GitHub-native resource data
- canonical Git storage
- discussion-text search like `mentions`

It is a derived code-query feature on top of the local Git mirror.

So the clean product split is:

- `/v1/github/...`
  - mirrored GitHub resources
- `/v1/changes/...`
  - git-backed truth and indexes
- `/v1/search/...`
  - overlap search, text search, and structural code search

## Core Rule

Structural search must run against an exact resolved commit SHA.

It must not silently mean "whatever is latest right now."

Allowed caller inputs can be:

- `commit_sha`
- `ref`
- `pull_request_number`

But all execution should resolve to one concrete commit SHA before running search, and that resolved SHA should be returned in the response.

This keeps results reproducible and makes cache keys stable.

## API Shape

The intended endpoint is:

- `POST /v1/search/repos/{owner}/{repo}/ast-grep`

The request should include:

- exactly one target:
  - `commit_sha`
  - `ref`
  - `pull_request_number`
- `language`
- `rule`
- optional `paths`
- optional `changed_files_only`
- optional `limit`

Example request:

```json
{
  "pull_request_number": 59883,
  "language": "typescript",
  "rule": {
    "pattern": "ctx.reply($MSG)"
  },
  "changed_files_only": true,
  "limit": 100
}
```

The response should include:

- `repository`
  - `owner`
  - `name`
  - `full_name`
- `resolved_commit_sha`
- `resolved_ref`
  - when applicable
- `matches`
  - `path`
  - `start_line`
  - `start_column`
  - `end_line`
  - `end_column`
  - `text`
  - `meta_variables`
- `truncated`

`truncated` is important because large repos and broad rules must be bounded.

## CLI Shape

If and when this is exposed in `ghr`, it should live under the existing search group:

- `ghr search ast-grep`

The shape should mirror the API:

- one target:
  - `--commit`
  - `--ref`
  - `--pr`
- `--language`
- `--pattern`
- `--path`
- `--changed-files-only`
- `--limit`

Example:

```bash
ghr search ast-grep \
  -R openclaw/openclaw \
  --pr 59883 \
  --language typescript \
  --pattern 'ctx.reply($MSG)' \
  --changed-files-only
```

## Execution Model

The local Git mirror remains the ground truth.

The execution flow should be:

1. resolve the target to an exact commit SHA
2. locate the repository's bare mirror
3. materialize that tree into a temporary directory
4. optionally narrow the candidate file set
5. run a pinned `ast-grep` binary with structured JSON output
6. parse matches and return a normalized response

This should be query-time execution, not a precomputed persistent AST index.

That is the right first design because:

- the mirror already has the source of truth
- results stay exact and reproducible
- the implementation stays simpler
- the feature can be shipped without inventing a large new indexing subsystem

## Changed-Files-Only Mode

`changed_files_only` should be a first-class option.

If the caller supplies `pull_request_number`, `ghreplica` should already know the changed files from the git-change index.

That means PR-focused structural search can be narrowed to:

- the PR's current changed paths
- and later, if useful, only text files of supported languages

This is the most practical workflow for code review.

It answers:

- does this PR contain this dangerous pattern
- did this PR add another handler of this shape
- where in the changed code does this rule match

without scanning the entire repository tree.

## Temporary Tree Materialization

The search worker should materialize a temporary filesystem tree for the target commit.

That tree should be:

- scoped to one request
- removed after execution
- isolated from the bare mirror

The mirror itself must stay immutable and reusable.

The implementation can use:

- `git archive`
- `git worktree`
- or another safe tree-export mechanism

The important product rule is:

- do not search against a mutable checkout
- do not let search mutate the mirror
- always search a tree that corresponds exactly to the resolved commit

## Caching

Caching should exist, but it should be derived and replaceable.

The cache key should include:

- repository
- resolved commit SHA
- normalized query hash
- `ast-grep` version

That makes repeated structural searches cheap without changing the truth model.

The cache should be treated as an optimization, not as the source of truth.

## Limits

This feature needs explicit hard limits.

At minimum:

- maximum runtime
- maximum number of files searched
- maximum bytes searched
- maximum matches returned
- maximum response payload size

Suggested serving behavior:

- return partial matches up to the limit
- set `truncated=true`
- never let one broad search monopolize the worker

This matters especially for:

- broad repo-wide queries
- large monorepos
- pathological regex-like structural rules

## Operational Model

Structural search should run in a bounded worker path.

That means:

- separate timeouts from normal API handlers
- clear execution logs
- stable error reporting
- explicit query budgets

If inline request execution remains fast enough, that is fine initially.

But the design should leave room for:

- queued execution
- stronger sandboxing
- stricter admission control

without changing the product surface.

## Production Notes

The deployed runtime surfaced two concrete operational requirements:

- the GitHub App private key mount must be readable by the runtime user
- the mounted git-mirror root must be owned by the runtime user

If either of those is wrong, structural search can fail even though the API and binary are present:

- unreadable key material breaks GitHub-authenticated sync and ref refresh
- wrong mirror ownership can make Git reject the local mirror with a `dubious ownership` error

So structural search should be treated as depending on three runtime prerequisites:

- `ast-grep` is installed and executable
- GitHub auth material is readable
- the mirror root is readable and Git-safe for the runtime user

## Error Handling

The API should distinguish between:

- invalid request
- unsupported language
- unsupported rule shape
- missing repo
- unresolved target
- timed out search
- internal execution failure

The response should be explicit when a target could not be resolved to a commit.

## Why Not "Latest"

Searching "latest" by default is convenient but not reproducible.

That is wrong for automation, debugging, and review workflows.

So the rule should be:

- allow a convenience ref like `main`
- resolve it immediately
- always return the exact commit SHA searched

For serious workflows, callers should pin to:

- `commit_sha`
- or PR head

## Why This Is The Clean Design

This design keeps the project's existing layering intact:

- Git stays the deepest truth
- Postgres keeps metadata and change indexes
- `/v1/search/...` stays the place for derived query features

It also avoids premature complexity:

- no giant persistent AST index
- no pretending that search is over "whatever is current"
- no mixing structural code search with discussion-text search

So the design is:

- exact target resolution
- mirror-backed execution
- bounded query-time search
- optional cache
- clear API separation

That is the most elegant and production-ready first version.
