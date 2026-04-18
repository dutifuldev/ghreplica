# Local Development

`ghreplica` can run locally in the same one-machine shape used in `verge`:

- local Postgres on `127.0.0.1:54329`
- API bound to `127.0.0.1:8080`
- optional `ngrok` tunnel for GitHub webhook delivery

## What You Need

- Go 1.25+
- Docker and Docker Compose, or another local Postgres instance
- `ngrok` if you want live GitHub webhook delivery
- a GitHub repository you control, such as `dutifuldev/ghreplica`

## Step 1: Create Environment Variables

Copy `.env.example` into your shell environment:

```bash
export APP_ADDR=127.0.0.1:8080
export DB_DIALER=postgres
export DATABASE_URL='postgres://ghreplica:ghreplica@127.0.0.1:54329/ghreplica?sslmode=disable'
export GIT_MIRROR_ROOT='.data/git-mirrors'
export GITHUB_TOKEN="$(gh auth token)"
export GITHUB_WEBHOOK_SECRET='replace-this-with-a-long-random-secret'
export DB_CONTROL_MAX_OPEN_CONNS=8
export DB_CONTROL_MAX_IDLE_CONNS=4
export DB_QUEUE_MAX_OPEN_CONNS=4
export DB_QUEUE_MAX_IDLE_CONNS=4
export DB_SYNC_MAX_OPEN_CONNS=8
export DB_SYNC_MAX_IDLE_CONNS=4
export WEBHOOK_JOB_QUEUE_CONCURRENCY=1
export WEBHOOK_JOB_TIMEOUT=30s
export WEBHOOK_JOB_MAX_ATTEMPTS=8
```

`APP_ADDR` defaults to `127.0.0.1:8080`, so the API stays local unless you override it.
`GIT_MIRROR_ROOT` defaults to `.data/git-mirrors`, which is writable in a normal host checkout and ignored by git.

If you want to use a GitHub App instead of a PAT, set:

```bash
export GITHUB_APP_ID='123456'
export GITHUB_APP_INSTALLATION_ID='7890123'
export GITHUB_APP_PRIVATE_KEY_PATH='/absolute/path/to/github-app.pem'
unset GITHUB_TOKEN
```

## Step 2: Start Postgres

The repo includes a local Compose file under [infra/local/docker-compose.yml](../infra/local/docker-compose.yml).

```bash
make db-up
make migrate
```

That brings up Postgres on:

```text
postgres://ghreplica:ghreplica@127.0.0.1:54329/ghreplica?sslmode=disable
```

## Step 3: Bootstrap A Repository

Pull the initial snapshot for a repo you want to mirror:

```bash
make sync REPO=dutifuldev/ghreplica
```

You can repeat that for any other test repo that your token can read:

```bash
make sync REPO=dutifulbob/some-test-repo
```

## Step 4: Start The API

```bash
make serve
```

Important local endpoints:

- `GET http://127.0.0.1:8080/healthz`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/issues?state=all`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/issues/1`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/issues/1/comments`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/pulls?state=all`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/pulls/1`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/pulls/1/reviews`
- `GET http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica/pulls/1/comments`
- `GET http://127.0.0.1:8080/readyz`
- `GET http://127.0.0.1:8080/metrics`

If you want to enqueue a manual repair crawl instead of doing a direct sync:

```bash
make refresh REPO=dutifuldev/ghreplica
```

## Step 5: Enable Live GitHub Webhook Delivery

The current webhook path now matches production more closely:

- `POST /webhooks/github`
- validates `X-Hub-Signature-256`
- stores the raw delivery in `webhook_deliveries`
- enqueues a background webhook-processing job
- the background worker projects supported events into the canonical GitHub-shaped tables and enqueues targeted pull refresh work where needed

This keeps the request path small while still exercising the real projection flow locally.

Start a tunnel:

```bash
ngrok http 127.0.0.1:8080
```

Suppose `ngrok` gives you:

```text
https://example-id.ngrok-free.app
```

Configure the GitHub repository webhook to:

```text
https://example-id.ngrok-free.app/webhooks/github
```

Recommended settings:

- content type: `application/json`
- secret: the exact `GITHUB_WEBHOOK_SECRET` value
- events: `ping`, `repository`, `issues`, `pull_request`

Once configured, GitHub deliveries will hit the local API, be accepted quickly, and then be processed asynchronously into the local mirror.

The background webhook worker and the split serve-time database pools are now controlled through the normal runtime config:

- `DB_DIALER`
- `DB_CONTROL_MAX_OPEN_CONNS`
- `DB_CONTROL_MAX_IDLE_CONNS`
- `DB_SYNC_MAX_OPEN_CONNS`
- `DB_SYNC_MAX_IDLE_CONNS`
- `WEBHOOK_JOB_QUEUE_CONCURRENCY`
- `WEBHOOK_JOB_TIMEOUT`
- `WEBHOOK_JOB_MAX_ATTEMPTS`

The defaults above match the current production baseline and are a good local starting point too. The control pool protects the HTTP path and River, while the sync pool is reserved for inventory scans, backfill, and heavy indexing.

## Recommended First Test Flow

1. `make db-up`
2. `make migrate`
3. export the variables from `.env.example`
4. `make sync REPO=dutifuldev/ghreplica`
5. `make serve`
6. `curl http://127.0.0.1:8080/healthz`
7. `curl http://127.0.0.1:8080/v1/github/repos/dutifuldev/ghreplica`
8. start `ngrok`
9. point a controlled repo webhook at `/webhooks/github`
10. trigger a `ping` or open a test issue or PR

## Limits Of The Current Local Webhook Path

- local `serve` now expects PostgreSQL because webhook background jobs use River
- deep pull-request refresh still depends on the existing targeted refresh and backfill machinery after webhook projection
- GitHub App installation-token auth is supported, but app registration and install management still happen outside `ghreplica`

For local development, that is the correct tradeoff: the webhook boundary now behaves like production, while the heavier repo-sync policy remains the same custom worker logic.
