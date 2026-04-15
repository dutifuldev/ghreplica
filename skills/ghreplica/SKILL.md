---
name: ghreplica
description: Explain ghreplica and teach how to use the ghr CLI for mirrored GitHub reads, git-change inspection, overlap search, and text search.
---

# ghreplica Skill

Use this skill when you need to explain what `ghreplica` is, show how to use the `ghr` CLI, or guide someone through the mirrored read, change, and search surfaces in this repository.

## What ghreplica is

`ghreplica` is a GitHub mirror with three separate read surfaces:

- `/v1/github/...`
  - GitHub-shaped mirrored resources such as repositories, issues, pull requests, reviews, and comments
- `/v1/changes/...`
  - normalized git-backed change truth such as PR snapshots, file lists, commit metadata, compare results, and indexing status
- `/v1/search/...`
  - `ghreplica`-specific search features such as related PRs, path overlap, range overlap, and mirrored text search

The `ghr` CLI is a thin client over those APIs.

## CLI defaults

- `ghr` defaults to `https://ghreplica.dutiful.dev`
- use `-R owner/repo` for repository-scoped commands
- `repo view` is the only command that may also take a positional `OWNER/REPO`
- use `--json` when the output is meant for scripts

## Quick decision guide

Use:

- `repo`, `issue`, or `pr`
  - when you want GitHub-shaped mirrored data
- `changes`
  - when you want git-backed truth, PR snapshots, files, commits, compare results, or indexing status
- `search related-prs`, `search prs-by-paths`, or `search prs-by-ranges`
  - when you want related PRs by changed files or overlapping line ranges
- `search status`
  - when you need to know whether mirrored text search is complete, current, or stale
- `search mentions`
  - when you want to find where a phrase or topic was mentioned in mirrored PRs, issues, comments, reviews, or review comments

## Core GitHub-shaped reads

```bash
ghr repo view openclaw/openclaw
ghr repo status -R openclaw/openclaw
ghr issue list -R openclaw/openclaw --state all
ghr issue view -R openclaw/openclaw 66797 --comments
ghr issue comments -R openclaw/openclaw 66797
ghr pr list -R openclaw/openclaw --state all
ghr pr view -R openclaw/openclaw 66863 --comments
ghr pr reviews -R openclaw/openclaw 66863
ghr pr comments -R openclaw/openclaw 66863
```

## Git change reads

Use these when the question is about git-backed truth instead of GitHub discussion text.

```bash
ghr changes repo status -R openclaw/openclaw
ghr changes pr status -R openclaw/openclaw 59883
ghr changes pr view -R openclaw/openclaw 59883
ghr changes pr files -R openclaw/openclaw 59883
ghr changes commit view -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes commit files -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes compare -R openclaw/openclaw main...5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
```

`changes ... status` is the first place to look when someone asks whether overlap search is complete or stale.

## Overlap and related-PR search

Use overlap search when the question is about changed code, not text discussion.

```bash
ghr search related-prs -R openclaw/openclaw 59883 --mode path_overlap --state all
ghr search related-prs -R openclaw/openclaw 59883 --mode range_overlap --state all
ghr search prs-by-paths -R openclaw/openclaw --path src/acp/control-plane/manager.core.ts --state all
ghr search prs-by-ranges -R openclaw/openclaw --path extensions/telegram/src/fetch.ts --start 24 --end 36 --state all
```

Use this surface for questions like:

- what other PRs touch this file
- what PRs overlap this line range
- which PRs are related by changed code

## Text search with `ghr search mentions`

Check status first when search completeness matters:

```bash
ghr search status -R openclaw/openclaw
```

Use this when the question is:

- is the text index present
- is it current or stale
- is an empty `mentions` result trustworthy
- should we rebuild text search for this repo

Use `ghr search mentions` when the question is about what people wrote.

It searches mirrored text from:

- issue titles and bodies
- pull request titles and bodies
- issue comments
- pull request reviews
- pull request review comments

### Modes

- `fts`
  - standard full-text search for keywords and phrases
- `fuzzy`
  - approximate wording and split-word matches
- `regex`
  - exact pattern hunting

### Useful flags

- `--mode`
- `--scope`
  - `pull_requests`
  - `issues`
  - `issue_comments`
  - `pull_request_reviews`
  - `pull_request_review_comments`
- `--state`
  - `open`
  - `closed`
  - `all`
- `--author`
- `--limit`
- `--page`
- `--json`

### Recommended usage

- start with `fts` for normal topic search
- use `fuzzy` when the wording may be approximate or misspelled
- use `regex` when you need an explicit pattern
- narrow with one or more `--scope` flags whenever possible
- use `--state all` when closed PRs or issues matter
- use `--author` when you want only one person's PR text or comments

### Examples

```bash
ghr search mentions -R openclaw/openclaw --query "heartbeat watchdog" --mode fts --scope pull_requests --scope issues
ghr search mentions -R openclaw/openclaw --query "watch dog" --mode fuzzy --scope pull_requests
ghr search mentions -R openclaw/openclaw --query "auth.*state" --mode regex --scope pull_requests --state all
ghr search mentions -R openclaw/openclaw --query "greptile" --mode fts --scope issue_comments
ghr search mentions -R openclaw/openclaw --query "acp" --mode fts --scope pull_requests --state all
```

### How to interpret results

Each result preserves:

- `resource`
  - `type`
  - `id`
  - `number`
  - `api_url`
  - `html_url`
- `matched_field`
- `excerpt`
- `score`

So the answer is not just “this matched,” but also:

- what object matched
- whether the hit came from `title` or `body`
- a short excerpt showing why it matched

## Structured output

Most commands support `--json`.

Examples:

```bash
ghr changes pr view -R openclaw/openclaw 59883 --json pull_request_number,head_sha,indexed_as,index_freshness
ghr search prs-by-paths -R openclaw/openclaw --path src/acp/control-plane/manager.core.ts --state all --json pull_request_number,score,shared_paths
ghr search mentions -R openclaw/openclaw --query "heartbeat watchdog" --mode fts --scope pull_requests --json resource,matched_field,excerpt,score
```

For text search, `--json` is the preferred output mode for scripts.

## Caveats

There are two different indexing dependencies:

- `search mentions`
  - depends on text-index coverage in `search_documents`
- overlap search and `changes`
  - depend on git-change index coverage under `/v1/changes/...`

That means:

- text search and overlap search can be complete at different times
- a repo may have mirrored PRs but incomplete change-index coverage
- a repo may need a text-index rebuild even when GitHub-shaped reads already work

If results look incomplete:

- check `ghr search status -R <owner>/<repo>`
- check `ghr changes repo status -R <owner>/<repo>`
- check `ghr changes pr status -R <owner>/<repo> <number>`
- materialize a specific PR with `ghreplica sync pr <owner>/<repo> <number>`
- rebuild text search with `ghreplica search-index repo <owner>/<repo>` when needed

## Good explanation style

When using this skill:

- explain which surface is being used
  - `github`, `changes`, or `search`
- give direct `ghr` examples instead of abstract descriptions
- distinguish text search from code-overlap search clearly
- mention indexing coverage when a “no result” answer may just mean incomplete indexing
