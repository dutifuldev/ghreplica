---
title: Background Job Webhook Processing Plan
date: 2026-04-18
status: proposed
---

# 2026-04-18 Background Job Webhook Processing Plan

This document describes the intended production cutover for GitHub webhook ingestion in `ghreplica`.

It is a separate concern from:

- three-lane repo change sync scheduling
- bounded commit detail indexing

Those plans decide how repo sync work is prioritized and what counts as a successful pull request refresh.

This plan decides how webhook-originated work should enter the system without doing too much synchronous processing on the request path.

## Problem

Today the GitHub webhook handler accepts the request and then performs too much real work inline before returning `202`.

That inline work currently includes things like:

- `webhook_deliveries` writes
- tracked repository lookup and projection-state updates
- repository lookup
- webhook projection into canonical GitHub-shaped tables
- search or refresh side effects
- targeted pull refresh enqueueing
- inventory refresh marking

All of that work currently runs under the HTTP request context.

When the client disconnects or times out, that context is canceled, and the in-flight database work is canceled too.

That produces the `context canceled` churn seen in production and wastes throughput on hot repos.

## Goal

The production goal should be:

- accept webhooks quickly
- persist them durably
- enqueue background processing transactionally
- return `202` without doing heavy projection work inline
- keep the current mirrored data model and downstream functionality unchanged

In plain language:

- the request path should only accept and queue
- the background worker should do the heavy work

## Core Design

Use a durable background job runner for webhook-driven projection work.

Keep the existing repo-specific sync logic custom.

The clean split is:

- River owns durable job execution, retries, queueing, and worker concurrency
- `ghreplica` still owns repo sync policy, inventory generations, targeted refresh rules, leases, and freshness semantics

This avoids reinventing queue plumbing without trying to force the entire sync engine into a generic framework abstraction.

## Recommended Runner

River is the right fit for this cutover because:

- it is PostgreSQL-backed, matching the current stack
- it supports transactional enqueueing
- it supports retries, uniqueness, and queues
- it does not require Redis or another new service

The most important property for this change is transactional enqueueing:

- if the webhook delivery row is committed
- the processing job is committed too

That is the right durability boundary for `ghreplica`.

Sources:

- https://riverqueue.com/
- https://github.com/riverqueue/river

## Non-Goal

This plan is not a replatform of the whole sync engine.

It should not move:

- three-lane change sync scheduling
- repo inventory generation logic
- targeted versus backfill policy
- GitHub-compatible read APIs

Those remain custom.

The scope here is specifically:

- webhook request handling
- webhook-originated projection work
- webhook-originated refresh and status side effects

## Target Request Path

The webhook HTTP handler should do only the following:

1. read the body
2. validate the GitHub signature
3. extract the event and delivery headers
4. open a transaction
5. insert the `webhook_deliveries` row if it is new
6. enqueue one River job for that delivery inside the same transaction
7. commit
8. return `202`

That request path should not do:

- repository projection
- issue or pull request projection
- tracked repository state updates
- searchindex side effects
- targeted refresh insertion
- inventory refresh decisions beyond what can be deferred to the job

The request path should become small, fast, and durable.

## Target Background Job

The first River job type should be a single delivery-processor job.

Recommended shape:

- kind: `github_webhook_process`
- args:
  - `delivery_id`

The job should be unique by `delivery_id`.

That ensures one GitHub delivery is only processed once in the projection path even if the request is replayed or retried.

## Initial Production Defaults

The first production cut should keep the setup small and explicit.

Recommended starting values:

- queue name: `webhook_projection`
- job kind: `github_webhook_process`
- queue concurrency: `3`
- job timeout: `30s`
- max attempts: `8`
- retry backoff: exponential with jitter, capped at `30m`
- uniqueness key: `delivery_id`
- uniqueness window: `7d`

These values match the current shape of production load reasonably well:

- current webhook-side cancellation churn tends to appear after roughly `4s` to `10s`
- webhook projection jobs should perform bounded projection work, not deep Git indexing
- the queue should be able to absorb hot-repo bursts without creating a large number of competing workers immediately

The intended first-cut meaning is:

- give each delivery enough time to finish normal projection work
- retry transient failures a handful of times
- avoid infinite churn on the same failing delivery
- dedupe replayed GitHub deliveries safely

## Database Pool Baseline

This cutover should be paired with split serve-time database pools.

Recommended starting values:

- control pool:
  - max open connections: `6`
  - max idle connections: `3`
- sync pool:
  - max open connections: `6`
  - max idle connections: `2`

That keeps River and the HTTP request path off the same pool as inventory scans, backfill, and heavy indexing.

