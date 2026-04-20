---
title: GitHub Read Path Index Plan
date: 2026-04-20
status: proposed
---

# GitHub Read Path Index Plan

## Purpose

This document records the next production fixes for slow GitHub-native read paths in `ghreplica`.

The current hot read handlers already use thinner `raw_json` queries.
The remaining bottlenecks are now mostly in the database access path and, for one downstream caller, the network path used to reach `ghreplica`.

## What We Measured

Production instrumentation showed three important facts.

First, the repository lookup query is using a sequential scan:

```sql
SELECT id
FROM repositories
WHERE owner_login = $1 AND name = $2
LIMIT 1;
```

That query is on the hot path for many GitHub-native endpoints.

Second, the issue list path is still slow in the database even after the HTTP handler cleanup.
The plan is a sequential scan on `issues` plus a sort on `github_created_at`.

Third, the pull list path is less bad, but still uses the same basic pattern:
a sequential scan on `pull_requests` plus a sort on `github_created_at`.

## Main Fixes

Add the following indexes:

- `repositories(owner_login, name)`
- `issues(repository_id, github_created_at DESC)`
- `issues(repository_id, state, github_created_at DESC)`
- `pull_requests(repository_id, github_created_at DESC)`
- `pull_requests(repository_id, state, github_created_at DESC)`

These match the actual GitHub-native read patterns:

- repo lookup by owner and repo name
- list latest issues for one repo
- list latest issues for one repo filtered by state
- list latest pulls for one repo
- list latest pulls for one repo filtered by state

## Things Not To Do

Do not include `raw_json` in these indexes.

That would make the indexes much larger and more expensive to maintain.
The goal is to help the database find the right rows and ordering quickly, not to build giant covering indexes over the stored payload.

## Table Health

Production measurements also showed that the `issues` table has meaningful dead-row churn.

That means index fixes alone are not the whole story.
We should also look at autovacuum behavior and table health after adding the indexes.

This should be treated as a second step, not as a substitute for the missing indexes.

## Downstream Network Path

`prtags` is still calling the public `ghreplica` URL.

That path is more jittery than calling `ghreplica` over the internal Docker or VM network.
It is not a database index issue, but it is still an obvious end-to-end latency problem for `prtags`.

After the database indexes, the next operational improvement should be:

- point `prtags` at an internal `ghreplica` base URL when both services run on the same host or private network

## Order

Implement in this order:

1. add the repository lookup index
2. add the issue list indexes
3. add the pull list indexes
4. remeasure the same production queries and HTTP paths
5. inspect autovacuum and dead-row behavior on `issues`
6. move `prtags` off the public `ghreplica` URL when possible

## Success Criteria

This work is successful when:

- repo lookup no longer uses a sequential scan
- issue list no longer uses a full table scan plus sort for the common repo-scoped path
- pull list no longer uses a full table scan plus sort for the common repo-scoped path
- public GitHub-native list endpoints are materially faster and more stable
- `prtags group get` becomes less sensitive to `ghreplica` read-path spikes
