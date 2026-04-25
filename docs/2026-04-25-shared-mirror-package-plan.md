---
title: Shared Mirror Package Plan
date: 2026-04-25
status: proposed
supersedes: []
---

# Problem

`prtags` and `ghreplica` are separate apps, but we want `prtags` to be able to read mirrored GitHub data directly from Postgres and join it with its own curation tables.

We also want to avoid copy-pasting table definitions, row shapes, and read helpers across the two repos.

The main design tension is:

- keep `ghreplica` standalone
- keep `prtags` standalone
- allow shared SQL reads and joins
- avoid duplicating schema and read-model code

# Decision

Create a public Go package in `ghreplica` named `mirror`.

The package should hold the reusable mirror-facing read layer that other apps can import.

The pinned end state is:

- one Postgres database
- schema `ghreplica` for mirrored GitHub data
- schema `prtags` for curation data
- `ghreplica` writes only `ghreplica.*`
- `prtags` writes only `prtags.*`
- `prtags` may read `ghreplica.*` directly
- `prtags` imports `github.com/dutifuldev/ghreplica/mirror` for shared read models and read helpers

There is no third `_ext` schema in this plan.

That extra schema is useful when downstream consumers need a stable SQL contract across raw storage changes.

For this plan, we own both apps and are willing to change them together.

So the cleaner shape is:

- two schemas
- one shared importable read package
- coordinated changes when the mirror schema changes

# Goals

- let `prtags` join mirrored GitHub rows with curation rows
- avoid duplicating core mirror table definitions
- avoid duplicating common mirror read queries
- keep `ghreplica` as the owner of mirrored GitHub data
- keep `ghreplica` free of `prtags`-specific write behavior

# Non-Goals

- merge `ghreplica` and `prtags` into one product
- move `prtags` tables into the `ghreplica` schema
- make `ghreplica` aware of groups, annotations, or other `prtags` concepts
- share webhook ingestion, sync orchestration, or server runtime code with `prtags`
- use runtime automigration for either app

# Why `mirror`

The shared package should be named `mirror`.

The name is broad enough for:

- table-backed read models
- query helpers
- GitHub-shaped `raw_json` decode helpers
- shared identity helpers

It is better than `mirrordb`, which is too narrow if the package grows beyond raw SQL structs.

It is better than importing all of `ghreplica`, which would couple `prtags` to the whole application instead of the shared mirror layer.

The intended import path is:

`github.com/dutifuldev/ghreplica/mirror`

# Scope Of The `mirror` Package

The first version of `mirror` should contain only shared read-side code.

It should include:

- schema-qualified table name constants where useful
- explicit read models for core mirrored tables
- helper query functions for common lookups
- small GitHub-shaped decode helpers when they simplify downstream use

It should not include:

- webhook acceptance
- job workers
- backfill orchestration
- sync completeness logic
- runtime boot code
- CLI commands
- HTTP handlers
- deploy code

# Initial Shared Read Models

The first imported models should be the rows that `prtags` is most likely to join.

Pin the initial set to:

- `Repository`
- `PullRequest`
- `Issue`

Optional early additions if they are needed soon:

- `IssueComment`
- `PullRequestReview`
- `PullRequestReviewComment`

Each persisted model should keep an explicit schema-qualified `TableName()`.

Examples:

- `ghreplica.repositories`
- `ghreplica.pull_requests`
- `ghreplica.issues`

# Initial Shared Read Helpers

The first query helpers should be simple and clearly reusable.

Pin the initial set to read helpers like:

- repository by `owner/name`
- repository by GitHub repository ID
- pull request by repository ID and PR number
- issue by repository ID and issue number
- list pull requests by repository ID and numbers
- list issues by repository ID and numbers

The point is to share the lookups that `prtags` will call repeatedly, not to create a giant general ORM layer on day one.

# Database Shape

Use one Postgres database with two schemas:

- `ghreplica`
- `prtags`

This is the intended ownership model:

- `ghreplica` owns `ghreplica.*`
- `prtags` owns `prtags.*`
- `prtags` may read `ghreplica.*`
- `ghreplica` should not write `prtags.*`

Do not add cross-schema foreign keys from `prtags` into `ghreplica`.

Those joins are useful for reads, but the systems should still be able to evolve without hard relational coupling at the write boundary.

# Go Package Rules

The `mirror` package must be importable from another repo.

That means the shared read layer cannot stay under `internal/`.

The intended shape is:

- `ghreplica/mirror` as a public package
- `prtags` imports that package through `go.mod`
- local multi-repo development may use a `replace` directive

The package should be treated as a real shared surface.

So it should favor:

- explicit models
- explicit functions
- stable naming
- small, obvious dependencies

It should avoid:

- depending on `ghreplica` server/runtime packages
- depending on app boot code
- depending on unrelated sync subsystems

# Import And Versioning Model

`prtags` should add `ghreplica` as a normal module dependency.

For local development, a `replace` is acceptable:

```go
replace github.com/dutifuldev/ghreplica => ../ghreplica
```

For normal use, `prtags` should import:

```go
import "github.com/dutifuldev/ghreplica/mirror"
```

This is intentionally stronger coupling than a fully isolated downstream integration.

That is acceptable here because we own both repos and are willing to version and migrate them together.

# Implementation Plan

## 1. Create The Public Package

Add a top-level `mirror` package in `ghreplica`.

Start with:

- schema-qualified model structs
- small read helpers
- no runtime or write-path code

## 2. Move Shared Read Models Out Of `internal/`

Copy or move the minimum stable read-side definitions needed by `prtags` into `mirror`.

Do not move the whole app.

Only move what is part of the intended shared read surface.

## 3. Keep Table Ownership Explicit

Make sure the shared models continue to use explicit table names and explicit schemas.

Do not rely on default table naming.

Do not rely on automigration.

## 4. Import `mirror` In `prtags`

Update `prtags` to import `github.com/dutifuldev/ghreplica/mirror`.

Use those models and helpers for mirror reads and joins.

## 5. Keep Write Ownership Separate

Even after the import exists:

- `ghreplica` still writes mirrored GitHub data
- `prtags` still writes only its own curation data

That boundary stays important even if the code-sharing boundary becomes looser.

# Operational Consequences

This plan increases code coupling between the repos.

That is deliberate.

The trade is:

- less duplicated code
- simpler shared reads
- easier SQL joins

in exchange for:

- coordinated schema and package changes
- shared versioning pressure between the repos

This is acceptable as long as we stay disciplined about what belongs in `mirror`.

If `prtags` starts importing large pieces of `ghreplica` beyond the shared read layer, then the more honest next step would be a monorepo or a separately versioned shared module.

# Pinned Rules

- use one Postgres database
- use two schemas: `ghreplica` and `prtags`
- create a public `mirror` package in `ghreplica`
- put shared read models and read helpers there
- keep runtime, sync, and webhook code out of `mirror`
- keep explicit table names and explicit migrations
- keep automigration off
- let `prtags` read `ghreplica` data, but not write it

# Recommendation

Implement the first version of `mirror` with only:

- `Repository`
- `PullRequest`
- `Issue`
- a few repository and object lookup helpers

That is enough to prove the design without overcommitting the package too early.
