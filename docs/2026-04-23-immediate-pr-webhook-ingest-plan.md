---
title: Immediate Canonical Webhook Projection Plan
date: 2026-04-23
status: proposed
supersedes: []
---

# Problem

`ghreplica` is healthy enough to serve reads, but fresh open PRs can still be missing from the mirror for too long.

The observed production failure mode is:

- GitHub creates a new PR
- the mirror still returns `404` for that PR
- older targeted refresh rows keep running first
- some of those older rows fail with `context deadline exceeded`
- new PRs wait behind that backlog instead of appearing quickly

This creates the wrong product behavior.

For a GitHub-compatible mirror, a new PR should exist in the mirror quickly after GitHub sends the `pull_request` webhook.

# Decision

Use one consistent rule for lightweight GitHub webhook payloads:

- if the payload already contains a canonical GitHub object, write that object immediately in the webhook accept path
- keep slow enrichment and repair work in the background

The pinned end state for this document is:

- `issues` immediately projects `repositories` and `issues`
- `issue_comment` immediately projects `repositories`, `issues`, and `issue_comments`
- `pull_request` immediately projects `repositories`, `issues`, and `pull_requests`
- `pull_request_review` immediately projects `repositories`, `pull_requests`, and `pull_request_reviews`
- `pull_request_review_comment` immediately projects `repositories`, `pull_requests`, and `pull_request_review_comments`

This is the clean long-term shape because it separates:

- existence of GitHub-shaped rows
- slower indexing, repair, and completeness work

`pull_request` was the first visible failure, but it should not stay a special case forever.

# Scope

This plan is for a consistent immediate canonical projection model across the lightweight webhook families that are already proven by retained production data.

It includes:

- immediate canonical projection for `issues`
- immediate canonical projection for `issue_comment`
- immediate canonical projection for `pull_request`
- immediate canonical projection for `pull_request_review`
- immediate canonical projection for `pull_request_review_comment`
- a shared immediate projector used by both webhook accept and async processing
- honest stale-status reporting for open-PR inventory

It does not include:

- immediate `push` projection
- a new remote operator API
- backfill redesign
- git, diff, hunk, or search indexing in the synchronous webhook path
- immediate `repository` event projection, which is explicitly `TBD` because the current retained production sample does not include any `repository` webhook rows to pin that contract from real data

# Root Cause

The original production failure was that a fresh PR could stay missing because the system still depended too much on slower background work.

The broader design problem is inconsistency:

- `pull_request` now has an immediate fast path
- other lightweight webhook families still wait for the async worker even though their payloads already carry enough canonical data

That leaves the mirror with mixed behavior for similar GitHub objects.

# Pinned Contracts From Existing Real Webhook Data

The following contracts are grounded in the retained production `webhook_deliveries` rows for `openclaw/openclaw`, not just code shape.

The recent retained sample from production on `2026-04-23` showed this action mix in the most recent `5000` rows for `repository_id = 21`:

- `issue_comment`: `created 457`, `deleted 10`, `edited 79`
- `issues`: `closed 130`, `edited 8`, `labeled 102`, `locked 142`, `opened 19`, `unlabeled 13`
- `pull_request`: `assigned 14`, `closed 89`, `converted_to_draft 2`, `edited 44`, `labeled 189`, `opened 40`, `ready_for_review 4`, `review_requested 2`, `synchronize 216`, `unlabeled 17`
- `pull_request_review`: `submitted 95`
- `pull_request_review_comment`: `created 116`
- `push`: `98`
- there were no retained `repository` webhook rows in the current sample window

The object-shape checks below are taken from direct reads of those retained production rows.

## 1. `issues` Is Canonical Enough For Immediate `repository + issue` Projection

From the retained sample:

- `417/417` inspected `issues` rows had `repository`
- `417/417` inspected `issues` rows had `issue`
- `0/417` inspected `issues` rows had `issue.pull_request`

So the contract is:

- a valid `issues` webhook payload is enough to immediately write the canonical repository row and canonical issue row
- `issues` is not a PR-existence event unless `issue.pull_request` is present, and the current retained sample did not show that

## 2. `issue_comment` Is Canonical Enough For Immediate `repository + issue + issue_comment` Projection

From the retained sample:

