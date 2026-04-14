# Testing And Connectivity

This document describes how `ghreplica` should connect to GitHub repositories and how it should be tested during implementation.

The key constraint is simple:

- we can poll any public repository
- we can only receive webhooks for repositories we control or repositories where users install our app

That constraint should shape both the product and the rollout plan.

## Connectivity Modes

`ghreplica` should support two ingestion modes from the start.

### 1. Poll-Only Mode

This mode works for any public repository.

How it works:

- authenticate to GitHub using a PAT or GitHub App token
- crawl the repository through the REST API
- persist raw responses
- normalize into canonical tables
- periodically re-crawl to keep state reasonably fresh

What it is good for:

- bootstrap
- mirroring public repositories without installation friction
- initial development and testing
- correctness repair even after webhook support exists

What it is bad for:

- low-latency updates
- avoiding excess polling on busy repositories

### 2. Webhook Plus Poll Repair Mode

This is the preferred long-term mode.

How it works:

- configure a GitHub App or repository webhook
- receive webhook deliveries in `ghreplica`
- validate signatures
- persist raw deliveries
- project changes quickly
- run periodic repair crawls to handle dropped or incomplete events

What it is good for:

- fresher data
- lower polling cost
- better support for active repositories

What it requires:

- control of the repository, or
- app installation by the repository or organization owner

## Which Repositories To Use

Testing should use two repository types.

### Fixture Repository

This is the main correctness target.

Properties:

- small
- owned by us
- intentionally scripted activity
- stable enough for deterministic tests

Recommended use:

- create known issues, PRs, labels, comments, reviews, pushes, and merges
- use it for end-to-end testing
- use it for contract tests against GitHub's API

Suggested repo:

- `dutifuldev/ghreplica-fixtures`

### Dogfood Repository

This is the realism target.

Properties:

- active but manageable
- contains real development behavior
- useful for validating triage assumptions

Recommended use:

- run polling against it first
- later enable webhook mode
- use it to validate that the system works outside scripted scenarios

Suggested repo:

- `dutifuldev/ghreplica`

### Optional Busy Public Repository

This is for later scale and edge-case validation.

Properties:

- higher event volume
- more varied issue and PR states
- more realistic pagination and activity patterns

Recommended use:

- poll-only smoke testing
- performance and pagination checks
- not the primary correctness target

## How We Connect To GitHub

### Authentication

Support these options:

- personal access token for simple development
- GitHub App installation token for production-style usage

Recommended development path:

- start with PAT-based polling for public repos
- add GitHub App support before webhook-first operation

## Configuration Inputs

At minimum, the service should support:

- `GITHUB_BASE_URL`
- `GITHUB_TOKEN`
- `GITHUB_WEBHOOK_SECRET`
- `GITHUB_REPO_ALLOWLIST`
- `DATABASE_URL`
- `SYNC_MODE`

Example modes:

- `poll`
- `webhook`
- `hybrid`

## Repository Registration

Each mirrored repository should have a registration record in the local database.

Suggested fields:

- owner
- repo
- full name
- sync mode
- enabled
- last bootstrap time
- last successful crawl time
- last successful webhook time

This gives the system a stable place to manage sync state per repository.

## Testing Strategy

Testing should be split into four layers.

### 1. Unit Tests

Purpose:

- verify normalization logic
- verify projector behavior
- verify triage state derivation

Test inputs:

- saved REST payloads
- saved webhook payloads
- synthetic payload edge cases

Examples:

- issue JSON maps to the correct canonical issue row
- pull request review transitions update review status correctly
- new commits after review move a PR from reviewer-complete to awaiting-review again

### 2. API Contract Tests

Purpose:

- verify that `ghreplica` serves GitHub-compatible responses

Method:

- call GitHub for a fixture repo
- call `ghreplica` for the same repo and endpoint
- compare normalized outputs

Compare:

- status code
- important headers
- pagination behavior
- key response fields

Initial endpoints:

- `GET /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{number}`
- `GET /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{number}`

Later endpoints:

