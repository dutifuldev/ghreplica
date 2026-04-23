---
title: Immediate PR Webhook Ingest Plan
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

Treat the `pull_request` webhook as the source of truth for PR existence.

The intended behavior is:

- when GitHub sends a `pull_request` webhook, the mirror should create or update the canonical repository, issue, and pull request rows immediately from the webhook payload
- slower follow-up work should happen later in the background
- background backlog should never prevent a new PR from appearing in the mirror

This is the clean long-term shape because it separates:

- existence of the GitHub object
- slower repair and indexing work

# Scope

This plan is for the open-PR freshness problem.

It includes:

- immediate canonical PR ingest from `pull_request` webhooks
- background indexing and repair after the canonical row exists
- queue behavior for fresh PRs versus old failing backlog
- status behavior when open-PR inventory is stale

It does not include:

- a full sync architecture redesign
- a new external operator API
- changing the GitHub-compatible read surface

# Root Cause

The current system still lets slow background work decide whether a fresh PR becomes visible quickly enough.

In practice:

- old targeted refresh rows are selected first
- some of them keep timing out
- fresh PRs can sit behind those older failures
- the inventory status can also stay stale and report `open_pr_missing = 0` even when fresh PRs are clearly absent

So there are really two problems:

- the ingest path for fresh PRs is too dependent on slower background work
- the status path can be stale and misleading at the same time

# Pinned Contracts From Existing Real Webhook Data

The following contracts are grounded in the current production `webhook_deliveries` rows for `openclaw/openclaw`, not just code shape.

The recent retained sample from production on `2026-04-23` showed these actions in the most recent `5000` webhook rows:

- `pull_request`: `opened`, `synchronize`, `labeled`, `unlabeled`, `review_requested`, `edited`
- `pull_request_review`: `submitted`
- `pull_request_review_comment`: `created`

Within those real stored rows:

- every sampled `pull_request`, `pull_request_review`, and `pull_request_review_comment` payload included both `repository` and `pull_request`
- every sampled `pull_request` payload included `title`, `state`, `head`, `base`, and `user`
- the recent sampled `edited` payloads carried `changes.body`, not a base-ref change

This means the following contracts are safe to pin down.

## 1. A Valid Stored `pull_request` Webhook Payload Is Canonical Enough To Write The PR Row

Recent real `pull_request` webhook rows for actions like `opened`, `synchronize`, `labeled`, `unlabeled`, `review_requested`, and `edited` all carried the full canonical `pull_request` object shape that the mirror needs.

So the contract should be:

- if `ghreplica` receives a valid `pull_request` webhook payload, it should write the canonical repository, issue, and pull request rows immediately from that payload

## 2. PR Existence Must Come From Stored Canonical Rows, Not Later Repair

The live payloads already contain the canonical PR object shape, and the GitHub-compatible PR read API serves from stored `pull_requests.raw_json`.

So the contract should be:

- once the canonical pull request row is written successfully, `GET /pulls/:number` must work even if slower follow-up work has not finished yet

## 3. Inventory Refresh Is A Smaller Contract Than Canonical PR Ingest

The real payload stream supports a narrower inventory contract than the ingest contract.

Recent production payloads prove that many `pull_request` actions carry enough data to refresh the PR row even though they do not imply an open-PR membership change.

So the contract should be:

- canonical PR row updates happen for every valid `pull_request` webhook payload
- open-PR inventory refresh only happens for actions that change open membership or base-branch shape

Today that means:

- `opened`
- `closed`
- `reopened`
- `edited` only when the payload actually shows a base-ref change

## 4. `synchronize` Is Targeted PR Follow-Up Work, Not Full Inventory Work

Recent real `synchronize` webhook rows carried a full `pull_request` object, but their sampled `changes` data did not imply an open-PR membership change.

So the contract should be:

- `synchronize` updates the canonical PR row immediately
- `synchronize` queues targeted follow-up work
- `synchronize` does not by itself force a full open-PR inventory refresh

## 5. Review And Review-Comment Events Are Not Inventory Events, But They Do Carry Canonical PR Data

Recent real `pull_request_review submitted` and `pull_request_review_comment created` payloads both carried `repository` and `pull_request`.

So the contract should be:

- `pull_request_review` and `pull_request_review_comment` do not control open-PR inventory
- but they can still refresh canonical PR state because the payloads include the PR object
- they should not be treated as existence-critical for a new PR, because the primary existence path should still be the `pull_request` webhook

## 6. `edited` Needs Payload-Based Gating, Not Action-Based Guessing

The recent real `edited` samples for `openclaw/openclaw` all changed the body, not the base ref.

So the contract should be:

- `edited` should only dirty inventory when the payload itself shows a base-ref change
- ordinary body or metadata edits should still refresh the canonical PR row, but they should not trigger a full inventory refresh

## 7. Status Honesty Is A Required Product Behavior, Not A Payload Contract

The production failure where fresh PRs were missing while `open_pr_missing` still reported `0` is real, but it is not something the webhook payload alone can define.

So this should be treated as a required product behavior:

- if inventory is stale or known-dirty, completeness counts must be marked stale or unknown
- stale inventory must not present `0 missing` as if that were current truth

# Pinned Implementation Defaults

The following implementation choices are now pinned down as the default production shape for the first rollout.

## 1. Immediate Write Scope

The synchronous `pull_request` webhook path should write these rows in one transaction:

- `repositories`
- `issues`
- `pull_requests`

That transaction is the existence boundary for the PR.

