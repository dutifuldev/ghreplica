# ghreplica

`ghreplica` is a backend-agnostic GitHub mirror written in Go.

It ingests GitHub repository data through webhooks and crawls, reconstructs that data into a local model, and serves a GitHub-compatible API on top. The project is intended to support tooling such as triage systems, reviewer agents, and offline analysis without forcing each tool to build its own partial GitHub sync layer.

## Goals

- replicate GitHub repo data with high fidelity
- stay agnostic to the underlying storage backend
- expose a GitHub-compatible read API
- support both operational workloads and async analytics exports

## Approach

The system is built around:

- webhook and crawl ingestion
- a canonical internal model of GitHub objects
- storage adapters for different backends
- API-compatible read handlers built with Echo

## Docs

- [Architecture](docs/architecture.md)
- [GitHub API Surface Research](docs/github-api-surface.md)
- [Data Model For PR Triage](docs/data-model.md)

## Status

Early design phase.
