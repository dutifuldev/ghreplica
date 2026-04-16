<p align="center">
  <img src="assets/ghreplica-logo.svg" alt="ghreplica logo" width="180">
</p>

<h1 align="center">ghreplica</h1>

<p align="center">Durable GitHub mirror for tooling.</p>

ghreplica keeps GitHub-shaped repository data in local storage, uses Git as ground truth for change indexing, and serves a stable read API and CLI so downstream tools do not need to build their own webhook handlers, crawlers, and caches.

Current ownership note: this project is currently being developed by Onur Solmaz and is expected to move to another organization once it is stable.

Current public instance:

- API: `https://ghreplica.dutiful.dev`
- CLI: `ghr`

## Agent Skill

This repo also ships a local skill for Codex-style agents at [skills/ghreplica/SKILL.md](skills/ghreplica/SKILL.md).

If you want your agent to immediately understand `ghreplica`, its API surfaces, and the `ghr` CLI, give your agent this exact instruction:

```text
Install the `ghreplica` skill from `/home/bob/repos/ghreplica/skills/ghreplica/SKILL.md`.
Then build the CLI with `cd /home/bob/repos/ghreplica && go build -o /tmp/ghr ./cmd/ghr`.
After that, use the skill for work involving the ghreplica API, the `ghr` CLI, mirrored GitHub reads, git-change inspection, overlap search, mirrored text search, or structural code search.
```

That is enough for your agent if it already knows how to install repo-local skills.

## Why This Exists

Most tools that need GitHub data end up rebuilding the same fragile stack. They poll GitHub, keep partial caches of issues and pull requests, handle webhooks inconsistently, and then discover later that they also need search, change overlap, or indexing status. That usually produces systems that are hard to reason about and even harder to trust.

`ghreplica` exists to centralize that work into one explicit system. It mirrors GitHub-shaped data into canonical storage, builds Git-backed change indexes on top of a local mirror, and exposes a read surface that other tools can depend on. The goal is not to pretend that the mirror is magically complete at all times. The goal is to make freshness, completeness, and derived features operationally honest.

## API Surfaces

`ghreplica` has three read surfaces:

- `/v1/github/...`
  - GitHub-compatible mirrored resources
- `/v1/changes/...`
  - normalized Git-backed change data
- `/v1/search/...`
  - derived search features over mirrored data and the Git mirror

These three surfaces exist for different reasons. `/v1/github/...` is the compatibility surface for GitHub-native resources like repositories, issues, pull requests, reviews, and comments. `/v1/changes/...` is the normalized Git-backed surface for things that GitHub does not present in exactly the form we want for tooling, such as indexed pull request snapshots, commit file lists, compare results, and mirror status. `/v1/search/...` is where the higher-level derived features live, such as overlap search, mirrored text search, and structural code search.

In practice, the current product already covers a meaningful slice of real workflows: repository, issue, pull request, review, and comment reads; repo mirror status; pull request and commit change snapshots; compare for indexed base and head pairs; related PR search by shared paths or overlapping hunks; text search across PRs, issues, comments, reviews, and review comments; and structural code search with `ast-grep`.

## Quick Examples

The fastest way to understand the project is to look at one GitHub-shaped read, one change-index read, and one search query.

From the CLI:

```bash
ghr repo view openclaw/openclaw
ghr pr view -R openclaw/openclaw 66863 --comments
ghr changes pr files -R openclaw/openclaw 59883
ghr search mentions -R openclaw/openclaw --query "acp" --mode fts --scope pull_requests --state all
ghr search ast-grep -R openclaw/openclaw --pr 59883 --language typescript --pattern 'ctx.reply($MSG)' --changed-files-only
```

These examples line up with the three API surfaces. `ghr repo view` and `ghr pr view` are GitHub-shaped reads. `ghr changes pr files` asks for the indexed file list for one PR. `ghr search mentions` searches mirrored discussion text for the term `acp`. `ghr search ast-grep` runs structural code search against the PR head and narrows the search to files changed by that PR.

If you want to hit the API directly, the same pattern looks like this:

```bash
curl -fsS https://ghreplica.dutiful.dev/v1/github/repos/openclaw/openclaw | jq
curl -fsS https://ghreplica.dutiful.dev/v1/changes/repos/openclaw/openclaw/mirror-status | jq
curl -fsS https://ghreplica.dutiful.dev/v1/search/repos/openclaw/openclaw/status | jq
curl -fsS https://ghreplica.dutiful.dev/v1/search/repos/openclaw/openclaw/mentions \
  -H 'Content-Type: application/json' \
  -d '{"query":"acp","mode":"fts","scopes":["pull_requests"],"state":"all","limit":10,"page":1}' | jq
```

The first call asks for a GitHub-compatible repository document. The second asks for `ghreplica`'s own mirror-status view, which is where you can inspect local freshness and completeness signals. The third asks for text-search status, which is useful before trusting an empty text-search result. The last call runs the actual mirrored text search.

## Search

