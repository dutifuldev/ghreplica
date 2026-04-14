# Compatibility Strategy

This document describes how `ghreplica` should ensure that its API matches GitHub's API as closely as possible.

The core principle is:

- compatibility should be enforced by tests and declared support boundaries
- not by informal similarity or manual inspection

`ghreplica` should not claim general GitHub compatibility unless it has contract coverage proving that claim for a defined subset of endpoints.

## Compatibility Goal

The goal is not immediate parity with the entire GitHub API.

The goal is:

- exact or near-exact compatibility for a declared subset of repo-scoped endpoints
- explicit non-support outside that subset
- progressive expansion of supported endpoints only when contract coverage exists

This is the only realistic way to stay rigorous.

## Source Of Truth

`ghreplica` should treat GitHub's public API surface as the external contract.

Primary sources of truth:

- GitHub REST OpenAPI description
- official GitHub REST documentation
- official GitHub GraphQL documentation
- live GitHub responses for behavior not fully captured by schemas

Each source serves a different role:

- OpenAPI describes paths, methods, parameters, and many schemas
- docs describe behavior like pagination, media types, and rate limits
- live responses reveal edge cases, omissions, defaults, and ordering behavior

## What Compatibility Means

For a supported endpoint, compatibility includes more than the body schema.

### Request Compatibility

- same path shape
- same HTTP method
- same path parameter names
- same supported query parameters
- same header semantics where supported

### Response Compatibility

- same status code class and expected status codes
- same key headers
- same field names
- same nested object shapes
- same list ordering behavior
- same null vs omitted behavior where practical
- same pagination semantics

### Behavioral Compatibility

- same state transitions visible through the API
- same distinctions between issues, PRs, reviews, and comments
- same mergeability and check-state interpretations where mirrored
- same relationships between resources

## Supported Subset Policy

`ghreplica` should maintain an explicit list of supported endpoints.

For each supported endpoint:

- the route must exist
- the query semantics must be defined
- there must be a contract test
- there must be fixture coverage for at least one realistic scenario

For unsupported endpoints:

- do not fake compatibility
- return a clear non-support response such as `501 Not Implemented` or equivalent documented behavior

This is better than implementing endpoints partially and silently diverging from GitHub.

## How To Compare `ghreplica` Against GitHub

Use a compatibility harness that sends the same request to both services.

For each test case:

1. send request to GitHub
2. send equivalent request to `ghreplica`
3. capture:
   - status code
   - response headers
   - response body
4. normalize fields that are expected to differ
5. compare the normalized results

This should be automated and run in CI for supported endpoints.

## What To Compare

### Status Codes

Compare:

- exact status code for happy paths
- exact status code for common errors where supported

Examples:

- `200`
- `404`
- `422`

### Headers

Compare key headers where relevant:

- `Content-Type`
- `Link`
- `ETag`
- rate limit headers where supported

Notes:

- some headers may intentionally differ in a replica
- if so, that should be explicit in the normalization rules

### Bodies

Compare:

- field presence
- field names
- nested object structure
- array ordering
- null versus omitted fields where clients care

Do not reduce compatibility checks to loose schema validation only.

## Normalization Rules

Direct byte-for-byte equality is often not realistic for a mirror.

The harness should normalize only the fields that are expected to differ structurally for replica reasons.

Examples of fields that may need normalization:

- base hostnames
- `url` and `html_url` host prefixes if `ghreplica` rewrites them
- rate-limit values if local limits differ from GitHub's
- timestamps that reflect local ingestion rather than mirrored resource timestamps

Examples of fields that should not be normalized away:

- `id`
- `node_id`
- `number`
- `state`
- `title`
- `body`
- `labels`
- `requested_reviewers`
- `head` and `base` refs
- review state
- check conclusion

The normalization rule should always be conservative. If you normalize too much, you stop testing compatibility.

## Contract Test Levels

Compatibility testing should happen at multiple levels.

### 1. Schema-Level Contract Tests

Purpose:

- verify that response shapes match for supported endpoints

Method:

- compare JSON structure and key fields for fixture repo requests

Good for:

- repository metadata
- issue lists
- PR detail
- comments
- labels

### 2. Parameter Behavior Tests

Purpose:

- verify that query parameters behave like GitHub

Test cases should cover:

- `state`
- `sort`
- `direction`
- `per_page`
- `page`
- later endpoint-specific filters like labels or base branch

