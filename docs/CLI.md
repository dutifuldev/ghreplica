# CLI

This document describes the `ghr` CLI for `ghreplica`.

`ghr` is a thin read client over the `ghreplica` HTTP API.

It should not invent a new object model, a new output format, or a separate local cache by default.

## Goal

The goal is:

- let users read mirrored repository data from terminals
- make `ghreplica` usable in scripts and shell workflows
- preserve GitHub-compatible shapes and output
- feel familiar to users who already know `gh`

For data that already exists on GitHub, the CLI should present it in the same shape and format as GitHub CLI wherever practical.

That means:

- command families should mirror `gh`
- flags should prefer `gh` naming
- JSON output should use the same field names GitHub exposes
- text output should match `gh` conventions as closely as possible

## Design Rule

The server is the product surface.

The CLI is just a convenience client.

So the CLI should:

- call `ghreplica` endpoints
- format responses the same way `gh` does
- avoid duplicating business logic from the server

The CLI should not:

- maintain its own interpretation of GitHub objects
- silently fetch from GitHub directly when data is missing from `ghreplica`
- invent `ghreplica`-specific response shapes for GitHub-native data

## Command Shape

The command tree should follow the `gh` mental model.

Repository selection should also follow `gh`:

- `repo view` may take a positional `OWNER/REPO`
- `repo status` is a `ghreplica`-specific command and should use `-R/--repo`
- `issue` and `pr` commands should use `-R/--repo` instead of a positional repo argument

Current command shape:

- `ghr issue list`
- `ghr issue view`
- `ghr issue comments`
- `ghr pr list`
- `ghr pr view`
- `ghr pr reviews`
- `ghr pr comments`
- `ghr repo status`
- `ghr repo view`
- `ghr changes repo status`
- `ghr changes pr status`
- `ghr changes pr view`
- `ghr changes pr files`
- `ghr changes commit view`
- `ghr changes commit files`
- `ghr changes compare`
- `ghr search related-prs`
- `ghr search prs-by-paths`
- `ghr search prs-by-ranges`

The first target is read-only parity for the endpoints `ghreplica` already serves.

Examples:

```bash
ghr repo view openclaw/openclaw
ghr repo status -R openclaw/openclaw
ghr issue list -R openclaw/openclaw --state all
ghr issue view -R openclaw/openclaw 66797 --comments
ghr issue comments -R openclaw/openclaw 66797
ghr pr list -R openclaw/openclaw --state all
ghr pr view -R openclaw/openclaw 66863 --comments
ghr pr reviews -R openclaw/openclaw 66795
ghr pr comments -R openclaw/openclaw 66795
ghr changes repo status -R openclaw/openclaw
ghr changes pr status -R openclaw/openclaw 59883
ghr changes pr view -R openclaw/openclaw 59883
ghr changes pr files -R openclaw/openclaw 59883
ghr changes commit view -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes commit files -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes compare -R openclaw/openclaw main...5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr search related-prs -R openclaw/openclaw 59883 --mode path_overlap
ghr search prs-by-paths -R openclaw/openclaw --path src/acp/control-plane/manager.core.ts --state all
ghr search prs-by-ranges -R openclaw/openclaw --path extensions/telegram/src/fetch.ts --start 24 --end 36 --state all
```

## API Mapping

The CLI should map directly onto the versioned API namespaces.

The boundary is:

- `repo`, `issue`, and `pr` map to `/v1/github/...`
- `changes` maps to `/v1/changes/...`
- `search` maps to `/v1/search/...`

That keeps GitHub-shaped reads separate from normalized git-change reads and `ghreplica`-specific search features.

### GitHub-compatible read surface

- `ghr repo view <owner>/<repo>`
  - `GET /v1/github/repos/{owner}/{repo}`
- `ghr repo status -R <owner>/<repo>`
  - `GET /repos/{owner}/{repo}/_ghreplica`
- `ghr issue list -R <owner>/<repo>`
  - `GET /v1/github/repos/{owner}/{repo}/issues`
- `ghr issue view -R <owner>/<repo> <number>`
  - `GET /v1/github/repos/{owner}/{repo}/issues/{number}`
- `ghr issue comments -R <owner>/<repo> <number>`
  - `GET /v1/github/repos/{owner}/{repo}/issues/{number}/comments`
- `ghr pr list -R <owner>/<repo>`
  - `GET /v1/github/repos/{owner}/{repo}/pulls`
- `ghr pr view -R <owner>/<repo> <number>`
  - `GET /v1/github/repos/{owner}/{repo}/pulls/{number}`
- `ghr pr reviews -R <owner>/<repo> <number>`
  - `GET /v1/github/repos/{owner}/{repo}/pulls/{number}/reviews`
- `ghr pr comments -R <owner>/<repo> <number>`
  - `GET /v1/github/repos/{owner}/{repo}/pulls/{number}/comments`

### Git change surface

- `ghr changes repo status -R <owner>/<repo>`
  - `GET /v1/changes/repos/{owner}/{repo}/status`
- `ghr changes pr status -R <owner>/<repo> <number>`
  - `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}/status`
- `ghr changes pr view -R <owner>/<repo> <number>`
  - `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}`
- `ghr changes pr files -R <owner>/<repo> <number>`
  - `GET /v1/changes/repos/{owner}/{repo}/pulls/{number}/files`
- `ghr changes commit view -R <owner>/<repo> <sha>`
  - `GET /v1/changes/repos/{owner}/{repo}/commits/{sha}`
