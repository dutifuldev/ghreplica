# TESTING

This document describes the unit and integration test strategy for `ghreplica`.

It is intentionally focused on local, deterministic coverage:

- unit tests
- package-level integration tests
- fixture-based contract tests

It does not cover live end-to-end or staging verification.

## Fixture Policy

Fixtures should come from two sources:

- small synthetic payloads for focused behavior tests
- sanitized real GitHub payloads that `ghreplica` has already received

For webhook and response regression coverage, real received payloads are preferred whenever they capture shapes that are easy to miss in hand-written fixtures.

That includes:

- pull request payloads with complex nested `head` and `base` objects
- review and review-comment payloads
- issue comment payloads
- repository rename and edit payloads
- bot-authored reviews and comments

These real payloads should be:

- copied into `testdata/`
- sanitized if needed
- frozen as deterministic fixtures
- used in offline tests only

When a stored webhook delivery is not the most practical source for a specific action transition, it is also acceptable to freeze real GitHub or mirrored API object responses and wrap them in test webhook envelopes locally. The object body must still come from real captured data; do not invent GitHub-shaped fixtures from scratch for complex cases.

Normal CI should not depend on live GitHub state.

## Priorities

The highest-value test areas are:

1. webhook projection correctness
2. idempotency and replay safety
3. targeted repair correctness
4. GitHub-shaped response fidelity
5. CLI output and behavior

## 1. Webhook Event Matrix

Each supported webhook family should have explicit tests for the important actions it handles.

### Repository

- `edited`
- `renamed`

Tests should verify:

- repository identity is preserved by stable GitHub ID
- `full_name`, `owner`, and URL fields update correctly
- rename does not create duplicate repository rows

### Issues

- `opened`
- `edited`
- `closed`
- `reopened`
- `labeled`
- `unlabeled`

Tests should verify:

- issue state transitions are applied correctly
- labels and issue metadata update without duplicating rows
- issue comments count and pull-request marker fields remain coherent

### Issue Comments

- `created`
- `edited`
- `deleted`

Tests should verify:

- comment body updates correctly
- delete behavior is applied consistently
- duplicate deliveries do not duplicate comments

### Pull Requests

- `opened`
- `edited`
- `synchronize`
- `ready_for_review`
- `converted_to_draft`
- `closed`
- `reopened`
- `review_requested`
- `review_request_removed`

Tests should verify:

- PR-specific fields update correctly
- the PR-backed issue row stays aligned with the PR row
- synchronize updates head SHA and related metadata without duplicating the PR

### Pull Request Reviews

- `submitted`
- `edited`
- `dismissed`

Tests should verify:

- review state changes are reflected correctly
- review body and submitted timestamps update correctly
- duplicate deliveries remain idempotent

### Pull Request Review Comments

- `created`
- `edited`
- `deleted`

Tests should verify:

- path, position, line, and reply linkage fields are stored correctly
- edits update the existing row rather than inserting duplicates

## 2. Idempotency And Replay

Webhook systems must tolerate redelivery.

Every supported projector should have tests for:

- same delivery processed twice
- same logical object processed through multiple different delivery IDs
- replay after the object already exists in canonical tables

Expected behavior:

- no duplicate canonical rows
- latest valid state wins where GitHub semantics require overwrite
- immutable identity fields stay stable

## 3. Ordering And Repair

The mirror should survive incomplete or awkward event order.

Add tests for:

- comment arrives before the issue exists locally
- review comment arrives before the review exists locally
- repository rename happens before a later issue or PR event
- targeted `sync issue` repairs missing issue comments
- targeted `sync pr` repairs issue comments, reviews, and review comments

Expected behavior:

- the system converges after targeted repair
- repair does not corrupt sync policy or completeness state

## 4. Sync Policy And Completeness

Per-repo sync policy and completeness metadata should be tested directly.

Add tests for:

- webhook-projected repos remain `webhook_only`
- targeted repair does not accidentally switch repos into backfill modes
- completeness fields remain `sparse` unless an explicit backfill path changes them
- mirror-status counts match canonical row counts

## 5. GitHub-Shaped API Responses

The read API should stay close to GitHub for data GitHub already defines.

Add tests for:

- repository response shape
- issue list/view/comment response shape
- pull request list/view/reviews/review-comments response shape
- nested `user`, `owner`, `head`, and `base` object rendering
- null and omitted field behavior where clients may care

These should primarily be fixture-based HTTP handler tests.

Where practical, use sanitized real GitHub responses and real webhook-derived payloads instead of synthetic-only fixtures.

## 6. CLI Coverage

`ghr` should have strong output and behavior tests.

Add tests for:

- human output for non-empty repository, issue, PR, review, and comment results
- empty-state output
- `--json` output for each command family
- `repo status`
- `issue view --comments`
- `pr view --comments`
- `--web`
- repo-selection errors and argument-validation errors

Golden-style snapshots are appropriate here as long as they are kept small and readable.

CLI golden tests should prefer realistic mirrored payload fixtures so formatting stays aligned with the data shapes the service actually stores and returns.

## 7. Migrations And Schema Assumptions

The database model is central to the system and deserves direct coverage.

Add tests for:

- migrations apply cleanly to an empty database
- important uniqueness assumptions remain true
- rename/upsert behavior keyed by GitHub IDs remains correct
- foreign-key relationships required by the read API are preserved

## 8. Operational Logic

The job and readiness logic should also be covered locally.

Add tests for:

- stale lease reclamation
- superseded jobs not degrading readiness
- historical failed jobs not breaking current readiness
- webhook-only serving health remaining independent from old backfill failures

## Recommended Order

The next best order for expanding coverage is:

1. webhook event/action matrix
2. idempotency and replay tests
3. ordering and targeted repair tests
4. GitHub-shaped response tests
5. CLI golden tests
6. migration and operational tests

This order gives the most confidence in the correctness of the mirror while keeping the feedback loop local and deterministic.
