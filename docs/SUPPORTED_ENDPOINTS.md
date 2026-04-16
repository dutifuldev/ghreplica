# Supported Endpoints

`ghreplica` does not claim full GitHub parity. This document is the current supported subset.

## Operational Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /webhooks/github`

## Current API Namespacing

`ghreplica` now serves a versioned split between:

- `/v1/github/...` for GitHub-compatible mirrored resources
- `/v1/github-ext/...` for explicit mirror-backed extensions that do not exist on GitHub itself
- `/v1/changes/...` for normalized Git-backed change data
- `/v1/search/...` for overlap and related-change queries

## GitHub-Compatible Endpoints

- `GET /v1/github/repos/{owner}/{repo}`
- `GET /v1/github/repos/{owner}/{repo}/issues`
- `GET /v1/github/repos/{owner}/{repo}/issues/{number}`
- `GET /v1/github/repos/{owner}/{repo}/issues/{number}/comments`
- `GET /v1/github/repos/{owner}/{repo}/pulls`
- `GET /v1/github/repos/{owner}/{repo}/pulls/{number}`
- `GET /v1/github/repos/{owner}/{repo}/pulls/{number}/reviews`
- `GET /v1/github/repos/{owner}/{repo}/pulls/{number}/comments`

## GitHub Extension Endpoints

- `POST /v1/github-ext/repos/{owner}/{repo}/objects/batch`

## Change Endpoints

- `GET /v1/changes/repos/{owner}/{repo}/mirror-status`
- `GET /v1/changes/repos/{owner}/{repo}/status`
- `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}`
- `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}/status`
- `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}/files`
- `GET /v1/changes/repos/{owner}/{repo}/commits/{sha}`
- `GET /v1/changes/repos/{owner}/{repo}/commits/{sha}/files`
- `GET /v1/changes/repos/{owner}/{repo}/compare/{base}...{head}`

## Search Endpoints

- `GET /v1/search/repos/{owner}/{repo}/pulls/{number}/related`
- `POST /v1/search/repos/{owner}/{repo}/pulls/by-paths`
- `POST /v1/search/repos/{owner}/{repo}/pulls/by-ranges`
- `GET /v1/search/repos/{owner}/{repo}/status`
- `POST /v1/search/repos/{owner}/{repo}/mentions`
- `POST /v1/search/repos/{owner}/{repo}/ast-grep`

Text-search request features:

- `query`
- `mode`
  - `fts`
  - `fuzzy`
  - `regex`
- `scopes`
  - `pull_requests`
  - `issues`
  - `issue_comments`
  - `pull_request_reviews`
  - `pull_request_review_comments`
- `state`
- `author`
- `limit`
- `page`

Text-search response features:

- status response:
  - `repository`
    - `owner`
    - `name`
    - `full_name`
  - `text_index_status`
  - `document_count`
  - `last_indexed_at`
  - `last_source_update_at`
  - `freshness`
  - `coverage`
  - `last_error`

- `resource`
  - `type`
  - `id`
  - `number`
  - `api_url`
  - `html_url`
- `matched_field`
- `excerpt`
- `score`

Structural-search request features:

- exactly one target:
  - `commit_sha`
  - `ref`
  - `pull_request_number`
- `language`
- `rule`
- `paths`
- `changed_files_only`
- `limit`

Structural-search response features:

- `repository`
  - `owner`
  - `name`
  - `full_name`
- `resolved_commit_sha`
- `resolved_ref`
- `matches`
  - `path`
  - `start_line`
  - `start_column`
  - `end_line`
  - `end_column`
  - `text`
  - `meta_variables`
- `truncated`

## CLI Coverage

The `ghr` CLI now covers all three read surfaces:

- `repo`, `issue`, and `pr` map to `/v1/github/...`
- `changes` maps to `/v1/changes/...`
- `search` maps to `/v1/search/...`

See [CLI](./CLI.md) for the command mapping and examples.

## Notes

- compatibility is strongest for the repository, issue, and pull endpoints listed in [Compatibility Strategy](./COMPATIBILITY_STRATEGY.md)
- comments and reviews are mirrored and served, but do not yet have the same breadth of contract coverage as the core read endpoints
- `GET /v1/changes/repos/{owner}/{repo}/mirror-status` is intentionally `ghreplica`-specific and exposes mirror policy, completeness, and local counts
- the versioned path structure for new work is `/v1/github/...`, `/v1/changes/...`, and `/v1/search/...`
- unsupported endpoints should be treated as out of scope until explicitly added here
- text-search endpoints stay under `/v1/search/...`, not `/v1/github/...`
- structural `ast-grep` search also stays under `/v1/search/...` because it is a derived Git-mirror feature, not a GitHub-native resource
