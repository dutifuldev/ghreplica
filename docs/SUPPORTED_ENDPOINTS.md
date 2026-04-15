# Supported Endpoints

`ghreplica` does not claim full GitHub parity. This document is the current supported subset.

## Operational Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /webhooks/github`

## Repository Endpoints

- `GET /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/_ghreplica`

## Issue Endpoints

- `GET /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{number}`
- `GET /repos/{owner}/{repo}/issues/{number}/comments`

## Pull Request Endpoints

- `GET /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{number}`
- `GET /repos/{owner}/{repo}/pulls/{number}/reviews`
- `GET /repos/{owner}/{repo}/pulls/{number}/comments`

## Notes

- compatibility is strongest for the repository, issue, and pull endpoints listed in [Compatibility Strategy](./COMPATIBILITY_STRATEGY.md)
- comments and reviews are mirrored and served, but do not yet have the same breadth of contract coverage as the core read endpoints
- `GET /repos/{owner}/{repo}/_ghreplica` is intentionally `ghreplica`-specific and exposes mirror policy, completeness, and local counts
- unsupported endpoints should be treated as out of scope until explicitly added here