Search in `ghreplica` is intentionally split into three different capabilities because they answer different questions.

Overlap search is for change similarity. Use `ghr search related-prs`, `ghr search prs-by-paths`, or `ghr search prs-by-ranges` when the question is “what other PRs touched the same code?” That search works over indexed pull request changes, not over discussion text.

Text search is for mirrored GitHub discussion content. Use `ghr search status` to check whether the text index is present and fresh enough to trust, and use `ghr search mentions` to search titles, bodies, comments, reviews, and review comments. It supports `fts` for ordinary keyword and phrase search, `fuzzy` for approximate wording, and `regex` for explicit pattern matching. This search does not look at code diffs.

Structural code search is for syntax-aware questions over repository contents. Use `ghr search ast-grep` when the question is “where does this code shape exist?” or “does this PR contain this structural pattern?” Structural search always resolves to one exact commit SHA so the result is reproducible even when the branch or PR moves later.

## Sync Model

`ghreplica` is webhook-first.

Webhooks drive freshness. Full backfills are explicit operator actions. Targeted repairs are preferred over whole-repo recrawls. Mirrors can be partial, and the system should be honest about that.

That means this project is not trying to pretend it has perfect live parity with GitHub at all times. The goal is reliable, inspectable, bounded mirroring. If something is partially indexed, stale, or still being rebuilt, the system should say so rather than silently acting complete.

## Local Development

The local development loop is deliberately simple. Start the database, run migrations, point the service at a Git mirror root, and run the API:

```bash
make db-up
make migrate
export GIT_MIRROR_ROOT=.data/git-mirrors
make serve
```

Once the server is up, these are the most useful manual operations:

```bash
go run ./cmd/ghreplica sync repo dutifuldev/ghreplica
go run ./cmd/ghreplica sync issue openclaw/openclaw 66797
go run ./cmd/ghreplica sync pr openclaw/openclaw 66863
go run ./cmd/ghreplica backfill repo openclaw/openclaw --mode open_only
go run ./cmd/ghreplica search-index repo openclaw/openclaw
go build ./cmd/ghr
```

The sync commands are for targeted ingestion and repair. `sync repo` mirrors the repo-level data we support. `sync issue` and `sync pr` are useful when you want one object and its related discussion right away. `backfill repo` is for bounded repo coverage work. `search-index repo` rebuilds the mirrored text-search corpus for a repo.

If you only want the CLI locally, build it directly:

```bash
cd /home/bob/repos/ghreplica
go build -o /tmp/ghr ./cmd/ghr
```

That gives you a local `ghr` binary without needing to deploy the whole service.

If you want to sanity-check a local instance quickly, these endpoints are usually enough:

- `GET http://127.0.0.1:8080/healthz`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica`
- `GET http://127.0.0.1:8080/v1/changes/repos/dutifuldev/ghreplica/mirror-status`

## Self-Hosting Notes

The current runtime model has a few requirements that matter in practice.

- `ast-grep` must be installed in the runtime image
- the GitHub App private key must be readable by the runtime user
- the mounted git-mirror directory must be owned by the runtime user

These are not theoretical details. We already hit the private-key readability and mirror-directory ownership problems in production. See [GCP Deployment](docs/DEPLOY_GCP.md) for the concrete deployment steps and the exact fixes.

## Local Build And Install

If you want to use the CLI locally without running the full service, build `ghr` from this repo:

```bash
cd /home/bob/repos/ghreplica
go build -o /tmp/ghr ./cmd/ghr
```

You can then run commands like:

```bash
/tmp/ghr repo view openclaw/openclaw
/tmp/ghr search mentions -R openclaw/openclaw --query "acp" --mode fts --scope pull_requests --state all
```

This is the simplest local install path when you only need the client.

## Deployment

If you want to run `ghreplica` yourself, think of deployment as standing up one service plus its supporting state, not as installing a single binary and being done.

At minimum you need:

- a Postgres database
- a writable Git mirror root
- a GitHub App installation for upstream auth
- a webhook endpoint that GitHub can reach
- `ast-grep` in the runtime image if you want structural search

The basic shape is:

1. create and configure the database
2. point `ghreplica` at a writable mirror directory
3. provide GitHub App credentials and webhook secret
4. run migrations
5. start the API
6. point GitHub webhooks at `/webhooks/github`

For the currently supported production setup, use [GCP Deployment](docs/DEPLOY_GCP.md). That document covers the Docker Compose deployment, migrations, TLS, and the runtime permission issues that matter in practice.

## Docs

The deeper design and operational details live in the docs:

- [CLI](docs/CLI.md)
- [Supported Endpoints](docs/SUPPORTED_ENDPOINTS.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Git Ground Truth](docs/GIT_GROUND_TRUTH.md)
- [Local Development](docs/LOCAL_DEVELOPMENT.md)
- [GCP Deployment](docs/DEPLOY_GCP.md)
- [Testing](docs/TESTING.md)
- [Skill](skills/ghreplica/SKILL.md)