- `552/552` inspected `issue_comment` rows had `repository`
- `552/552` inspected `issue_comment` rows had `issue`
- `552/552` inspected `issue_comment` rows had `comment`
- `302/552` inspected `issue_comment` rows had `issue.pull_request`

So the contract is:

- a valid `issue_comment` payload is enough to immediately write the canonical repository row, issue row, and issue-comment row
- some issue comments are attached to PR-backed issues, but `issue_comment` is still not the primary PR-existence event

## 3. `pull_request` Is Canonical Enough For Immediate `repository + issue + pull_request` Projection

From the retained sample:

- `623/623` inspected `pull_request` rows had `repository`
- `623/623` inspected `pull_request` rows had `pull_request`

So the contract is:

- a valid `pull_request` payload is enough to immediately write the canonical repository row, canonical issue row for that PR number, and canonical pull-request row
- once that transaction commits, PR reads must work even if slower indexing has not finished

## 4. `pull_request_review` Is Canonical Enough For Immediate `repository + pull_request + review` Projection

From the retained sample:

- `94/94` inspected `pull_request_review` rows had `repository`
- `94/94` inspected `pull_request_review` rows had `pull_request`
- `94/94` inspected `pull_request_review` rows had `review`

So the contract is:

- a valid `pull_request_review` payload is enough to immediately write the canonical repository row, refresh the canonical pull-request row, and write the review row
- this event is not an open-PR inventory event by itself

## 5. `pull_request_review_comment` Is Canonical Enough For Immediate `repository + pull_request + review_comment` Projection

From the retained sample:

- `115/115` inspected `pull_request_review_comment` rows had `repository`
- `115/115` inspected `pull_request_review_comment` rows had `pull_request`
- `115/115` inspected `pull_request_review_comment` rows had `comment`

So the contract is:

- a valid `pull_request_review_comment` payload is enough to immediately write the canonical repository row, refresh the canonical pull-request row, and write the review-comment row
- this event is not an open-PR inventory event by itself

## 6. `push` Stays Async

From the retained sample:

- the latest retained `push` row had `repository`
- the current retained `push` sample did not give the same kind of full canonical object coverage as the issue and PR families above

So the contract is:

- `push` remains an async event for this change
- do not force it into the immediate canonical projector

## 7. `repository` Is `TBD`

There were no retained `repository` webhook rows in the current production sample window.

So the contract is:

- do not guess a `repository` immediate-write contract from memory or code shape
- treat `repository` as `TBD` for this document
- leave `repository` out of the immediate set until a real retained sample is inspected

## 8. `edited` Must Be Gated By Real Payload Content

From the retained sample:

- `issue_comment edited`: `81/81` had `changes.body`
- `issues edited`: `8/8` had `changes.body`
- `pull_request edited`: `44` total
  - `37` had `changes.body`
  - `7` had `changes.title`
  - `0` had `changes.base`

So the contract is:

- `edited` should refresh canonical rows for the affected object
- `pull_request edited` should only dirty open-PR inventory when the payload actually shows a base-ref change
- in the current retained sample, `edited` was not a base-change signal

## 9. Status Honesty Is A Required Product Rule

The production failure where fresh PRs were missing while `open_pr_missing` still reported `0` is real, but it is not a webhook-payload contract.

So this should be treated as a required product rule:

- if inventory is stale or known-dirty, completeness counts must be marked stale or unknown
- stale inventory must not present `0 missing` as current truth

# Pinned Implementation Defaults

The following implementation choices are now pinned down as the default production shape for the end state.

## 1. Use One Shared Immediate Projector

There should be one shared immediate projector that knows how to project the pinned lightweight webhook families.

That shared projector should be used in two places:

- the webhook accept path, inside the delivery insert transaction
- the async webhook processor, so there is still only one projection implementation

The accept path and the async path must not drift apart.

## 2. Immediate Write Scope By Event Family

The synchronous webhook path should write these canonical rows in one transaction:

- `issues` -> `repositories`, `issues`
- `issue_comment` -> `repositories`, `issues`, `issue_comments`
- `pull_request` -> `repositories`, `issues`, `pull_requests`
- `pull_request_review` -> `repositories`, `pull_requests`, `pull_request_reviews`
- `pull_request_review_comment` -> `repositories`, `pull_requests`, `pull_request_review_comments`