- `ghr changes commit files -R <owner>/<repo> <sha>`
  - `GET /v1/changes/repos/{owner}/{repo}/commits/{sha}/files`
- `ghr changes compare -R <owner>/<repo> <base>...<head>`
  - `GET /v1/changes/repos/{owner}/{repo}/compare/{base}...{head}`

### Search surface

- `ghr search related-prs -R <owner>/<repo> <number> --mode path_overlap`
  - `GET /v1/search/repos/{owner}/{repo}/pulls/{number}/related?mode=path_overlap`
- `ghr search related-prs -R <owner>/<repo> <number> --mode range_overlap`
  - `GET /v1/search/repos/{owner}/{repo}/pulls/{number}/related?mode=range_overlap`
- `ghr search prs-by-paths -R <owner>/<repo> --path <path>`
  - `POST /v1/search/repos/{owner}/{repo}/pulls/by-paths`
- `ghr search prs-by-ranges -R <owner>/<repo> --path <path> --start <n> --end <n>`
  - `POST /v1/search/repos/{owner}/{repo}/pulls/by-ranges`
- `ghr search mentions -R <owner>/<repo> --query <expr>`
  - `POST /v1/search/repos/{owner}/{repo}/mentions`

Planned flags for `ghr search mentions`:

- `--mode`
  - `fts`
  - `fuzzy`
  - `regex`
- `--scope`
  - `pull_requests`
  - `issues`
  - `issue_comments`
  - `pull_request_reviews`
  - `pull_request_review_comments`
- `--state`
- `--author`
- `--limit`
- `--page`

The `search` responses should preserve the reasons for the match, not just the PR numbers.

That means surfacing fields like:

- `score`
- `shared_paths`
- `overlapping_hunks`
- `matched_ranges`
- `indexed_as`
- `index_freshness`

The `changes ... status` commands should surface indexing truth directly, including:

- `indexed_as`
- `index_freshness`
- `indexed_at`
- `head_sha`
- `base_sha`
- `merge_base_sha`
- coverage counts that explain whether range-overlap search is trustworthy for that PR or repo

For `ghr search mentions`, the response should preserve why a text match happened, not just the object number.

That means surfacing fields like:

- `resource`
  - `type`
  - `id`
  - `number`
  - `api_url`
  - `html_url`
- `matched_field`
- `excerpt`
- `score`

## Compatibility Expectations

For the overlapping read surface, the CLI should try to match `gh` in:

- command names
- positional argument patterns
- core flags
- JSON field names
- human-readable output ordering and labels

This is especially important for:

- `issue list`
- `issue view`
- `pr list`
- `pr view`
- `repo view`

`repo status` is the exception because it exposes mirror metadata that GitHub does not define.

If `gh` prints GitHub-native data in a certain shape, `ghr` should aim to print the mirrored data the same way.

The `changes` and `search` command groups are the explicit exceptions.

They should still be consistent and scriptable, but they are not trying to mimic `gh`, because GitHub CLI does not define those surfaces.

## Output Modes

The CLI should support two primary output modes.

### 1. Human-readable output

This should be the default.

It should mimic `gh` as closely as possible for:

- headings
- tables
- labels
- ordering
- summary lines

### 2. Structured output

This should support:

- `--json`
- `-R, --repo`
- `--comments` on `issue view` and `pr view`
- `--web` on `repo view`, `issue view`, and `pr view`
- later, possibly `--jq`
- later, possibly `--template`

The JSON output should preserve GitHub field names and nested object structure.

## Configuration

The CLI should accept:

- `--base-url`
- optional auth configuration later if the hosted mirror requires it

Reasonable defaults:

- `GHR_BASE_URL`
- default local base URL for development
- explicit base URL for hosted environments such as `https://ghreplica.dutiful.dev`

## Implementation Guidance

Use `Cobra` as the CLI framework.

Why:

- it is the standard choice for multi-command Go CLIs
- it matches the command-tree style we want
- GitHub CLI itself uses a Cobra-based command layout
- it keeps help, completions, and nested subcommands straightforward

So the intended stack for the read CLI is:

- `Cobra` for command and flag structure
- a thin HTTP client for talking to `ghreplica`
- formatting code that mirrors `gh` output conventions

Avoid inventing a custom parser layer unless Cobra becomes a real limitation.

Use the local `gh` CLI source as the UX reference:

- `~/repos/gh-cli/pkg/cmd/issue`
- `~/repos/gh-cli/pkg/cmd/pr`
- `~/repos/gh-cli/pkg/cmd/repo`

The goal is not to copy implementation details blindly.

The goal is to copy the user-facing contract:

- same command family layout
- same flags where the data surface overlaps
- same output style where possible

## Initial Scope

Current read scope matches the server endpoints already supported:

- repository view
- repository mirror status
- issue list
- issue view
- issue comments
- pull request list
- pull request view
- pull request reviews
- pull request review comments
- pull request change snapshot
- pull request changed files
- commit details
- commit changed files
- compare details
- related PR search
- PR search by file paths
- PR search by line ranges

That is enough to make the CLI useful for debugging, triage, and agent workflows.

## Non-Goals

For the first version, do not add:

- write operations
- local sync logic
- GitHub fallback reads
- a second schema layer
- repo bootstrap controls in the read CLI

Operator functions can stay on the main `ghreplica` binary.

The read CLI should stay focused on consuming mirrored data.
