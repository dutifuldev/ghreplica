---
title: Cloud SQL Connector And Database Dialer
date: 2026-04-18
status: proposed
summary: Keep ghreplica cloud-agnostic by isolating provider-specific database connection code to a small startup dialer layer, with Cloud SQL Go connector as the GCP production path.
---

# Cloud SQL Connector And Database Dialer

## Purpose

`ghreplica` should stay a Postgres application, not a GCP-specific application.

The long-term rule is:

- the product depends on Postgres
- cloud-specific logic lives only in the database connection opener

This lets `ghreplica` run on:

- local Docker Postgres
- Postgres on a VM
- Cloud SQL on GCP
- RDS or another managed Postgres service later

without spreading provider-specific behavior across the app.

## Problem

The current GCP production deployment still routes database traffic through a `cloudsql-proxy` sidecar.

That creates an extra process and hop in the hot path:

- `ghreplica` container
- `cloudsql-proxy`
- Cloud SQL

The production goal is to remove that sidecar from the request and worker hot path while keeping:

- IAM-based access
- managed TLS
- durable connection pooling
- workload isolation between control and sync traffic

## Core Rule

The application should only know that it needs Postgres connections.

It should not care whether those connections come from:

- a normal TCP host and port
- an in-process Cloud SQL connector
- another provider-specific dialer later

That selection should happen once at startup.

## Target Shape

The clean long-term shape is:

- `ghreplica` owns the database pools
- `ghreplica` selects the dialer implementation at startup
- the rest of the codebase receives ordinary `sql.DB` and GORM handles

The app keeps separate pools by workload class:

- `control` pool
  - API
  - webhook accept path
  - River leader and producer
  - lightweight repo and status reads
- `sync` pool
  - inventory scans
  - backfill
  - heavy indexing

If River still needs its own pool, that remains an application-level pool choice, not a provider-specific concern.

## Recommended GCP Production Path

The preferred long-term GCP path is:

- use the Cloud SQL Go connector in process
- remove `cloudsql-proxy` from the hot path
- keep separate control and sync pools

That gives:

- one less process to operate
- one less local network hop
- IAM and TLS handled in process
- direct ownership of connection reuse by the Go app

## Database Dialer Boundary

The app should have one small infrastructure boundary responsible for opening DB handles.

That boundary should:

- parse config
- choose a dialer mode
- open `sql.DB` handles
- apply pool sizes and lifetimes
- return ready-to-use handles for GORM and other callers

The rest of `ghreplica` should not know which dialer mode is active.

## Dialer Modes

The intended supported modes are:

### Standard Postgres URL

Use a normal Postgres DSN and standard TCP connection behavior.

This is the default portable mode for:

- local development
- Docker Compose
- plain VMs
- AWS RDS
- any normal Postgres deployment

### Cloud SQL Go Connector

Use the in-process Cloud SQL Go connector for GCP production.

This mode should:

- authenticate with ADC or the configured workload identity
- connect using the Cloud SQL instance connection name
- keep the app on ordinary `database/sql` pools
- avoid the `cloudsql-proxy` sidecar for the main application traffic

## What Must Stay Portable

These parts of the product should remain unchanged across dialer modes:

- schema and migrations
- GORM models
- query logic
- River usage
- sync scheduler
- webhook jobs
- API behavior

Only the database opener should vary by environment.

## What Must Not Happen

Do not:

- scatter GCP-specific code across request handlers or workers
- make sync logic aware of Cloud SQL
- introduce provider conditionals throughout the app
- create fake database abstraction layers over SQL behavior itself

The correct abstraction level is the connection opener, not the whole data layer.

## Configuration Direction

The config model should separate:

- portable Postgres settings
- dialer mode selection
- provider-specific dialer inputs

Examples of the shape:

- `DB_DIALER=postgres|cloudsql`
- `DATABASE_URL=...` for standard Postgres mode
- `CLOUDSQL_INSTANCE_CONNECTION_NAME=project:region:instance` for Cloud SQL mode

Pool sizing remains provider-neutral:

- `DB_CONTROL_MAX_OPEN_CONNS`
- `DB_CONTROL_MAX_IDLE_CONNS`
- `DB_SYNC_MAX_OPEN_CONNS`
- `DB_SYNC_MAX_IDLE_CONNS`

If River keeps a separate pool:

- `DB_QUEUE_MAX_OPEN_CONNS`
- `DB_QUEUE_MAX_IDLE_CONNS`

## Cutover Direction

The GCP cutover should be:

1. add the database dialer boundary
2. support standard Postgres and Cloud SQL connector modes
3. switch GCP production to the Cloud SQL connector
4. remove `cloudsql-proxy` from the hot path
5. verify that:
   - health and readiness remain stable
   - River control-plane timeouts disappear
   - inventory scans no longer stall unrelated control traffic

## Long-Term Outcome

If this is implemented cleanly, `ghreplica` becomes:

- cloud-agnostic at the product layer
- GCP-optimized in the production deployment
- easier to move to AWS or plain Postgres later

The important final rule is:

- `ghreplica` is a Postgres application
- cloud-specific database connectivity is a small startup concern, not an app-wide architecture choice
