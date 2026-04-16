---
title: Batch Object Read Extension Plan
date: 2026-04-17
status: implemented
---

# 2026-04-17 Batch Object Read Extension Plan

This document describes how `ghreplica` should expose a production-ready batch object read endpoint for downstream tools such as `PRtags`.

This plan has now been implemented on the live `ghreplica` service as:

- `POST /v1/github-ext/repos/:owner/:repo/objects/batch`

The core need is simple: downstream systems often already know a small set of GitHub object references and need to resolve them efficiently to the stored GitHub-shaped payloads. Doing that through one request per object is wasteful. At the same time, `ghreplica` should not pretend GitHub already has a matching batch API when it does not.

## Problem

Today, `ghreplica` has:

- GitHub-shaped single-object endpoints
- GitHub-shaped list endpoints
- search endpoints that return multiple matches

What it does not have is an explicit way to say:

- here are these exact PRs and issues
- resolve them from the mirror
- give me the stored GitHub-shaped objects back in one request

That gap is especially visible for `PRtags`, where group membership stores references such as:

- repository identity
- object type
- object number

To display a group cleanly, `PRtags` should be able to resolve those referenced objects without N separate calls.

## Goal

The production goal should be:

- one explicit batch read endpoint
- local mirror reads only
- GitHub-shaped stored objects returned as-is
- partial misses handled cleanly
- clear separation from the GitHub-compatible surface

## Core Rule

This should be an extension endpoint, not a fake GitHub-compatible endpoint.

That means:

- keep `/v1/github/...` for GitHub-shaped routes that already correspond to GitHub concepts
- add a separate extension surface for batch resolution
- keep the returned object payloads themselves GitHub-shaped

The important distinction is:

- endpoint shape is `ghreplica`-specific
- object shape remains GitHub-shaped

## Recommended Route

Use:

- `POST /v1/github-ext/repos/:owner/:repo/objects/batch`

This is the cleanest production shape because:

- it is repo-scoped like the canonical GitHub routes
- it is explicit that this is not part of the mirrored GitHub API
- it gives downstream tooling one obvious place for efficient object resolution

## Request Shape

The request should be:

```json
{
  "objects": [
    { "type": "pull_request", "number": 24 },
    { "type": "issue", "number": 11 }
  ]
}
```

Initial supported types should be:

- `pull_request`
- `issue`

That is enough for the first real downstream use case.

Later, if needed, we can add support for:

- `issue_comment`
- `pull_request_review`
- `pull_request_review_comment`

But the first version should stay narrow.

## Response Shape

The response should preserve request order and return one result per requested object.

Recommended shape:

```json
{
  "results": [
    {
      "type": "pull_request",
      "number": 24,
      "found": true,
      "object": {
        "...": "stored GitHub-shaped PR payload"
      }
    },
    {
      "type": "issue",
      "number": 11,
      "found": false
    }
  ]
}
```

The production rules should be:

- keep one result per input object
- preserve input order
- include `found: false` for misses
- do not fail the whole request just because some objects are missing

## Why This Is The Right Shape

This design preserves the important boundary:

- canonical `/v1/github/...` routes stay honest
- extension behavior is explicit
- the object payloads are still exactly the stored GitHub-shaped resources

That gives downstream tools the efficiency they need without muddying the compatibility contract.

## Read Path Rule

The batch endpoint must read from the local mirror only.

It must not:

- fetch live from GitHub in the request path
- trigger a refresh implicitly
- invent partially reconstructed objects

The request path should be:

1. resolve the repository route once
2. normalize and validate the requested object refs
3. read matching mirrored rows from local storage
4. decode the stored raw JSON payloads
5. assemble results in the original request order

If an object is missing from the mirror, the response should report:

- `found: false`

not trigger live repair.

That keeps the endpoint predictable and operationally safe.

## Query Strategy

The implementation should:

- deduplicate repeated requested refs internally
- split requests by object type
- run one `IN (...)` query per type
- build a lookup map in memory
- re-expand to the original order when producing the response

For example:

- one query for all requested issues
- one query for all requested PRs

That is much better than one query per object and still keeps the implementation simple.

## Validation And Limits

The endpoint should be strict and bounded.

Recommended first limits:

- maximum `100` requested objects per call
- reject empty `objects`
- reject unsupported `type`
- reject non-positive `number`

Request validation failures should return:

- `400 Bad Request`

Partial misses should still return:

- `200 OK`

with per-item `found: false`.

## Authentication And Visibility

This endpoint should follow the same visibility model as the normal read routes.

That means:

- public mirrored repos remain readable
- if later we add private-repo read controls, the batch endpoint should obey the same rules

There should not be a special auth policy just for batch reads.

## Error Semantics

Use simple response semantics:

- `200` for valid requests, even if some items are missing
- `400` for malformed input
- `404` if the repository itself does not exist in the mirror
- `500` only for real server failures

The endpoint should not collapse “missing object” and “missing repository” into the same outcome.

## Initial Scope

The first production version should only support:

- repo-scoped issue reads
- repo-scoped pull request reads

That covers the real downstream case now:

- `PRtags` group members referencing issues and PRs

Do not expand to every GitHub object type before we have a real caller for them.

## Future Extensions

If this proves useful, the same extension surface can grow to support:

- richer object ref types
- optional field projection
- optional typed response summaries alongside the raw GitHub-shaped object

But the first version should not try to be a general query language.

## Testing

The testing surface should include:

- valid mixed issue and PR batches
- repeated refs in the same request
- partially missing batches
- fully missing batches
- invalid type and invalid number handling
- request-order preservation
- batch-size limit enforcement

Integration tests should verify:

- only one repo lookup happens
- one query per supported object type
- returned `object` payloads match stored raw JSON

## Downstream Usage

The first intended caller is `PRtags`.

The usage pattern should be:

1. `PRtags` reads group membership refs
2. `PRtags` sends those refs to the batch endpoint
3. `ghreplica` returns the mirrored GitHub-shaped objects in one response
4. `PRtags` decorates its group response without doing N separate object fetches

That is the clean layering:

- `ghreplica` remains the mirror
- `PRtags` remains the annotation layer

## Summary

The production-ready solution is:

- add `POST /v1/github-ext/repos/:owner/:repo/objects/batch`
- support `pull_request` and `issue` first
- read from the mirror only
- return stored GitHub-shaped objects
- preserve request order
- report per-item misses cleanly

That is the honest and efficient extension layer that downstream tools need.