This catches mismatches that schema comparisons alone do not reveal.

### 3. Pagination Tests

Purpose:

- verify list endpoints across multiple pages

Compare:

- item ordering
- item boundaries between pages
- `Link` header content and semantics
- empty-page behavior

### 4. Behavioral Transition Tests

Purpose:

- verify that mirrored API output changes the same way GitHub's would after events

Examples:

- opening a PR
- pushing a new commit to a PR
- requesting review
- approving a PR
- requesting changes
- adding and removing labels
- merging a PR

This is where the hardest compatibility problems show up.

### 5. Replay Tests

Purpose:

- prevent regressions in ingestion and projection logic

Method:

- replay captured webhook deliveries and crawl responses
- verify final canonical and API output state

Replay tests are essential because compatibility depends on convergence, not just on one-shot handlers.

## Fixture Strategy

Use a controlled fixture repository as the primary compatibility target.

The fixture repo should contain:

- normal issues
- PR-backed issues
- labels and milestones
- issue comments
- PR reviews
- review comments
- multiple commits on PR branches
- CI status and checks
- merged and closed PRs

This repo allows deterministic test cases and stable snapshots.

Later, validate against one real active repository for realism.

## Endpoint Rollout Strategy

Endpoints should be added in ordered slices.

### Slice 1

- `GET /repos/{owner}/{repo}`
- `GET /repos/{owner}/{repo}/issues`
- `GET /repos/{owner}/{repo}/issues/{number}`
- `GET /repos/{owner}/{repo}/pulls`
- `GET /repos/{owner}/{repo}/pulls/{number}`

### Slice 2

- issue comments
- labels
- milestones
- pull request reviews
- pull request review comments

### Slice 3

- commits
- compare
- branches and refs
- check runs
- check suites

### Slice 4

- releases
- actions workflow metadata
- reactions

No slice should be declared complete without contract tests.

## OpenAPI Coverage Tracking

The GitHub REST OpenAPI spec should be used to track supported coverage.

Recommended practice:

- maintain a local manifest of supported operations
- map each operation to:
  - handler implementation
  - contract test file
  - fixture scenarios

This avoids drift between claims and reality.

## Known Hard Problems

These are areas where one-to-one behavior is hardest.

### Issues And Pull Requests

GitHub exposes pull requests both as issues and as pull resources.

Risk:

- issue list endpoints may include PR-backed issues
- PR detail endpoints include additional fields

The mirror must preserve that duality exactly.

### Review Semantics

Review state is not just a list of comments.

Risk:

- comment-only review vs formal approval vs changes requested
- review dismissal
- stale approval after new commits

These behaviors must be modeled carefully in projections and API responses.

### Mergeability

GitHub's `mergeable` and `mergeable_state` fields can be subtle and time-dependent.

Risk:

- the mirror may lag
- mergeability may not be immediately known

The mirror should be honest about unsupported or delayed mergeability rather than fabricating certainty.

### URL Fields

Many GitHub responses include related resource URLs and URI templates.

Risk:

- clients may follow these links directly

The mirror should preserve URL field structure and template behavior as much as possible.

### Null Versus Omitted

Some clients rely on GitHub's exact response shape.

Risk:

- returning `{}` instead of `null`
- omitting a field GitHub usually includes as `null`
- returning empty arrays where GitHub returns `204`

These differences can break compatibility even when the data is conceptually the same.

## CI Policy

Supported endpoint compatibility tests should run in CI.

Recommended split:

- fast fixture-based tests on every PR
- replay tests on every PR
- live GitHub contract tests on a scheduled job and on demand

Reason:

- live GitHub comparisons can be flaky due to external changes
- saved fixtures keep PR feedback deterministic
- scheduled live tests detect drift against the real API

## Definition Of Done For A Supported Endpoint

An endpoint should be considered supported only when:

- handler is implemented
- canonical data needed for the endpoint is mirrored
- pagination and key params are handled
- contract test exists
- fixture scenario exists
- docs list the endpoint as supported

Without these, the endpoint is not done.

## Bottom Line

The strategy to make `ghreplica` match GitHub one-to-one is:

- define a supported subset
- treat GitHub as the contract
- compare `ghreplica` and GitHub directly
- normalize only what must differ
- use fixture, replay, and live contract tests
- expand coverage only when tests prove compatibility

That is the disciplined path to real API parity.
