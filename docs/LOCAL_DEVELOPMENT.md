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
export DATABASE_URL='postgres://ghreplica:ghreplica@127.0.0.1:54329/ghreplica?sslmode=disable'
export GIT_MIRROR_ROOT='.data/git-mirrors'
export GITHUB_TOKEN="$(gh auth token)"
export GITHUB_WEBHOOK_SECRET='replace-this-with-a-long-random-secret'
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

The current webhook path is intentionally thin:

- `POST /webhooks/github`
- validates `X-Hub-Signature-256`
- stores the raw delivery in `webhook_deliveries`
- enqueues a repository refresh job
- the in-process worker refreshes the repository through the existing GitHub poller

This is enough for controlled local testing even though it is not yet a full event projector.

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

Once configured, GitHub deliveries will hit the local API, enqueue refresh work, and the local worker will refresh that repository's mirrored state.

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

- webhook deliveries are persisted, but only a small amount of event-specific data is interpreted directly
- repository refresh still depends on the GitHub REST API
- GitHub App installation-token auth is supported, but app registration and install management still happen outside `ghreplica`

For local development, that is the correct tradeoff: simple, reproducible, and close to the production data model without overbuilding the ingestion path.
