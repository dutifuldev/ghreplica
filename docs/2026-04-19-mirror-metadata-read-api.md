---
title: Mirror Metadata Read API
date: 2026-04-19
status: proposed
---

# Mirror Metadata Read API

## Goal

Expose read-only `ghreplica` metadata about mirrored repositories without mixing that metadata into the GitHub-shaped API.

This API is for discovery and status.

It is not for changing sync behavior.

## Design Rules

- Keep GitHub-native resources under `/v1/github/...`.
- Keep `ghreplica` mirror metadata under `/v1/mirror/...`.
- Keep these endpoints read-only.
- Expose stable mirror facts separately from live sync status.
- Do not leak raw internal scheduler details unless they are intentionally part of the product contract.
- Do not put expensive live aggregate counts on the stable metadata path unless they are backed by durable cached state.

## Initial Endpoints

### `GET /v1/mirror/repos`

Return a list of mirrored repositories.

This endpoint is for repo discovery.

This endpoint should use page-based pagination:

- `page`
- `per_page`

The defaults should match the existing GitHub-shaped list endpoints:

- `page=1`
- `per_page=30`
- clamp `per_page` to `100`

The response should include a standard `Link` header for navigation.

The response should include stable facts only, for example:

- `github_id`
- `node_id`
- `owner`
- `name`
- `full_name`
- `fork`
- `enabled`
- `sync_mode`
- grouped completeness fields
- grouped metadata timestamps

This endpoint should not include noisy live scheduler fields like active leases.

The list response should be ordered predictably.

The initial default should be `full_name ASC`.

### `GET /v1/mirror/repos/:owner/:repo`

Return stable mirror metadata for one repository.

This is the per-repo discovery endpoint.

It should include the same stable fields as the list view, with room for a little more detail, for example:

- canonical GitHub identity
- current repo name
- whether the repo is a fork
- whether the repo is enabled here
- sync mode
- completeness state
- metadata timestamps

### `GET /v1/mirror/repos/:owner/:repo/status`

Return live sync status for one repository.

This endpoint is for current mirror progress, not durable facts.

It should include fields like:

- grouped sync state
- grouped PR change progress counts
- grouped live activity flags
- grouped status timestamps

This should stay at the level of useful product status.

It should not expose every internal lease heartbeat unless we later decide users truly need that.

## Why This Split

The split between metadata and status keeps the API easier to understand.

`/v1/mirror/repos` and `/v1/mirror/repos/:owner/:repo` answer:

"Is this repo mirrored here, and what is its stable mirror state?"

`/v1/mirror/repos/:owner/:repo/status` answers:

"What is sync doing right now?"

That separation is cleaner than returning one large mixed object with both durable facts and volatile worker state.

## Response Shape

### Stable Metadata

`GET /v1/mirror/repos`

`GET /v1/mirror/repos/:owner/:repo`

These endpoints should return one stable mirror object per repository:

```json
{
  "owner": "openclaw",
  "name": "openclaw",
  "full_name": "openclaw/openclaw",
  "github_id": 1213778837,
  "node_id": "R_kgDO...",
  "fork": false,
  "enabled": true,
  "sync_mode": "webhook_only",
  "completeness": {
    "issues": "sparse",
    "pulls": "sparse",
    "comments": "sparse",
    "reviews": "sparse"
  },
  "timestamps": {
    "last_webhook_at": "2026-04-19T00:00:00Z",
    "last_bootstrap_at": null,
    "last_crawl_at": null
  }
}
```

This shape should stay stable over time.

It should not expose internal row IDs, separate table presence booleans, or scheduler generation fields.

### Live Status

`GET /v1/mirror/repos/:owner/:repo/status`

This endpoint should return one live status object:

```json
{
  "repository": {
    "owner": "openclaw",
    "name": "openclaw",
    "full_name": "openclaw/openclaw"
  },
  "sync": {
    "state": "running",
    "last_error": null
  },
  "pull_request_changes": {
    "total": 6724,
    "current": 5264,
    "stale": 4,
    "missing": 1462
  },
  "activity": {
    "inventory_scan_running": false,
    "backfill_running": true,
    "targeted_refresh_pending": true,
    "targeted_refresh_running": false,
    "inventory_refresh_requested": true
  },
  "timestamps": {
    "last_inventory_scan_started_at": "2026-04-19T00:00:00Z",
    "last_inventory_scan_finished_at": "2026-04-19T00:03:00Z",
    "last_backfill_started_at": "2026-04-19T00:04:00Z",
    "last_backfill_finished_at": null
  }
}
```

This shape should describe the current mirror state in product terms.

It should not expose raw lease rows, generation IDs, cursors, or internal scheduler bookkeeping.

## What Not To Do

- Do not put this under `/v1/github/...`.
- Do not invent a custom GitHub replacement representation for native GitHub resources.
- Do not make these endpoints mutate mirror state.
- Do not expose raw scheduler internals by default just because they exist in the database.

## Future Expansion

If users later need more operator-style detail, add a separate internal or operator endpoint then.

Do not start with `/v1/admin/...` unless we have a concrete user for it.

The first version should stay small:

- repo discovery
- per-repo metadata
- per-repo live status

## Pagination Rule

`GET /v1/mirror/repos` should follow the same pagination style already used by the repo’s GitHub-shaped list endpoints.

That means:

- use `page` and `per_page`
- not `limit`
- return a `Link` header

This keeps list semantics consistent across the product.
