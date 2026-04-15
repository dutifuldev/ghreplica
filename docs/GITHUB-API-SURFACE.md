# GitHub API Surface Research

This document summarizes the public GitHub API surface relevant to `ghreplica`, based on the official GitHub docs and GitHub's public REST OpenAPI description.

It is not an exhaustive reproduction of every endpoint or every field. The goal is to identify:

- which API products GitHub exposes
- which repo-scoped endpoint families matter for a mirror
- what the common data shapes look like
- which protocol behaviors `ghreplica` must preserve for compatibility

## Sources

- [GitHub REST docs](https://docs.github.com/en/rest)
- [Getting started with the REST API](https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api)
- [Using pagination in the REST API](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api)
- [Rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [GitHub GraphQL API docs](https://docs.github.com/en/graphql)
- [About the GraphQL API](https://docs.github.com/en/graphql/overview/about-the-graphql-api)
- [Rate limits and query limits for the GraphQL API](https://docs.github.com/en/graphql/overview/resource-limitations)
- [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads)
- [GitHub REST OpenAPI description](https://github.com/github/rest-api-description/blob/main/descriptions-next/api.github.com/api.github.com.json)

## Top-Level API Products

GitHub exposes three distinct integration surfaces:

1. REST API
   - Resource-oriented HTTP API.
   - This is the most practical target for `ghreplica` v1.
2. GraphQL API
   - Single endpoint graph API at `https://api.github.com/graphql`.
   - Strongly typed, introspectable, connection-based schema.
3. Webhooks
   - Event delivery channel for changes.
   - Essential for keeping a replica fresh without aggressive polling.

For a mirror, webhooks are not an alternative to the API. They are an input stream that helps reconstruct the state served by the REST and possibly GraphQL layers.

## REST API: Protocol Shape

### Base conventions

The REST API uses:

- base URL `https://api.github.com`
- standard HTTP methods like `GET`, `POST`, `PATCH`, `PUT`, `DELETE`
- path templates such as `/repos/{owner}/{repo}/issues`

Common request headers:

- `Accept: application/vnd.github+json`
- `X-GitHub-Api-Version: <date-version>`
- `User-Agent: <client-name>`

Important compatibility details:

- many endpoints support custom media types such as `diff`, `patch`, `sha`, `raw`, `text`, `html`, or `full`
- many resources include `*_url` fields that are RFC 6570 URI templates, not plain fixed URLs

### Pagination

The REST API is heavily paginated.

Observed behavior from the docs:

- many list endpoints default to 30 items per page
- many endpoints support `per_page`
- continuation is exposed via the `Link` response header
- pagination styles vary by endpoint: `page`, `before` and `after`, or `since`
- some list-like endpoints can return `204 No Content` instead of an empty array
- some search-style endpoints wrap arrays in objects like `{ total_count, incomplete_results, items }`

For `ghreplica`, pagination behavior is part of compatibility. It is not enough to return the right JSON items; list responses need compatible pagination semantics and headers.

### Rate limits

GitHub rate limiting is part of the API contract.

REST-specific facts from the docs:

- unauthenticated requests: 60 requests per hour
- authenticated user requests: 5,000 requests per hour
- GitHub App installation tokens: at least 5,000 requests per hour, with scaling rules
- rate state is exposed in headers such as `x-ratelimit-limit`, `x-ratelimit-remaining`, `x-ratelimit-used`, `x-ratelimit-reset`, `x-ratelimit-resource`
- `GET /rate_limit` exists for explicit checks

`ghreplica` does not need to copy GitHub's exact quotas, but it should likely preserve the same header names and overall response model.

## REST API: Surface Area

The public GitHub REST OpenAPI description currently reports:

- OpenAPI version: `3.1.0`
- spec info title: `GitHub v3 REST API`
- spec info version: `1.1.4`
- 1,112 operations
- 44 top-level tags

Current tags in the spec:

- `actions`
- `activity`
- `agent-tasks`
- `apps`
- `billing`
- `campaigns`
- `checks`
- `classroom`
- `code-scanning`
- `code-security`
- `codes-of-conduct`
- `codespaces`
- `copilot`
- `credentials`
- `dependabot`
- `dependency-graph`
- `emojis`
- `enterprise-team-memberships`
- `enterprise-team-organizations`
- `enterprise-teams`
- `gists`
- `git`
- `gitignore`
- `hosted-compute`
- `interactions`
- `issues`
- `licenses`
- `markdown`
- `meta`
- `migrations`
- `oidc`
- `orgs`
- `packages`
- `private-registries`
- `projects`
- `pulls`
- `rate-limit`
- `reactions`
- `repos`
- `search`
- `secret-scanning`
- `security-advisories`
- `teams`
- `users`

For `ghreplica`, only a subset needs to be mirrored first.

## REST API: Repo-Mirror-Relevant Families

These are the endpoint families that matter most for a repository-centric replica.

### `repos`

This is the largest tag in the public spec and contains core repository resources.

Representative endpoints:

- `GET /repos/{owner}/{repo}`
- `PATCH /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/branches`
- `GET /repos/{owner}/{repo}/branches/{branch}`
- `GET /repos/{owner}/{repo}/tags`
- `GET /repos/{owner}/{repo}/contributors`
- `GET /repos/{owner}/{repo}/languages`
- `GET /repos/{owner}/{repo}/commits`
- `GET /repos/{owner}/{repo}/commits/{ref}`
- `GET /repos/{owner}/{repo}/compare/{basehead}`
- `GET /repos/{owner}/{repo}/contents/{path}`
- `GET /repos/{owner}/{repo}/readme`
- `GET /repos/{owner}/{repo}/releases`
- `GET /repos/{owner}/{repo}/releases/latest`
- `GET /repos/{owner}/{repo}/events`
- `GET /repos/{owner}/{repo}/hooks`

Mirror significance:

- repository metadata
- default branch and visibility
- repo-level feature flags
- repo-level derived resources and URL templates

### `issues`

GitHub models issues as a first-class resource family.

Representative endpoints:

- `GET /repos/{owner}/{repo}/issues`
- `POST /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{issue_number}`
- `PATCH /repos/{owner}/{repo}/issues/{issue_number}`
- `GET /repos/{owner}/{repo}/issues/{issue_number}/comments`
- `POST /repos/{owner}/{repo}/issues/{issue_number}/comments`
- `GET /repos/{owner}/{repo}/issues/comments/{comment_id}`
- `PATCH /repos/{owner}/{repo}/issues/comments/{comment_id}`
- `GET /repos/{owner}/{repo}/assignees`
- `GET /repos/{owner}/{repo}/labels`
- `GET /repos/{owner}/{repo}/milestones`

Important schema detail:

- issue objects can include a `pull_request` subobject when the issue is actually a pull request

That means a mirror cannot treat issues and pull requests as disjoint worlds.

### `pulls`

Pull requests are a separate resource family layered on top of issues.

Representative endpoints:

- `GET /repos/{owner}/{repo}/pulls`
- `POST /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}`
- `PATCH /repos/{owner}/{repo}/pulls/{pull_number}`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}/commits`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}/files`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}/comments`
- `POST /repos/{owner}/{repo}/pulls/{pull_number}/comments`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}/reviews`
- `POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews`
- `GET /repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers`
- `PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge`

Mirror significance:

- PR head and base refs
- mergeability and merge state
- review and code-comment system
- diff and patch representations

### `git`

This family exposes lower-level Git objects.

Representative endpoints:

- `GET /repos/{owner}/{repo}/git/ref/{ref}`
- `GET /repos/{owner}/{repo}/git/matching-refs/{ref}`
- `POST /repos/{owner}/{repo}/git/refs`
- `PATCH /repos/{owner}/{repo}/git/refs/{ref}`
- `DELETE /repos/{owner}/{repo}/git/refs/{ref}`
- `GET /repos/{owner}/{repo}/git/commits/{commit_sha}`
- `POST /repos/{owner}/{repo}/git/commits`
- `GET /repos/{owner}/{repo}/git/blobs/{file_sha}`
- `POST /repos/{owner}/{repo}/git/blobs`
- `GET /repos/{owner}/{repo}/git/trees/{tree_sha}`
- `POST /repos/{owner}/{repo}/git/trees`
- `POST /repos/{owner}/{repo}/git/tags`

This is important because GitHub's higher-level resources often reference lower-level Git objects by SHA and ref name.

### `checks`

Checks are part of the modern CI and code health surface.

Representative endpoints:

- `GET /repos/{owner}/{repo}/check-runs/{check_run_id}`
- `GET /repos/{owner}/{repo}/check-runs/{check_run_id}/annotations`
- `GET /repos/{owner}/{repo}/commits/{ref}/check-runs`
- `GET /repos/{owner}/{repo}/check-suites/{check_suite_id}`
- `GET /repos/{owner}/{repo}/check-suites/{check_suite_id}/check-runs`
- `GET /repos/{owner}/{repo}/commits/{ref}/check-suites`

If `ghreplica` is intended for review and triage agents, checks are part of the minimum useful surface.

### `actions`

GitHub Actions is much larger than the basic CI state, but a mirror may need a subset.

Representative endpoints:

- `GET /repos/{owner}/{repo}/actions/workflows`
- `GET /repos/{owner}/{repo}/actions/workflows/{workflow_id}`
- `GET /repos/{owner}/{repo}/actions/runs`
- `GET /repos/{owner}/{repo}/actions/runs/{run_id}`
- `GET /repos/{owner}/{repo}/actions/jobs/{job_id}`
- `GET /repos/{owner}/{repo}/actions/artifacts`

For v1, it is reasonable to mirror only read-only workflow, run, and job metadata.

### `activity` and `reactions`

These provide event streams, notifications, stars, watchers, and reactions.

Representative endpoints:

- `GET /events`
- `GET /networks/{owner}/{repo}/events`
- `GET /repos/{owner}/{repo}/issues/{issue_number}/reactions`
- `GET /repos/{owner}/{repo}/issues/comments/{comment_id}/reactions`
- `GET /repos/{owner}/{repo}/pulls/comments/{comment_id}/reactions`

For a repo mirror, reactions and event streams matter more than user inbox notifications.

### `users` and `orgs`

Many repo-scoped resources embed user and org objects.

Representative endpoints:

- `GET /users/{username}`
- `GET /user`
- `GET /orgs/{org}`
- `GET /orgs/{org}/repos`
- `GET /repos/{owner}/{repo}/collaborators`

Even if `ghreplica` starts repo-first, user and org objects are shared dimensions in almost every response schema.

## Common REST Schema Patterns

GitHub REST responses are not random JSON blobs. A mirror should preserve the common shape.

### Cross-cutting fields

Many top-level objects include:

- `id`
- `node_id`
- `url`
- `html_url`
- timestamps like `created_at`, `updated_at`, `closed_at`, `merged_at`, `published_at`
- nested actor objects such as `user`, `owner`, `sender`, `assignee`, `creator`, `merged_by`

This strongly suggests that canonical storage should preserve:

- GitHub numeric IDs
- GitHub node IDs
- API URLs
- HTML URLs
- timestamps in GitHub's field naming

### Hypermedia fields

Many objects contain URL fields pointing to related resources:

- `comments_url`
- `commits_url`
- `statuses_url`
- `labels_url`
- `events_url`
- `review_comments_url`
- `contents_url`

These are part of the contract. Clients often follow them directly instead of building paths themselves.

### Embedded summaries

GitHub frequently embeds partial nested objects instead of forcing separate round trips:

- repository owner inside repository
- labels inside issues and pull requests
- PR head and base objects
- commit summary objects inside branches and checks

That means a mirror needs both canonical entities and response builders that can materialize GitHub-style nested summaries.

## Representative REST Schemas

The public OpenAPI document contains many schema components. These are the most important ones for a repo mirror.

### `simple-user`

Representative fields:

- `login`
- `id`
- `node_id`
- `avatar_url`
- `html_url`
- `type`
- `site_admin`
- `url`
- `repos_url`
- `events_url`
- `received_events_url`

This is the common embedded identity shape used all over the API.

### `repository`

Representative fields:

- `id`
- `node_id`
- `name`
- `full_name`
- `owner`
- `private`
- `html_url`
- `description`
- `fork`
- `url`
- `default_branch`
- `visibility`
- `permissions`
- `topics`
- feature flags such as `has_issues`, `has_projects`, `has_wiki`, `has_discussions`
- merge strategy flags such as `allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`
- many related resource URLs

This is a large schema and acts as a hub for many linked resources.

### `issue`

Representative fields:

- `id`
- `node_id`
- `number`
- `title`
- `body`
- `state`
- `state_reason`
- `user`
- `assignee`
- `assignees`
- `labels`
- `milestone`
- `comments`
- `locked`
- `active_lock_reason`
- `author_association`
- `pull_request`
- `repository_url`
- `comments_url`
- `events_url`
- `html_url`
- `created_at`
- `updated_at`
- `closed_at`

Important note:

- the presence of `pull_request` turns an issue into an issue-backed pull request reference from the issue API point of view

### `pull-request`

Representative fields:

- `id`
- `node_id`
- `number`
- `title`
- `body`
- `state`
- `draft`
- `user`
- `assignee`
- `assignees`
- `labels`
- `milestone`
- `head`
- `base`
- `mergeable`
- `mergeable_state`
- `merged`
- `merged_at`
- `merged_by`
- `merge_commit_sha`
- `commits`
- `additions`
- `deletions`
- `changed_files`
- `requested_reviewers`
- `requested_teams`
- `statuses_url`
- `diff_url`
- `patch_url`
- `review_comments_url`
- `comments_url`

Pull requests are issue-like objects plus Git and review state.

### `issue-comment`

Representative fields:

- `id`
- `node_id`
- `body`
- `body_text`
- `body_html`
- `user`
- `author_association`
- `issue_url`
- `html_url`
- `performed_via_github_app`
- `reactions`
- `created_at`
- `updated_at`

### `pull-request-review`

Representative fields:

- `id`
- `node_id`
- `user`
- `body`
- `body_text`
- `body_html`
- `state`
- `commit_id`
- `submitted_at`
- `pull_request_url`
- `_links`

### `pull-request-review-comment`

Representative fields:

- `id`
- `node_id`
- `user`
- `body`
- `body_text`
- `body_html`
- `path`
- `diff_hunk`
- `position`
- `original_position`
- `line`
- `original_line`
- `start_line`
- `side`
- `start_side`
- `commit_id`
- `original_commit_id`
- `in_reply_to_id`
- `pull_request_review_id`
- `pull_request_url`
- `html_url`
- `reactions`
- `created_at`
- `updated_at`

This schema is important because it encodes line-level review state, not just freeform comments.

### `label`

Representative fields:

- `id`
- `node_id`
- `name`
- `color`
- `description`
- `default`
- `url`

### `milestone`

Representative fields:

- `id`
- `node_id`
- `number`
- `title`
- `description`
- `state`
- `creator`
- `open_issues`
- `closed_issues`
- `labels_url`
- `created_at`
- `updated_at`
- `closed_at`
- `due_on`

### `commit`

Representative fields:

- `sha`
- `node_id`
- `url`
- `html_url`
- `author`
- `committer`
- nested `commit` object
- `parents`
- `stats`
- `files`
- `comments_url`

The nested `commit` object carries author, committer, message, tree, and verification details, while the outer wrapper connects that Git data to GitHub identities and file-level diff metadata.

### `branch-short`

Representative fields:

- `name`
- `commit`
- `protected`

### `git-ref`

Representative fields:

- `ref`
- `node_id`
- `url`
- `object`

### `check-run`

Representative fields:

- `id`
- `node_id`
- `name`
- `head_sha`
- `status`
- `conclusion`
- `started_at`
- `completed_at`
- `details_url`
- `external_id`
- `app`
- `check_suite`
- `output`
- `pull_requests`
- `deployment`

### `check-suite`

Representative fields:

- `id`
- `node_id`
- `head_branch`
- `head_sha`
- `before`
- `after`
- `status`
- `conclusion`
- `app`
- `repository`
- `pull_requests`
- `check_runs_url`
- `latest_check_runs_count`
- `rerequestable`
- `runs_rerequestable`
- `created_at`
- `updated_at`

### `release`

Representative fields:

- `id`
- `node_id`
- `tag_name`
- `target_commitish`
- `name`
- `body`
- `draft`
- `prerelease`
- `immutable`
- `author`
- `assets`
- `assets_url`
- `tarball_url`
- `zipball_url`
- `discussion_url`
- `reactions`
- `published_at`
- `created_at`
- `updated_at`

### `workflow`, `workflow-run`

Representative fields from `workflow`:

- `id`
- `node_id`
- `name`
- `path`
- `state`
- `badge_url`
- `html_url`
- `created_at`
- `updated_at`

Representative fields from `workflow-run`:

- `id`
- `node_id`
- `name`
- `display_title`
- `event`
- `status`
- `conclusion`
- `head_branch`
- `head_sha`
- `head_commit`
- `workflow_id`
- `workflow_url`
- `jobs_url`
- `logs_url`
- `artifacts_url`
- `run_number`
- `run_attempt`
- `run_started_at`
- `actor`
- `triggering_actor`
- `repository`
- `pull_requests`
- `created_at`
- `updated_at`

### `reaction`

Representative fields:

- `id`
- `node_id`
- `user`
- `content`
- `created_at`

## GraphQL API: What Matters

GitHub's GraphQL API is a separate product, not just another serialization of REST.

Important characteristics from the docs:

- root endpoint: `https://api.github.com/graphql`
- main operation style: `POST`
- introspection is supported
- schema is strongly typed and introspectable
- operations are `query` and `mutation`
- object graphs use connections and edges for pagination
- most connections require `first` or `last`, with values between 1 and 100
- total node count per request is capped at 500,000
- rate limiting is point-based rather than request-count-based

Representative GraphQL concepts relevant to a mirror:

- `Node` IDs and global node references
- `Repository`, `Issue`, `PullRequest`, `Label`, `Commit`, `CheckRun`, `CheckSuite`, `User`, `Organization`
- connections like `IssueConnection`, `PullRequestConnection`, `LabelConnection`
- nested traversal that can replace multiple REST calls in one request

Practical implication:

- `ghreplica` should treat GraphQL as a separate API surface layered on the same canonical data
- v1 should likely focus on REST parity first
- GraphQL support should come later, probably read-only first, and likely only for a subset of the schema that matters to repo workflows

## Webhooks: Input Surface For Replication

Webhooks are the most efficient change feed GitHub provides.

Observed protocol details:

- payload cap: 25 MB
- delivery headers include:
  - `X-GitHub-Hook-ID`
  - `X-GitHub-Event`
  - `X-GitHub-Delivery`
  - `X-Hub-Signature`
  - `X-Hub-Signature-256`
  - `User-Agent: GitHub-Hookshot/...`
  - installation target headers

Common payload fields:

- `action`
- `enterprise`
- `installation`
- `organization`
- `repository`
- `sender`

Repo-mirror-relevant webhook events include:

- `create`
- `delete`
- `push`
- `issues`
- `issue_comment`
- `label`
- `milestone`
- `pull_request`
- `pull_request_review`
- `pull_request_review_comment`
- `pull_request_review_thread`
- `check_run`
- `check_suite`
- `commit_comment`
- `release`
- `repository`
- `discussion`
- `workflow_job`
- `workflow_run`

Important implication:

- webhooks do not replace crawling
- webhook deliveries can be dropped, delayed, or capped
- the replica needs raw webhook storage plus periodic repair crawls against the API

## Compatibility Implications For `ghreplica`

Based on the public API shape, a faithful mirror needs to preserve more than object fields.

### Things that must look GitHub-like

- endpoint paths
- path and query parameter names
- pagination headers and semantics
- numeric IDs and `node_id`
- `url`, `html_url`, and related `*_url` fields
- list response shapes
- timestamp field names
- media type variants where practical
- rate limit headers

### Things that should be canonicalized internally

- users and organizations
- repositories
- issues and pull requests as linked but distinct entity types
- comments split by issue comments, review comments, and review objects
- labels and milestones
- commits, refs, branches, trees, blobs, tags
- checks and workflow runs
- reactions
- webhook deliveries and crawl snapshots

### Recommended v1 API target

If the goal is repo mirroring for triage and review agents, the smallest useful read-only target is:

- repository metadata
- branches and refs
- commits and compare
- issues, labels, milestones, issue comments
- pull requests, files, reviews, review comments, requested reviewers
- check suites and check runs
- releases
- users and org summaries needed to embed inside those responses

This already covers the majority of repo-scoped GitHub automation workflows.

## Bottom Line

GitHub's public API is large, but the repo-centric part has a clear shape:

- REST is the main compatibility target
- GraphQL is a separate typed API that can be layered on later
- webhooks are an input stream, not a standalone source of truth
- most important schemas revolve around repository, issue, pull request, review, comment, commit, check, release, and user objects
- compatibility depends on protocol details such as pagination, media types, and URL templates, not just JSON fields

That should be the working baseline for designing `ghreplica`'s canonical model and its first API surface.
