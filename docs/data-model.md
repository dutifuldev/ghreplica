# Data Model For PR Triage

This document describes a practical relational data model for `ghreplica` if the main product goal is PR herding and triage.

The design follows GitHub's structure closely enough to preserve compatibility, but it is optimized for the questions a triage system actually needs to answer:

- what PRs are open and actionable
- who owns each PR
- who is blocking progress
- what changed recently
- whether the author or reviewers are waiting on each other
- whether CI is green
- whether the PR is mergeable

## Design Principles

- Model pull requests as issues plus PR-specific state.
- Keep canonical GitHub-like tables separate from triage-oriented projection tables.
- Preserve GitHub IDs and numbers.
- Store raw webhook and crawl data for replay and repair.
- Use JSONB for long-tail fields, not core query paths.
- Keep the table design explicit even if the implementation uses GORM models.

## Table Layers

The schema should be split into three layers:

1. canonical tables
   - normalized GitHub-like entities
2. raw sync tables
   - webhook deliveries and crawl snapshots
3. projection tables
   - triage-optimized materializations

In implementation terms, these tables should map to distinct GORM models rather than one generic persistence model.

## Canonical Tables

### `repositories`

Represents GitHub repositories.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `owner_login`
- `name`
- `full_name`
- `default_branch`
- `private`
- `archived`
- `disabled`
- `metadata_jsonb`
- `created_at`
- `updated_at`

Notes:

- `github_id` and `node_id` should be preserved exactly as received.
- `full_name` should be unique.
- `metadata_jsonb` can hold infrequently queried fields such as homepage, permissions, topics, or extra URL templates.

### `users`

Represents GitHub users and bots embedded throughout the API.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `login`
- `type`
- `site_admin`
- `name`
- `avatar_url`
- `html_url`
- `user_jsonb`
- `created_at`
- `updated_at`

Notes:

- `login` should be indexed.
- Keep this separate from git author and committer strings, which do not always map cleanly to GitHub users.

### `issues`

Represents GitHub issues, including the issue component of pull requests.

Suggested columns:

- `id`
- `repository_id`
- `github_id`
- `node_id`
- `number`
- `title`
- `body`
- `state`
- `state_reason`
- `author_id`
- `assignee_id`
- `milestone_id`
- `locked`
- `closed_at`
- `created_at`
- `updated_at`
- `issue_jsonb`

Key constraints:

- unique `(repository_id, number)`

Notes:

- this table must include both regular issues and PR-backed issues
- the PR-specific parts live in `pull_requests`

### `pull_requests`

Represents pull-request-specific state.

Suggested columns:

- `issue_id`
- `github_id`
- `node_id`
- `head_repo_id`
- `head_ref`
- `head_sha`
- `base_repo_id`
- `base_ref`
- `base_sha`
- `draft`
- `mergeable`
- `mergeable_state`
- `merged`
- `merged_at`
- `merged_by_id`
- `merge_commit_sha`
- `additions`
- `deletions`
- `changed_files`
- `commits_count`
- `pr_jsonb`

Key constraints:

- primary key `issue_id`
- foreign key to `issues.id`

Notes:

- this is a 1:1 extension of `issues`
- this is the right shape because GitHub PRs are issue-like resources with additional Git and review state

### `issue_comments`

Represents issue comments on issues and pull requests.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `issue_id`
- `author_id`
- `body`
- `created_at`
- `updated_at`
- `comment_jsonb`

Notes:

- comments on pull requests through the issue comments API belong here
- inline code review comments do not belong here

### `pull_request_reviews`

Represents review objects such as approval, changes requested, or comment-only review submissions.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `pull_request_id`
- `author_id`
- `state`
- `body`
- `commit_sha`
- `submitted_at`
- `review_jsonb`

Notes:

- `state` typically captures values like `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, or `DISMISSED`

### `pull_request_review_comments`

Represents inline review comments tied to file positions.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `pull_request_id`
- `review_id`
- `author_id`
- `in_reply_to_comment_id`
- `path`
- `diff_hunk`
- `position`
- `original_position`
- `line`
- `original_line`
- `side`
- `start_line`
- `start_side`
- `body`
- `created_at`
- `updated_at`
- `comment_jsonb`

Notes:

- this table is distinct from `issue_comments`
- the line and side fields are critical for code review workflows

### `labels`

Represents repository labels.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `repository_id`
- `name`
- `color`
- `description`
- `is_default`

Notes:

- enforce unique `(repository_id, name)`

### `issue_labels`

Join table between issues and labels.

Suggested columns:

- `issue_id`
- `label_id`

Notes:

- PRs inherit labels through their issue row

### `milestones`

Represents repository milestones.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `repository_id`
- `number`
- `title`
- `description`
- `state`
- `creator_id`
- `due_on`
- `closed_at`

Notes:

- enforce unique `(repository_id, number)`

### `commits`

Represents commit metadata as exposed by GitHub.

Suggested columns:

- `id`
- `repository_id`
- `sha`
- `author_user_id`
- `committer_user_id`
- `author_name`
- `author_email`
- `committer_name`
- `committer_email`
- `message`
- `committed_at`
- `commit_jsonb`

Notes:

- `sha` should be heavily indexed
- preserve both GitHub-linked users and raw git identity strings

### `pull_request_commits`

Maps commits onto pull requests.

Suggested columns:

- `pull_request_id`
- `commit_sha`
- `position`

Notes:

- `position` is useful when preserving the observed PR commit order

### `check_suites`

Represents GitHub check suites.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `repository_id`
- `head_sha`
- `head_branch`
- `status`
- `conclusion`
- `app_id`
- `created_at`
- `updated_at`
- `suite_jsonb`

### `check_runs`

Represents GitHub check runs.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `check_suite_id`
- `repository_id`
- `head_sha`
- `name`
- `status`
- `conclusion`
- `details_url`
- `started_at`
- `completed_at`
- `app_id`
- `run_jsonb`

Notes:

- for triage, `head_sha`, `status`, and `conclusion` are the most important fields

### `reactions`

Represents reactions on issues, comments, and review comments.

Suggested columns:

- `id`
- `github_id`
- `node_id`
- `user_id`
- `subject_type`
- `subject_id`
- `content`
- `created_at`

Notes:

- `subject_type` can distinguish issue, issue_comment, review_comment, or other supported subjects

### `timeline_events`

Represents user and system activity over time in a normalized event stream.

Suggested columns:

- `id`
- `repository_id`
- `issue_id`
- `pull_request_id`
- `actor_id`
- `event_type`
- `occurred_at`
- `subject_type`
- `subject_id`
- `payload_jsonb`

Notes:

- this is one of the most valuable tables for triage
- it answers behavior questions that current-state tables cannot answer cleanly

Examples of events that belong here:

- PR opened
- PR synchronized after pushes
- review requested
- review submitted
- issue comment added
- review comment added
- label added
- label removed
- check run completed
- PR merged
- PR closed
- PR reopened

## Raw Sync Tables

These tables are not user-facing, but they are required for a reliable mirror.

### `webhook_deliveries`

Stores each webhook delivery before normalization.

Suggested columns:

- `delivery_id`
- `event`
- `action`
- `repository_id`
- `received_at`
- `headers_jsonb`
- `payload_jsonb`
- `processed_at`

Notes:

- `delivery_id` should be unique
- this table enables replay, debugging, and idempotent ingestion

### `crawl_runs`

Stores pull-based synchronization state and fetch metadata.

Suggested columns:

- `id`
- `repository_id`
- `source`
- `started_at`
- `finished_at`
- `status`
- `cursor_jsonb`
- `response_meta_jsonb`

Notes:

- use this for checkpoints, ETags, pagination cursors, and repair jobs

## Projection Tables

These tables are derived from the canonical model and optimized for triage queries.

### `triage_pull_requests`

Materialized view or projection table containing one row per PR.

Suggested columns:

- `pull_request_id`
- `repository_id`
- `number`
- `title`
- `author_id`
- `state`
- `draft`
- `merged`
- `head_sha`
- `base_ref`
- `opened_at`
- `updated_at`
- `last_activity_at`
- `last_author_activity_at`
- `last_reviewer_activity_at`
- `review_status`
- `ci_status`
- `merge_status`
- `conflict_status`
- `awaiting_review`
- `awaiting_author`
- `ready_to_merge`
- `stale`
- `requested_reviewer_ids`
- `label_names`
- `priority_score`
- `triage_reason`

Notes:

- this is the table most triage UIs and agents should query first
- it should be maintained by projectors, not by direct writes from handlers

### `user_triage_stats`

Derived per-user stats useful for routing and load awareness.

Suggested columns:

- `user_id`
- `repository_id`
- `open_prs_authored`
- `open_prs_reviewing`
- `requested_reviews_pending`
- `median_review_turnaround_hours`
- `median_author_response_hours`
- `changes_requested_open_count`
- `approvals_open_count`
- `last_active_at`

Notes:

- this helps answer questions like who is overloaded, who is responsive, and which reviewers are bottlenecks

## Recommended Indexes

At minimum, index:

- `repositories.full_name`
- `users.login`
- `issues (repository_id, number)`
- `issues (repository_id, state, updated_at)`
- `issues (author_id)`
- `pull_requests (head_sha)`
- `pull_requests (base_ref)`
- `pull_request_reviews (pull_request_id, submitted_at)`
- `pull_request_review_comments (pull_request_id, created_at)`
- `issue_comments (issue_id, created_at)`
- `commits (repository_id, sha)`
- `check_runs (repository_id, head_sha)`
- `timeline_events (repository_id, occurred_at)`
- `timeline_events (pull_request_id, occurred_at)`
- `timeline_events (actor_id, occurred_at)`

## Why This Shape Works For Triage

This model supports the core triage questions directly:

- open PRs by state, label, author, reviewer, or freshness
- latest activity on a PR
- whether a PR is waiting on reviewers or the author
- whether requested reviewers responded
- whether new commits arrived after review
- whether CI is failing, pending, or green
- whether a PR is mergeable
- who is most active, blocked, or overloaded

It also stays close enough to GitHub's resource model that building a GitHub-compatible API on top remains practical.

## Minimum Useful V1

If implementation needs to start small, the minimum useful set is:

- `repositories`
- `users`
- `issues`
- `pull_requests`
- `issue_comments`
- `pull_request_reviews`
- `pull_request_review_comments`
- `labels`
- `issue_labels`
- `commits`
- `pull_request_commits`
- `check_runs`
- `timeline_events`
- `webhook_deliveries`
- `triage_pull_requests`

That is enough to support a strong first version of PR triage and herding.
