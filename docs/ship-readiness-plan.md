# Ship Readiness Plan

This document turns the remaining prototype gaps into a concrete path from current state to something that is reasonable to ship.

## Current State

What already works:

- local Postgres-backed development
- PAT-based repository bootstrap sync
- GitHub webhook ingestion
- queued repository refresh jobs
- read endpoints for:
  - `GET /repos/{owner}/{repo}`
  - `GET /repos/{owner}/{repo}/issues`
  - `GET /repos/{owner}/{repo}/pulls`

What is still missing:

- endpoint depth
- deeper GitHub object coverage
- compatibility automation
- worker and operations hardening
- GitHub App support

## Release Levels

### Level 1: Internal Dogfood

Good enough for:

- local development
- controlled repos
- engineering iteration

Required:

- current state plus basic job and sync observability

### Level 2: Private Beta

Good enough for:

- a small number of users
- a few controlled repositories or org installs
- reliability-focused iteration

Required:

- endpoint detail coverage
- comments and reviews
- contract tests for supported endpoints
- worker hardening
- GitHub App onboarding

### Level 3: Public Ship

Good enough for:

- broader public use
- stronger compatibility claims
- sustained operation under load

Required:

- robust retry and repair behavior
- strong observability
- documented limits and support surface
- CI-enforced API parity for the supported subset

## Phase 1: Complete The Initial Read Surface

Goal:

Make the mirror usable for actual PR triage flows rather than only list views.

Scope:

- add `GET /repos/{owner}/{repo}/issues/{number}`
- add `GET /repos/{owner}/{repo}/pulls/{number}`
- sync and serve:
  - issue comments
  - pull request reviews
  - pull request review comments

Implementation notes:

- keep canonical tables aligned with the existing data model doc
- keep response shapes GitHub-compatible even if the backend remains incomplete
- prefer repo-scoped endpoints over broad API expansion

Exit criteria:

- a PR triage tool can inspect one PR in detail
- review state and discussion can be reconstructed from mirrored data

## Phase 2: Add Compatibility Automation

Goal:

Make supported parity measurable rather than aspirational.

Scope:

- build contract tests that compare GitHub and `ghreplica`
- start with:
  - `GET /repos/{owner}/{repo}`
  - `GET /repos/{owner}/{repo}/issues`
  - `GET /repos/{owner}/{repo}/issues/{number}`
  - `GET /repos/{owner}/{repo}/pulls`
  - `GET /repos/{owner}/{repo}/pulls/{number}`
- compare:
  - status code
  - key headers
  - pagination behavior
  - normalized JSON body

Implementation notes:

- use a small fixture repo that we control
- normalize only fields that must differ, such as hostname or local rate-limit headers
- fail CI on compatibility regressions for supported endpoints

Exit criteria:

- every supported endpoint has a contract test
- CI catches parity regressions automatically

## Phase 3: Harden Refresh Jobs And Worker Execution

Goal:

Make webhook-triggered refreshes dependable enough for continuous use.

Scope:

- add explicit retry policy and bounded retry count
- store failure reason and next retry time
- add backoff for transient GitHub failures
- avoid duplicate refresh work more aggressively
- support manual repair crawl for a tracked repo

Implementation notes:

- keep the current in-process worker for dev
- make worker state explicit in the database
- prefer deterministic SQL state transitions over implicit behavior

Exit criteria:

- failed refreshes are visible and retryable
- transient GitHub failures do not require manual DB edits
- larger repos do not leave ambiguous job state behind

## Phase 4: Add GitHub App Support

Goal:

Move from one-user PAT operation to a real multi-repo integration model.

Scope:

- GitHub App registration and configuration
- installation-scoped auth
- webhook routing by installation
- repo allowlist or installation discovery

Implementation notes:

- keep PAT support for local development
- use app installs for production-style onboarding
- separate local test repo onboarding from production install flow

Exit criteria:

- users can install the app on a repo or org
- webhooks and API access are scoped per installation

## Phase 5: Add Operations And Ship Controls

Goal:

Make the system operable as a service rather than only a development tool.

Scope:

- metrics for:
  - webhook deliveries
  - refresh queue depth
  - refresh duration
  - refresh failure count
  - GitHub API failures and rate-limit pressure
- health/readiness endpoints with job and sync signals
- structured logs around webhook, sync, and worker flows
- explicit supported-endpoint matrix in docs
- documented rate and scale expectations

Exit criteria:

- an operator can tell whether the system is healthy
- the supported API surface is documented precisely
- the failure modes are visible without digging through raw rows

## Recommended Implementation Order

The right order is:

1. issue and pull detail endpoints
2. comments and reviews
3. contract-test harness
4. refresh job retries and repair behavior
5. GitHub App support
6. ops and ship controls

This order is important:

- deeper endpoint coverage makes the product useful
- contract tests stop compatibility drift
- worker hardening prevents operational pain
- App support broadens adoption only after the core is stable

## Minimum Bar For Private Beta

Before calling this private-beta ready, `ghreplica` should have:

- detail endpoints for issues and pulls
- comments and reviews mirrored
- contract tests for all supported endpoints
- retryable refresh jobs
- GitHub App support for controlled installs
- basic metrics and readiness signals

## Minimum Bar For Public Ship

Before calling this publicly shippable, `ghreplica` should additionally have:

- a clearly declared supported API subset
- CI-enforced parity on that subset
- operational docs for setup, recovery, and scaling
- tested behavior on at least one busier real-world repository
