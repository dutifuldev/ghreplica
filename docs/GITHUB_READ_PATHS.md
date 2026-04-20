# GitHub Read Paths

## Purpose

This document defines the production read-path rule for GitHub-native endpoints in `ghreplica`.

The goal is simple:

- keep the public response GitHub-shaped
- keep the read path cheap
- avoid rebuilding data we already store in canonical GitHub JSON form

## Core Rule

For GitHub-native resources, if `ghreplica` already stores the canonical GitHub JSON for an object, the read path should:

1. resolve the minimum identity needed to find the row
2. read the stored `raw_json`
3. return that JSON directly

That is the default design.

## Why

`ghreplica` stores GitHub objects in two forms:

- indexed relational columns for filtering, sorting, joins, and sync logic
- the original GitHub-shaped JSON payload in `raw_json`

That split is intentional.

The relational columns exist so the mirror can query and maintain the data efficiently.
The stored `raw_json` exists so the API can return faithful GitHub-shaped objects without manually rebuilding every nested field.

## Read-Path Rules

Hot GitHub-native read endpoints should follow these rules:

- use the cheapest repository lookup that works, usually `repository_id`
- do not preload related models unless the handler actually reads them
- do not fetch full ORM rows when only `raw_json` is needed
- do not decode stored JSON and re-encode it if the endpoint can return the stored bytes directly
- keep the SQL narrow and explicit

For list and batch endpoints, the same rule still applies:

- fetch only the page or object set needed
- select only the identity fields needed for ordering or mapping plus `raw_json`
- return the stored JSON payloads directly

## What This Means In Practice

Good hot-path handler shape:

1. resolve `repository_id`
2. query only the columns needed for the endpoint, often just `raw_json`
3. return the stored JSON bytes directly

Bad hot-path handler shape:

1. load a full repository row with unrelated preloads
2. load full issue or pull rows with unrelated associations
3. decode `raw_json` into generic Go values
4. encode the same object back to JSON for the response

That pattern adds cost without improving fidelity.

## Scope

This rule applies first to GitHub-native endpoints such as:

- single repository reads
- single issue reads
- single pull request reads
- issue and pull request list endpoints
- batch GitHub-object read endpoints

It does not require every endpoint in the service to avoid decoding JSON.

Derived `ghreplica` surfaces such as `/v1/changes/...`, `/v1/search/...`, and mirror metadata endpoints may need their own explicit response-building logic because they are not serving canonical GitHub-native resources.

## GORM Rule

`gorm` is still fine for this project.

The problem is not `gorm` itself.
The problem is using heavyweight ORM patterns on hot mirror read paths.

For GitHub-native hot reads, use `gorm` in a thin way:

- narrow `Select(...)`
- direct `Row`, `Rows`, or `Raw(...).Scan(...)` when useful
- small query helpers that return IDs and stored JSON

Do not use full object graph loading on the hot compatibility surface unless the endpoint truly needs it.

## Design Standard

For GitHub-native compatibility endpoints, the preferred shape is:

- cheap lookup
- narrow query
- direct return of stored GitHub JSON

That is the default production standard for `ghreplica` read paths.