- issue comments
- reviews
- review comments
- commits
- checks

## 3. End-To-End Sync Tests

Purpose:

- verify that the mirror converges to the expected state after real GitHub activity

Method:

- perform scripted actions in the fixture repo
- wait for poll or webhook ingestion
- verify canonical rows and API responses

Suggested scripted scenarios:

- create issue
- create label and apply it
- open PR
- push new commit to PR branch
- request review
- submit approval
- submit changes requested
- comment on issue
- comment on code
- rerun CI or complete check run
- merge PR
- reopen issue or PR if needed

### 4. Replay Tests

Purpose:

- verify that parser and projector changes remain backward compatible

Method:

- store real webhook deliveries and crawl payloads as fixtures
- replay them through the ingestion and projection pipeline
- compare resulting canonical and projection state to snapshots

This is especially important once webhook handling exists.

## What We Should Validate

These are the practical checks that matter most for triage.

### State Correctness

- open vs closed issue state
- open vs closed vs merged PR state
- draft status
- head and base refs
- current labels
- assignees and requested reviewers
- latest commit SHA
- review outcomes
- CI status summary

### Behavioral Correctness

- does a new commit after review change the triage state correctly
- does a dismissed review stop blocking merge
- does a force-push preserve or invalidate the right review context
- do issue comments and review comments remain distinct
- do check runs attach to the right head SHA

### Compatibility Correctness

- endpoint paths
- pagination headers
- response field names
- embedded user and repository summaries
- URL fields

## Recommended Rollout Plan

The implementation should follow a staged plan.

### Phase 1: Poll-Only Vertical Slice

Goal:

- get a working mirror for a small fixture repository

Work:

- bootstrap Go service and Echo server
- add Postgres, GORM models, and migrations
- add repository registration table
- implement GitHub client with PAT auth
- crawl:
  - repository
  - issues
  - pull requests
  - issue comments
  - reviews
  - review comments
- normalize into canonical tables
- expose:
  - `GET /repos/{owner}/{repo}`
  - `GET /repos/{owner}/{repo}/issues`
  - `GET /repos/{owner}/{repo}/pulls`

Exit criteria:

- fixture repo can be mirrored from polling alone
- basic contract tests pass

### Phase 2: Triage Projections

Goal:

- answer useful herding questions

Work:

- implement `timeline_events`
- implement `triage_pull_requests`
- derive:
  - awaiting review
  - awaiting author
  - ready to merge
  - stale
  - latest activity
  - CI summary

Exit criteria:

- can rank and filter PRs for triage from local data

### Phase 3: Webhook Ingestion

Goal:

- reduce sync lag and polling dependence

Work:

- add webhook endpoint
- validate signatures
- store raw deliveries
- project from webhook deliveries
- add repair crawls

Exit criteria:

- fixture repo remains consistent under hybrid mode
- replay tests pass

### Phase 4: Dogfooding

Goal:

- validate system behavior on a real active repository

Work:

- enable mirroring for `dutifuldev/ghreplica`
- compare local mirror against GitHub regularly
- fix mismatches

Exit criteria:

- mirror remains stable on a real repo over time

### Phase 5: App-Based Multi-Repo Support

Goal:

- support repositories we do not directly own

Work:

- add GitHub App authentication flow
- track installations
- support webhook routing per installation
- support repo onboarding and sync state management

Exit criteria:

- users can install the app and mirror their repositories with hybrid sync

## Recommended First Test Repository Workflow

For the fixture repository:

1. create the repo
2. create baseline labels and milestones
3. create a few seed issues
4. create seed PRs in different states
5. create a script that performs deterministic lifecycle actions
6. capture resulting GitHub payloads as fixtures

The system should not rely on ad hoc manual testing for core correctness.

## Bottom Line

The first implementation should connect to a small owned fixture repository through polling, not webhooks.

That is the fastest path to:

- deterministic testing
- schema validation
- API contract checks
- usable initial development

After that, webhook support should be added for freshness, and a GitHub App should be added for multi-repo adoption.