If it commits successfully:

- `GET /pulls/:number` must work
- list reads must be able to include the PR

If later indexing fails, the PR still exists.

## 2. First-Cut Event Scope

Phase 1 should keep the existence path simple:

- only `pull_request` webhooks are responsible for immediate PR existence

For phase 1:

- `pull_request_review` and `pull_request_review_comment` are allowed to keep their current update behavior
- but the system must not depend on them for a new PR to appear

This keeps the first implementation aligned with the clearest GitHub event contract.

## 3. Out-Of-Order Webhook Freshness Rule

Canonical PR upserts must be freshness-gated.

The rule is:

- if there is no stored PR row yet, write the incoming webhook payload
- if there is a stored PR row, compare `pull_request.updated_at`
- only overwrite the stored canonical row when the incoming `updated_at` is strictly newer than the stored `updated_at`
- if the timestamps are equal, keep the existing stored row

This avoids an older webhook arriving late and rolling the PR backward.

The same rule should apply to the canonical issue projection derived from the PR payload.

## 4. Targeted Refresh Queue Ordering

The targeted refresh queue should prefer freshness over historical fairness.

Selection order should be:

1. rows with `attempt_count = 0`
2. within that set, newest `requested_at` first
3. retryable rows whose `next_attempt_at <= now()`
4. within retryable rows, newest `requested_at` first

This makes new or never-attempted PR work outrank old failing backlog.

## 5. Retry And Park Policy

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

## 6. Status Shape For Stale Inventory

When inventory is stale or dirty, completeness counts must not pretend to be current.

The default status shape should be:

- keep the existing numeric fields for current snapshots
- add `open_pr_missing_stale: true` when the snapshot is stale or dirty
- when stale, set `open_pr_missing` to `null`

That makes stale status machine-readable and prevents a false `0`.

## 7. Success Metric

The rollout should be judged against a concrete operator-facing SLO.

Default success metric:

- for a new `pull_request opened` webhook, the PR should be readable from `GET /pulls/:number` within `30s` of webhook receipt

That is the main product contract this change is trying to restore.

## 8. Minimum Observability

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

1. GitHub sends `pull_request` webhook data.
2. `ghreplica` writes the canonical repository, issue, and pull request rows immediately.
3. The PR becomes readable through the GitHub-compatible read API right away.
4. Background work fills in the slower parts later.

Those slower parts include:

- git indexing
- diff and hunk extraction
- search indexing
- repair of stale or missing related rows

If the slower background work fails, the PR should still exist.

# Implementation Plan

## 1. Make Immediate PR Existence A Hard Rule

The `pull_request` webhook path should guarantee that the mirror stores the canonical PR object from the webhook payload itself.

That means:

- repository row upsert
- issue row upsert
- pull request row upsert

This write must be the thing that makes the PR visible through:

- `GET /v1/github/repos/:owner/:repo/pulls/:number`
- `GET /v1/github/repos/:owner/:repo/pulls`

The visible existence of the PR must not depend on later indexing work.

Pinned default:

- do the `repositories`, `issues`, and `pull_requests` writes in one transaction
- gate canonical overwrite by strictly newer `pull_request.updated_at`

## 2. Split Fast Canonical Writes From Slow Work

After the canonical PR row exists, enqueue slower follow-up work separately.

That follow-up work should include:

- pull request change indexing
- git mirror sync needed for indexing
- targeted refresh or repair if related objects are incomplete

If that slower work fails, keep the canonical PR row and record the follow-up failure separately.

The important rule is:

- failure to index is not the same thing as failure to ingest the PR

Pinned default:

- phase 1 depends only on `pull_request` for immediate PR existence
- review-side events may continue to refresh detail or canonical rows, but they are not part of the existence guarantee

## 3. Prioritize Fresh PRs Over Old Failed Backlog

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

## 4. Keep Status Honest

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

## 5. Keep Repair And Inventory Separate From Existence

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
- old failing targeted refresh rows no longer block newer PR visibility
- stale inventory no longer reports `open_pr_missing = 0` as current truth

# Rollout

## Phase 1. Guarantee Immediate Canonical PR Writes

Change the webhook path so a fresh `pull_request` event is enough to create the canonical PR row immediately.

Success means:

- a fresh PR becomes readable quickly after webhook delivery
- a failed index no longer makes the PR look missing

## Phase 2. Reorder The Targeted Refresh Queue

Change targeted refresh selection so new work outranks stale failing backlog.

Success means:

- fresh PRs do not wait behind much older repeatedly failing rows
- old failures stop dominating the queue

## Phase 3. Make Status Honest

Update the repo status endpoint so stale inventory cannot falsely imply completeness.

Success means:

- operators can tell when missing counts are current
- `0 missing` stops appearing during stale inventory windows unless it is actually current

# Validation

The implementation should be considered successful when:

- a fresh `pull_request` webhook makes the PR readable quickly without requiring manual sync
- a failed indexing pass does not remove or hide the canonical PR row
- fresh PRs are processed ahead of old repeatedly failing targeted refresh rows
- stale inventory status is clearly marked as stale or unknown instead of pretending it is current

# Future Follow-Up

Once immediate canonical ingest is reliable, there are still worthwhile follow-ups:

- reduce the amount of work done inside webhook processing while keeping the canonical row write fast
- add better operator visibility for stuck targeted refresh rows
- add explicit dead-letter handling for repeated targeted refresh failures

Those are follow-up improvements.

The core fix is simpler:

- make the PR exist immediately
- do the slow work later
