# Supported Endpoints

`ghreplica` does not claim full GitHub parity. This document is the current supported subset.

## Operational Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /webhooks/github`

## Current API Namespacing

`ghreplica` is moving toward a versioned split between:

- `/v1/github/...` for GitHub-compatible mirrored resources
- `/v1/changes/...` for normalized Git-backed change data
- `/v1/search/...` for overlap and related-change queries

The currently implemented read endpoints below are still served on the legacy unversioned `/repos/...` surface.

## Current Repository Endpoints

- `GET /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/_ghreplica`

## Current Issue Endpoints

- `GET /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{number}`
- `GET /repos/{owner}/{repo}/issues/{number}/comments`

## Current Pull Request Endpoints

- `GET /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{number}`
- `GET /repos/{owner}/{repo}/pulls/{number}/reviews`
- `GET /repos/{owner}/{repo}/pulls/{number}/comments`

## Notes

- compatibility is strongest for the repository, issue, and pull endpoints listed in [Compatibility Strategy](./COMPATIBILITY_STRATEGY.md)
- comments and reviews are mirrored and served, but do not yet have the same breadth of contract coverage as the core read endpoints
- `GET /repos/{owner}/{repo}/_ghreplica` is intentionally `ghreplica`-specific and exposes mirror policy, completeness, and local counts
- unsupported endpoints should be treated as out of scope until explicitly added here
- the long-term path structure for new work should prefer `/v1/github/...`, `/v1/changes/...`, and `/v1/search/...`