These defaults are now exposed through the normal runtime config surface:

- `DB_CONTROL_MAX_OPEN_CONNS`
- `DB_CONTROL_MAX_IDLE_CONNS`
- `DB_SYNC_MAX_OPEN_CONNS`
- `DB_SYNC_MAX_IDLE_CONNS`
- `WEBHOOK_JOB_QUEUE_CONCURRENCY`
- `WEBHOOK_JOB_TIMEOUT`
- `WEBHOOK_JOB_MAX_ATTEMPTS`

## Processing Flow

The River worker should:

1. load the stored webhook delivery row
2. decode the stored payload
3. decide whether the event is supported or intentionally ignored
4. perform the current projection logic for supported events
5. update tracked repository projection state
6. enqueue targeted pull refresh work where needed
7. mark inventory refresh needed where needed
8. mark the delivery as processed or failed

The important change is not the business logic.

The important change is that the business logic no longer runs under the request context.

## Function Boundary Refactor

The clean implementation cut should be:

- `AcceptWebhook(...)`
  - lightweight
  - transactionally insert delivery + River job
- `ProcessWebhookDelivery(...)`
  - heavy
  - perform today’s projection work

That lets the current synchronous `HandleWebhook(...)` logic be split cleanly instead of rewritten all at once.

## Expected Behavior After Cutover

After the cutover:

- the public webhook endpoint should behave the same from GitHub’s point of view
- supported events should still populate the same canonical GitHub-shaped tables
- targeted refreshes should still be enqueued the same way
- inventory refresh markers should still be set from the same event rules
- `202 accepted` should still be returned to GitHub

What changes is:

- the heavy work happens off-request
- retries are handled by River instead of the webhook client
- request cancellation no longer kills projection halfway through

## Retry Rules

River retries should be used for actual transient failures such as:

- temporary database errors
- temporary GitHub client failures when a projection path requires them
- lease or row-lock races that should be retried

Deterministic no-op outcomes should not churn:

- unsupported events
- already-processed duplicate deliveries
- intentionally ignored events

Malformed payloads should be recorded as failed processing, not retried forever.

## Event Filtering

Noisy events should be cheap.

The worker should be able to look at a stored delivery and conclude quickly:

- unsupported, ignore
- supported but projection-disabled for this repo, update minimal state only
- supported and projection-enabled, do full processing

That means low-value traffic like `check_run` should stop causing expensive synchronous request-path work.

## Data Model

This cutover should reuse existing durable tables where possible.

At minimum:

- keep `webhook_deliveries`
- add River’s job tables

The `webhook_deliveries` row should remain the canonical record of what GitHub sent.

River jobs are the execution mechanism, not the source-of-truth payload store.

## Status and Operations

Operationally, this change should make it easier to answer:

- are webhook deliveries arriving
- are they being processed
- are jobs retrying
- is queue depth growing
- are specific deliveries stuck or failing repeatedly

The main operator-facing improvement is that webhook acceptance and webhook processing become separately observable.

That is much more honest than the current model where the request is accepted only after heavy inline work already happened or got canceled.

## Cutover Strategy

This should be a direct cutover, not a dual-path design.

The clean rollout is:

1. add River
2. add the delivery-processing job
3. split webhook acceptance from webhook processing
4. switch the HTTP handler to transactionally insert delivery + job and return `202`
5. remove the old synchronous projection path from the request handler

The downstream projection behavior should stay the same.

## What Stays Custom

Even after River adoption, these parts should stay in `ghreplica`:

- repo change sync scheduler
- targeted refresh burst policy
- inventory generation rules
- backfill rules
- freshness semantics
- GitHub-compatible read surface

River should run jobs.

`ghreplica` should still decide what repo sync work means.

## Testing

Coverage should include:

- duplicate webhook delivery only enqueues one unique processing job
- accepted webhook returns `202` without running heavy projection work inline
- supported webhook still projects the same canonical data as before
- unsupported webhook is stored and processed cheaply
- transient worker failure causes retry
- malformed or deterministically bad delivery is marked failed without infinite churn
- targeted refresh and inventory refresh side effects still happen for the same webhook cases as before

Production verification should confirm:

- webhook request latencies drop materially
- `context canceled` request-path churn disappears or drops sharply
- queue depth remains bounded under hot-repo traffic
- canonical repo, issue, pull request, comment, and review projection behavior remains unchanged

## Long-Term Shape

This is the intended long-term production split:

- River for generic async webhook job execution
- custom `ghreplica` logic for repo sync policy

That is the right abstraction boundary.

It removes the queue and retry plumbing we do not need to own, while preserving the domain-specific sync behavior that still belongs in this project.