That transaction is the existence boundary for those GitHub-shaped rows.

If it commits successfully, the corresponding GitHub-shaped read should work even if slow follow-up work has not finished yet.

## 3. Async Work Stays Async

The synchronous webhook path must not grow into a full indexing pipeline.

These stay async:

- `push`
- git mirror fetch and repair
- diff and hunk extraction
- search indexing
- targeted refresh execution
- backfill
- inventory scans

The immediate path is for existence and cheap canonical updates, not for heavy enrichment.

## 4. Freshness And Overwrite Rules

Immediate projection should reuse the current canonical upsert rules instead of inventing new table-specific logic in the acceptor.

The important rule is:

- older webhook payloads must not roll canonical state backward

For issue and PR rows, that means preserving the existing `updated_at` freshness gates already used by the projector.

For the other immediate families, keep using the current keyed upsert behavior in the projector rather than inventing a second write policy in the accept path.

## 5. Cheap Sync-State Side Effects Stay In The Immediate Transaction

The synchronous webhook path may also record cheap follow-up state that is required for correct scheduling.

For example:

- `pull_request` may note the repo webhook time
- `pull_request` may enqueue targeted PR follow-up
- `pull_request` may mark open-PR inventory dirty when the real payload semantics require it

Do not add heavy side effects there.

## 6. Targeted Refresh Queue Ordering

The targeted refresh queue should prefer freshness over historical fairness.

Selection order should be:

1. rows with `attempt_count = 0`
2. within that set, newest `requested_at` first
3. retryable rows whose `next_attempt_at <= now()`
4. within retryable rows, newest `requested_at` first

This makes new or never-attempted PR work outrank old failing backlog.

## 7. Retry And Park Policy

Retries should use bounded exponential backoff.

Default policy:

- attempt `1`: immediate
- attempt `2`: after `1m`
- attempt `3`: after `5m`
- attempt `4`: after `15m`
- attempt `5`: after `1h`
- after `5` failed attempts, mark the row as parked and stop automatic retries

Parked rows should:

- remain visible to operators
- not block fresh queue work
- be eligible for explicit operator retry later

This avoids infinite retry churn on bad old rows.

## 8. Status Shape For Stale Inventory

When inventory is stale or dirty, completeness counts must not pretend to be current.

The default status shape should be:

- keep the existing numeric fields for current snapshots
- add `open_pr_missing_stale: true` when the snapshot is stale or dirty
- when stale, set `open_pr_missing` to `null`

That makes stale status machine-readable and prevents a false `0`.

## 9. Success Metric

The end state should be judged against concrete operator-facing behavior.

Default success metrics:

- for a new `pull_request opened` webhook, the PR should be readable from `GET /pulls/:number` within `30s` of webhook receipt
- for the other pinned immediate families, the canonical row should be present as soon as the webhook accept transaction commits, without waiting for later indexing work

The PR visibility SLO remains the main externally visible proof.

## 10. Minimum Observability

Phase 1 should ship with enough signal to prove or disprove the design.

Track at least:

- webhook-to-visible latency for new PRs
- immediate canonical upsert failures
- targeted refresh queue depth
- parked targeted refresh row count
- open-PR inventory age

This is enough to debug regressions without adding a large observability project first.

# Target Behavior

The mirror should behave like this:

1. GitHub sends a supported lightweight webhook.
2. `ghreplica` inserts the delivery row.
3. `ghreplica` immediately projects the canonical GitHub-shaped rows from that payload in the same transaction.
4. the canonical object becomes readable right away from stored rows.
5. background work fills in slower derived state later.

Those slower background steps still include:

- git indexing
- diff and hunk extraction
- search indexing
- repair of stale or missing related rows

If that slower background work fails, the canonical GitHub object should still exist.

# Implementation Plan

## 1. Extract One Shared Immediate Projector

Move the immediate projection logic into one shared entrypoint that can handle the pinned event families:

- `issues`
- `issue_comment`
- `pull_request`
- `pull_request_review`
- `pull_request_review_comment`

The acceptor should call that shared projector inside the delivery insert transaction.

The async processor should call the same projector, not a duplicate implementation.

## 2. Keep The Immediate Transaction Small And Deterministic

