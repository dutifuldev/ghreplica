---
name: ghreplica
description: Explain ghreplica and teach how to inspect mirrored GitHub data, git change indexes, and overlap searches with the ghr CLI.
---

# ghreplica Skill

Use this skill when you need to explain what `ghreplica` is or show how to use the `ghr` CLI in this repository.

## What ghreplica is

`ghreplica` has three read surfaces:

- `/v1/github/...` for GitHub-shaped mirrored repository, issue, pull request, review, and comment data
- `/v1/changes/...` for normalized git-backed change truth such as PR snapshots, file lists, commit metadata, and compare results
- `/v1/search/...` for `ghreplica`-specific overlap and related-PR queries

The `ghr` CLI is a thin client over those APIs.

## CLI defaults

- `ghr` defaults to `https://ghreplica.dutiful.dev`
- use `-R owner/repo` for repository-scoped commands
- `repo view` is the only command that may also take a positional `OWNER/REPO`

## Core GitHub-shaped reads

```bash
ghr repo view openclaw/openclaw
ghr repo status -R openclaw/openclaw
ghr issue list -R openclaw/openclaw --state all
ghr issue view -R openclaw/openclaw 66797 --comments
ghr pr list -R openclaw/openclaw --state all
ghr pr view -R openclaw/openclaw 66863 --comments
ghr pr reviews -R openclaw/openclaw 66863
ghr pr comments -R openclaw/openclaw 66863
```

## Git change reads

```bash
ghr changes pr view -R openclaw/openclaw 59883
ghr changes pr files -R openclaw/openclaw 59883
ghr changes commit view -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes commit files -R openclaw/openclaw 5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
ghr changes compare -R openclaw/openclaw main...5a3d3e54d93a03ee6f775d0010d1b1c433b34a23
```

## Overlap and related-PR search

```bash
ghr search related-prs -R openclaw/openclaw 59883 --mode path_overlap --state all
ghr search related-prs -R openclaw/openclaw 59883 --mode range_overlap --state all
ghr search prs-by-paths -R openclaw/openclaw --path src/acp/control-plane/manager.core.ts --state all
ghr search prs-by-ranges -R openclaw/openclaw --path extensions/telegram/src/fetch.ts --start 24 --end 36 --state all
```

## Structured output

Most commands support `--json`.

Examples:

```bash
ghr changes pr view -R openclaw/openclaw 59883 --json pull_request_number,head_sha,indexed_as,index_freshness
ghr search prs-by-paths -R openclaw/openclaw --path src/acp/control-plane/manager.core.ts --state all --json pull_request_number,score,shared_paths
```

## Important caveat

Search quality depends on indexing coverage.

- If a PR has not been indexed into `/v1/changes/...`, search cannot return it.
- Use `ghreplica sync pr <owner>/<repo> <number>` when you need to materialize a specific PR into the change index.
