---
title: Repair Engine Refactor Plan
date: 2026-04-21
status: proposed
---

# Repair Engine Refactor Plan

## Goal

Make PR and issue repair easier to reason about, cheaper to operate, and clearer to monitor, without changing the product boundary that is already working in production.

The target shape is:

- one repair engine
- explicit stale detection
- explicit apply step
- clear scheduling policy
- clean separation between repair and full sync
- clearer status and metrics

## Pinned Design Choice

For canonical GitHub object storage, this plan assumes:

- keep only the latest canonical snapshot per object
- do not introduce full historical snapshot retention as part of this refactor

That means the refactor is aimed at a cleaner current source of truth, not at building an object-history archive.

## Current Problems

- repair detection and repair application are mixed together
- recent repair and full-history repair have overlapping but not fully unified logic
- scheduling priority across targeted refresh, recent repair, and full-history repair is hard to reason about
- the boundary between bounded repair and heavier full sync is not clear enough in naming and code structure
- status fields can look noisy or lag reality
- we rely too much on logs for understanding progress

## Refactor Areas

### 1. Split Detection From Application

Create a two-stage repair flow:

1. scan recent PR and issue lists
2. detect stale object numbers by comparing stored vs GitHub `updated_at`
3. fetch full objects only for stale numbers
4. apply updates only for the stale side

This should become the core repair shape for both pull rows and issue-backed PR rows.

Suggested helper shape:

- `scanRecentPullRepairCandidates(...)`
- `scanRecentIssueRepairCandidates(...)`
- `detectStalePulls(...)`
- `detectStaleIssues(...)`
- `applyPullRepairs(...)`
- `applyIssueRepairs(...)`

### 2. Unify Recent And Full-History Repair

Recent repair and full-history repair should share one repair engine.

They should differ only in:

- source window
- cursor policy
- page budget
- scheduling priority

Suggested model:

- `repairModeRecent`
- `repairModeFullHistory`

Both should use the same inner flow:

- scan
- detect
- fetch changed objects
- apply

### 3. Make Scheduler Policy Explicit

Move scheduling decisions into a small policy layer.

The scheduler should decide which of these phases runs next:

- targeted refresh
- recent repair
- full-history repair
- open-PR backfill
- inventory work

Instead of embedding priority decisions across several worker functions, define:

- one clear priority function
- one clear starvation policy
- one explicit same-repo handoff rule where needed

Suggested direction:

- one `nextWorkItem(...)` selector
- explicit repo-aware fairness rules
- explicit handoff rules, especially from incomplete recent repair into same-repo full-history repair

### 4. Clarify Repair vs Full Sync Boundary

Repair should mean:

- bounded stale correction
- list-based detection
- fetch full objects only for changed rows

Full sync should mean:

- broader completeness work
- inventory or crawl-style reconciliation

Names and docs should make these visibly different concepts.

Candidates to rename or clarify:

- helpers that currently imply a full sync when they only repair stale objects
- status fields whose names do not make the bounded-vs-full distinction obvious

### 5. Clean Up Status Reporting

Status should make it easy to answer:

- what phase is running now
- what phase finished last
- whether the last run succeeded
- whether the repo is behind because of stale detection, pending targeted refresh, or broader inventory drift

Suggested improvements:

- group fields by phase
- expose the current active phase clearly
- make `last_error` phase-scoped or time-scoped
- avoid stale status fields making the system look actively broken when the current phase is healthy

### 6. Add Real Metrics

Add metrics for each repair phase instead of relying mainly on logs.

Suggested counters and timings:

- PRs scanned
- issues scanned
- stale PRs detected
- stale issues detected
- PRs repaired
- issues repaired
- unchanged objects skipped
- full-object fetch count
- apply-step write count
- phase duration
- lease acquisition latency
- DB timeout count

Metrics should be split by:

- repo
- phase
- mode (`recent`, `full_history`)

## Implementation Order

### Phase 1: Repair Pipeline Extraction

- extract candidate scan helpers
- extract stale detection helpers
- extract apply helpers
- keep current behavior, but make boundaries explicit

### Phase 2: Shared Repair Engine

- route recent repair through the shared engine
- route full-history repair through the same engine
- keep separate cursors and budgets, but not separate repair mechanics

### Phase 3: Scheduler Cleanup

- add explicit next-work selection
- move priority rules into one place
- preserve the production behavior that prevents targeted-refresh starvation from blocking repair

### Phase 4: Status Cleanup

- simplify or regroup status output
- scope `last_error` and phase timestamps more clearly
- make it obvious when work is progressing versus merely pending

### Phase 5: Metrics

- add counters and durations for scan, detect, fetch, and apply
- add timeout and lease metrics
- verify they work in production

## Testing Plan

### Unit And Service Tests

- stale detection from list payloads based on `updated_at`
- pull-only stale repair
- issue-only stale repair
- mixed PR plus issue stale repair
- unchanged objects skipped
- cursor advancement for recent mode
- cursor advancement for full-history mode
- same-repo handoff when recent repair is incomplete and full-history mode is enabled

### Production-Shaped Verification

- verify recent repair completes within its page budget
- verify full-history repair still advances after restart
- verify stale sample PRs flip on both pull and issue endpoints
- verify status reflects the correct active phase
- verify metrics move as expected during a live pass

## Non-Goals

- Do not turn repair back into an unbounded full crawler.
- Do not bundle comments, reviews, or search indexing into the repair path.
- Do not change the GitHub-compatible response shape for PR or issue reads.

## Success Criteria

- repair logic is visibly split into scan, detect, fetch, and apply stages
- recent and full-history repair share one engine
- scheduling order is obvious from one policy layer
- status is less noisy and easier to interpret
- metrics make it possible to tell whether the system is scanning, detecting, repairing, or stalling
- production behavior stays correct for `openclaw/openclaw` while becoming easier to maintain
