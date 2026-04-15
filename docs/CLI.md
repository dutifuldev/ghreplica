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
- `ghr search status`
- `ghr search mentions`
- `ghr search ast-grep`

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
ghr search status -R openclaw/openclaw
ghr search mentions -R openclaw/openclaw --query "heartbeat watchdog" --mode fts --scope pull_requests --scope issues
ghr search mentions -R openclaw/openclaw --query "watch dog" --mode fuzzy --scope pull_requests
ghr search mentions -R openclaw/openclaw --query "auth.*state" --mode regex --scope pull_requests --state all
ghr search ast-grep -R openclaw/openclaw --pr 59883 --language typescript --pattern 'ctx.reply($MSG)' --changed-files-only
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
- `ghr search status -R <owner>/<repo>`
  - `GET /v1/search/repos/{owner}/{repo}/status`
- `ghr search mentions -R <owner>/<repo> --query <expr>`
  - `POST /v1/search/repos/{owner}/{repo}/mentions`
- `ghr search ast-grep -R <owner>/<repo> --pr <number> --language <lang> --pattern <pattern>`
  - `POST /v1/search/repos/{owner}/{repo}/ast-grep`

### Search workflow

The `search` group has two distinct jobs:

- overlap search over mirrored code-change data
  - `related-prs`
  - `prs-by-paths`
  - `prs-by-ranges`
- text-search indexing status
  - `status`
- text search over mirrored GitHub discussion data
  - `mentions`
- structural code search over the local Git mirror
  - `ast-grep`

Use `ghr search status` when the question is:

- is the text index present
- is it current or stale
- is an empty `mentions` result trustworthy
- does this repo need a text-index rebuild

Use `ghr search mentions` when the question is:

- where was this phrase mentioned
- which PRs talked about this topic
- did anyone mention something close to this wording

Use `ghr search ast-grep` when the question is:

- where in this repo does this syntax pattern exist
- does this PR contain this structural shape
- which changed files in this PR match this pattern

Use the overlap commands when the question is:

- which PRs touched the same file
- which PRs touched overlapping lines
- what other PRs are similar by code change

Flags for `ghr search mentions`:

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

Recommended usage:

- start with `--mode fts` for normal keyword or phrase search
- use `--mode fuzzy` for approximate wording, split words, or misspellings
- use `--mode regex` when you need exact pattern hunting
- add one or more `--scope` flags whenever you know the kind of object you want
- use `--state all` when closed PRs or issues matter
- use `--author` when you want only one person’s PR text or comments

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

Default human-readable output should make it easy to scan:

- object type and number
- matched field
- score
- excerpt

Examples:

```bash
ghr search status -R openclaw/openclaw
ghr search mentions -R openclaw/openclaw --query "heartbeat watchdog" --mode fts --scope pull_requests --scope issues
ghr search mentions -R openclaw/openclaw --query "watch dog" --mode fuzzy --scope pull_requests
ghr search mentions -R openclaw/openclaw --query "auth.*state" --mode regex --scope pull_requests --state all
ghr search mentions -R openclaw/openclaw --query "greptile" --mode fts --scope pull_request_reviews --scope pull_request_review_comments
ghr search mentions -R openclaw/openclaw --query "acp" --mode fts --scope pull_requests --state all --json resource,matched_field,score
ghr search ast-grep -R openclaw/openclaw --pr 59883 --language typescript --pattern 'ctx.reply($MSG)' --changed-files-only
ghr search ast-grep -R dutifuldev/ghreplica --ref main --language go --pattern 'fmt.Errorf($MSG)' --json resolved_commit_sha,matches
```

Flags for `ghr search ast-grep`:

- exactly one target:
  - `--commit`
  - `--ref`
  - `--pr`
- `--language`
- `--pattern`
- `--path`
- `--changed-files-only`
- `--limit`
- `--json`

Recommended usage:

- pin to `--commit` when you need fully reproducible automation
- use `--pr` for review workflows
- add `--changed-files-only` when you only care about code changed by that PR
- add one or more `--path` filters when you already know the files of interest
- keep `--limit` bounded on large repos

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

For `ghr search mentions`, `--json` is the preferred scripting mode.

Example:

```bash
ghr search mentions \
  -R openclaw/openclaw \
  --query "heartbeat watchdog" \
  --mode fts \
  --scope pull_requests \
  --json resource,matched_field,excerpt,score
```

## Configuration

The CLI should accept:

- `--base-url`
- optional auth configuration later if the hosted mirror requires it

Reasonable defaults:

- `GHR_BASE_URL`
- default local base URL for development
- explicit base URL for hosted environments such as `https://ghreplica.dutiful.dev`

Current hosted default:

- `https://ghreplica.dutiful.dev`

So normal production use usually does not need `--base-url`.

## Search Caveats

`ghr search mentions` searches mirrored text that has already been indexed into `search_documents`.

Use `ghr search status` first when you need to know whether that text index is:

- `missing`
- `building`
- `ready`
- `stale`
- `failed`

That means:

- it does not call GitHub’s live search API
- results reflect what `ghreplica` has already mirrored
- older mirrored repos may need a text-index rebuild before repo-wide text search is complete

The overlap commands depend on `/v1/changes/...` coverage instead.

That means:

- `mentions` depends on text-index coverage
- `status` reports text-index freshness and coverage directly
- `related-prs`, `prs-by-paths`, and `prs-by-ranges` depend on git-change index coverage

If search results look incomplete:

- check `ghr search status -R <owner>/<repo>`
- check `ghr changes repo status -R <owner>/<repo>`
- check `ghr changes pr status -R <owner>/<repo> <number>`
- materialize a specific PR with `ghreplica sync pr <owner>/<repo> <number>`
- rebuild text search with `ghreplica search-index repo <owner>/<repo>` when needed

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
