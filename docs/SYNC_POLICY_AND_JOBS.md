# Sync Policy And Jobs

This document describes the intended production sync model for `ghreplica`.

The key rule is:

- webhooks should project the events they receive
- full backfills should be explicit
- repo-wide refresh should not be the default reaction to one webhook

## Default Sync Policy

Every tracked repository should have an explicit sync policy.

Recommended fields:

- `full_name`
- `enabled`
- `sync_mode`
- `webhook_projection_enabled`
- `allow_manual_backfill`
- `allow_targeted_repair`
- `issues_completeness`
- `pulls_completeness`
- `comments_completeness`
- `reviews_completeness`

Recommended sync modes:

- `webhook_only`
  - default for active or large repositories
  - only data observed through webhook events is mirrored
- `webhook_plus_backfill`
  - webhook-driven updates plus explicit backfill jobs
  - good for smaller repos that need broader local completeness
- `manual_only`
  - no automatic webhook projection
  - useful for controlled repair or offline imports

Recommended default:

- new repos should start as `webhook_only`

## Completeness

`ghreplica` should preserve GitHub's data model while being honest about local completeness.

This means:

- canonical objects should stay GitHub-shaped
- sync completeness should be tracked separately
- partial mirrors should not invent non-GitHub object shapes to compensate

Suggested completeness states:

- `empty`
- `sparse`
- `backfilling`
- `backfilled`
- `repairing`

Examples:

- `openclaw/openclaw` pulls can be `sparse` if only webhook-observed PRs exist locally
- `dutifuldev/ghreplica` pulls can be `backfilled` after an explicit full sync

## Job Model

Avoid one generic `refresh repo` job type for everything.

Prefer typed jobs:

- `apply_webhook_delivery`
- `repair_repository`
- `repair_issue`
- `repair_pull_request`
- `repair_issue_comments`
- `repair_pull_request_reviews`
- `repair_pull_request_review_comments`
- `backfill_issues_page`
- `backfill_pulls_page`

Why:

- the job type tells operators what is happening
- retries and timeouts can be tailored to the scope
- a failed PR repair should not block the whole repo

## Webhook Flow

The preferred webhook flow is:

1. receive webhook
2. validate signature
3. persist raw delivery
4. acknowledge quickly
5. apply an event-specific projector
6. mark the delivery applied

Event projectors should:

- use the payload first
- be idempotent
- avoid whole-repo crawls
- schedule targeted repair only when the payload is insufficient

## Repair And Backfill

Repairs should be narrow and explicit.

Good examples:

- refresh one pull request after an edge-case event
- refresh one issue after an out-of-order edit
- backfill one page of PRs

Bad example:

- full bootstrap because one `pull_request` webhook arrived

## Worker Reliability

Jobs should be lease-based.

Recommended fields:

- `status`
- `attempts`
- `max_attempts`
- `lease_expires_at`
- `started_at`
- `finished_at`
- `last_error`

The system should reclaim stale leased jobs automatically.

This prevents a repo from being blocked forever by one abandoned `processing` row.

## Operational Rule

Readiness should reflect the current serving and ingestion health, not historical optional backfill failures.

Recommended split:

- request path health
- database health
- webhook ingestion health
- backfill and repair health

Backfill failures can degrade an operator dashboard without making the whole service look down.
