---
title: Webhook Delivery Cleanup Implementation Plan
date: 2026-04-21
status: proposed
supersedes: []
---

# Problem

`webhook_deliveries` is growing without bound in production.

The main reasons are straightforward:

- every webhook stores full `headers_json` and full `payload_json`
- processed deliveries are only cleaned up when `WEBHOOK_DELIVERY_RETENTION` is set
- production currently leaves `WEBHOOK_DELIVERY_RETENTION` blank

So old webhook rows are never cleaned up, and the table keeps accumulating large JSON blobs forever.

# Decision

Use the existing cleanup worker and make processed webhook payload retention `6h`.

The intended behavior is:

- keep full webhook payloads for recent debugging and replay
- once a delivery has been processed successfully and is older than `6h`, delete the row
- do not auto-delete unprocessed or failed rows just because they are old

This is the smallest change that materially reduces storage growth and creates a real cap on processed-delivery retention without inventing a new subsystem.

# Scope

This plan is only for payload cleanup of `webhook_deliveries`.

It does not:

- redesign dedupe
- change webhook processing semantics
- change GitHub-compatible read APIs

# Important Constraint

This plan creates a hard retention cap for processed webhook rows, but not for unprocessed or failed rows.

Why:

- processed deliveries older than `6h` will no longer accumulate forever
- unprocessed or failed rows will still remain until they are handled or cleaned up explicitly
- if webhook processing gets stuck for a long time, that backlog can still grow

So this plan is the right immediate fix for the current storage blow-up, but it still assumes webhook processing stays healthy enough that unprocessed backlog does not become the dominant storage problem.

# Implementation Plan

## 1. Turn On Cleanup In Production

Set the production baseline to:

- `WEBHOOK_DELIVERY_RETENTION=6h`
- keep `WEBHOOK_DELIVERY_CLEANUP_INTERVAL=15m`
- start with a larger catch-up batch size than the current default if backlog is large

This should use the existing cleanup worker rather than a new cleanup service.

## 2. Use The Existing In-Place Compaction Path

Keep the existing cleanup worker, but change the cleanup action from compaction to deletion.

Cleanup semantics should be:

- only delete rows where `processed_at IS NOT NULL`
- only delete rows older than the retention cutoff
- never auto-delete rows where `processed_at IS NULL`
- never auto-delete rows just because they are old if they are still failed or pending operator attention

## 3. Add A Backlog Catch-Up Plan

The existing default batch size of `500` is too small for a large accumulated backlog.

For example, with a large historical backlog, `500` rows every `15m` can take weeks to catch up.

So rollout should include a catch-up phase:

- temporarily raise `WEBHOOK_DELIVERY_CLEANUP_BATCH_SIZE` in production
- keep the cleanup interval short enough to make forward progress
- watch app and DB load during catch-up
- lower the batch size later if needed once backlog is under control

This should still use the existing worker and config knobs.

## 4. Measure The Right Thing

Success is not just “cleanup ran once.”

Track:

- number of processed rows older than `6h`
- number of rows deleted per pass
- total `webhook_deliveries` table size
- cleanup query duration
- readiness and DB pressure during cleanup

## 5. Handle Existing Bloat Honestly

Deleting rows will stop future growth from processed backlog, but PostgreSQL may not immediately return disk space to the OS.

So there are two separate outcomes:

- near-term: storage growth rate drops because old processed rows are deleted
- later: physical table size may still stay large until table or TOAST storage is rewritten

If we need disk usage to come down materially after catch-up, plan a one-time storage reclamation step.

Preferred choice:

- use `pg_repack` if operationally acceptable

Fallback:

- `VACUUM FULL` during a maintenance window

This should be treated as a separate operational step, not part of ordinary cleanup.

# Rollout

## Phase 1. Enable `6h` Retention

- set `WEBHOOK_DELIVERY_RETENTION=6h`
- raise cleanup batch size for backlog catch-up
- deploy

Success means:

- cleanup worker is enabled
- old processed rows older than `6h` start being deleted
- readiness remains acceptable during cleanup

## Phase 2. Catch Up Historical Backlog

- monitor compaction progress
- keep batch size high enough to reduce backlog in days, not weeks
- tune down only if cleanup starts harming DB or request health

Success means:

- processed backlog older than `6h` drops steadily
- table-growth trend flattens materially

## Phase 3. Reclaim Physical Space If Needed

- after backlog compaction, inspect actual table and TOAST sizes
- if disk footprint is still unacceptably high, run one-time storage reclamation

Success means:

- future growth is much slower
- disk footprint is acceptable for the deployment

# Validation

The implementation should be considered successful when:

- processed deliveries older than `6h` are deleted automatically
- recent webhook debugging still works for the last `6h`
- unprocessed or failed deliveries are not deleted by age alone
- the database no longer accumulates processed webhook rows forever

# Future Follow-Up

If we later want a true long-term storage cap, we should separate that from this change.

That future work would likely mean one of:

- moving dedupe to a separate slimmer table if we want longer delivery-id memory without keeping full rows
- adding explicit lifecycle rules for failed or stuck deliveries
- archiving payload history outside the primary database

That is separate from deleting processed deliveries after `6h`.