The synchronous webhook transaction should do only:

- insert `webhook_deliveries`
- project the canonical rows for the supported event family
- record cheap follow-up scheduler state where required
- commit

Do not move git, diff, hunk, or search work into this transaction.

## 3. Preserve The Existing PR Fast-Path Guarantees

The broader immediate model must preserve the guarantees already introduced for `pull_request`:

- canonical PR existence must not wait for indexing
- newer PR state must not be overwritten by older webhook payloads
- cheap targeted-refresh scheduling still happens immediately

## 4. Extend The Same Model To Issues, Comments, And Reviews

Add the same immediate behavior to:

- `issues`
- `issue_comment`
- `pull_request_review`
- `pull_request_review_comment`

Each family should write only the rows that the real retained payloads proved are present.

## 5. Prioritize Fresh PRs Over Old Failed Backlog

The targeted refresh queue should not let old failing rows block fresh PRs.

Selection order should prefer:

- never-attempted rows first
- newest or most recent webhook activity next
- repeatedly failing old rows later

Retries should also be less aggressive for rows that keep failing.

For example:

- use increasing backoff
- track attempt count
- after a threshold, mark the row as stuck or dead-lettered instead of retrying every short interval forever

This keeps one bad old backlog from delaying today’s PRs.

Pinned default:

- first select `attempt_count = 0`, newest first
- then select retryable rows whose `next_attempt_at <= now()`, newest first
- after `5` failed attempts, park the row instead of retrying automatically

## 6. Keep Status Honest

If the open-PR inventory snapshot is stale, the status endpoint should not present `open_pr_missing = 0` as if that were current truth.

When inventory is stale or dirty, status should say so clearly.

Acceptable options:

- mark the missing count as stale or unknown
- expose a flag that tells operators the count is not current

The important rule is:

- stale inventory must not look like complete inventory

Pinned default:

- add `open_pr_missing_stale`
- when stale, return `open_pr_missing = null`

## 7. Keep Repair And Inventory Separate From Existence

Inventory refresh and full-history repair should remain useful, but they should not be the main path that makes a fresh PR appear.

Their job should be:

- reconcile missing rows
- repair stale snapshots
- refresh completeness reporting

Their job should not be:

- decide whether a new PR is visible at all

# Concrete Exit Criteria

This change is complete when all of the following are true:

- a new PR becomes readable from `GET /pulls/:number` within `30s` of webhook receipt
- the PR remains readable even if later indexing fails
- `issues`, `issue_comment`, `pull_request_review`, and `pull_request_review_comment` no longer depend on the async worker to make their canonical rows exist
- old failing targeted refresh rows no longer block newer PR visibility
- stale inventory no longer reports `open_pr_missing = 0` as current truth
- there is one shared immediate projector instead of a growing list of event-specific special cases in the acceptor

# Rollout

Ship this as one coherent change set:

- one shared immediate projector
- one synchronous accept-path integration
- one preserved async processor path that reuses the same projector
- one stale-status behavior
- one targeted-refresh queue policy

Do not split this into a long-lived PR-only fast path and a separate “someday” model for the other lightweight families.

# Validation

The implementation should be considered successful when:

- a fresh `pull_request` webhook makes the PR readable quickly without requiring manual sync
- a failed indexing pass does not remove or hide the canonical PR row
- a fresh `issues` webhook makes the issue row readable without waiting for async processing
- a fresh `issue_comment` webhook makes the comment row readable without waiting for async processing
- a fresh `pull_request_review` webhook makes the review row readable without waiting for async processing
- a fresh `pull_request_review_comment` webhook makes the review-comment row readable without waiting for async processing
- fresh PRs are processed ahead of old repeatedly failing targeted refresh rows
- stale inventory status is clearly marked as stale or unknown instead of pretending it is current

# Future Follow-Up

Once immediate canonical ingest is reliable, there are still worthwhile follow-ups:

- reduce the amount of work done inside webhook processing while keeping the canonical row write fast
- add better operator visibility for stuck targeted refresh rows
- add explicit dead-letter handling for repeated targeted refresh failures

Those are follow-up improvements.

The core fix is simpler:

- make GitHub-shaped canonical rows exist immediately from lightweight webhook payloads
- do the slow work later
