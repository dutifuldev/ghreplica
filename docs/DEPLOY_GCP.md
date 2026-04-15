# GCP Deployment

This document describes the first shared staging deployment for `ghreplica` on GCP.

## Target Shape

- VM: `e2-small`
- Region: `europe-west1`
- Hostname: `ghreplica.dutiful.dev`
- Reverse proxy: `caddy`
- App: `ghreplica`
- Database: existing Cloud SQL PostgreSQL instance `horse-pg`

The intended traffic path is:

`Internet -> static IP -> Caddy -> ghreplica -> cloud-sql-proxy -> Cloud SQL`

Only `80` and `443` should be exposed publicly.

## Files

Deployment artifacts live under [deploy/gcp](../deploy/gcp):

- [deploy/gcp/docker-compose.yml](../deploy/gcp/docker-compose.yml)
- [deploy/gcp/Caddyfile](../deploy/gcp/Caddyfile)
- [deploy/gcp/ghreplica.env.example](../deploy/gcp/ghreplica.env.example)
- [deploy/gcp/github-app.env.example](../deploy/gcp/github-app.env.example)

The app image is built from the repo [Dockerfile](../Dockerfile).

## Required Inputs

Before deployment, confirm:

- project billing is enabled for `dutiful-20260414`
- `compute.googleapis.com` is enabled on `dutiful-20260414`
- GCP project: `dutiful-20260414`
- VM zone: any `europe-west1` zone with available `e2-small` capacity
- current VM zone: `europe-west1-b`
- Cloud SQL instance: `horse-pg`
- Cloud SQL connection name, in `project:region:instance` form
- if the database is reused from another project, that project must be used in the connection name, for example `horse-460221:europe-west1:horse-pg`
- Service account on the VM: `bob-gcloud@dutiful-20260414.iam.gserviceaccount.com`

The VM service account must already be able to connect to the `ghreplica` database and must not have access to the Horse databases.

## 1. Create The VM

If `gcloud` is not installed locally, run it through the official Cloud SDK container.

Create a static IP:

```bash
gcloud compute addresses create ghreplica-ip \
  --project=dutiful-20260414 \
  --region=europe-west1
```

Create the VM:

```bash
gcloud compute instances create ghreplica \
  --project=dutiful-20260414 \
  --zone=europe-west1-b \
  --machine-type=e2-small \
  --address=ghreplica-ip \
  --service-account=bob-gcloud@dutiful-20260414.iam.gserviceaccount.com \
  --scopes=https://www.googleapis.com/auth/cloud-platform \
  --tags=ghreplica-http,ghreplica-https \
  --image-family=ubuntu-2404-lts-amd64 \
  --image-project=ubuntu-os-cloud \
  --boot-disk-size=30GB
```

Create firewall rules:

```bash
gcloud compute firewall-rules create ghreplica-allow-http \
  --project=dutiful-20260414 \
  --direction=INGRESS \
  --allow=tcp:80 \
  --target-tags=ghreplica-http

gcloud compute firewall-rules create ghreplica-allow-https \
  --project=dutiful-20260414 \
  --direction=INGRESS \
  --allow=tcp:443 \
  --target-tags=ghreplica-https
```

## 2. Point DNS

In Cloudflare, create:

- Type: `A`
- Name: `ghreplica`
- Value: the reserved static IP on the VM

That should resolve:

- `ghreplica.dutiful.dev -> <vm-static-ip>`

## 3. Prepare The Server

SSH to the VM and install Docker plus the Compose plugin.

Then clone the repo:

```bash
git clone https://github.com/dutifuldev/ghreplica.git
cd ghreplica
```

Create the env file:

```bash
cp deploy/gcp/ghreplica.env.example deploy/gcp/ghreplica.env
```

Populate:

- `CLOUD_SQL_INSTANCE_CONNECTION_NAME`
- `DB_NAME=ghreplica`
- `DB_IAM_USER_URLENCODED=bob-gcloud%40dutiful-20260414.iam`
- `GITHUB_WEBHOOK_SECRET`
- `GITHUB_APP_ID`
- `GITHUB_APP_INSTALLATION_ID`
- `GITHUB_APP_PRIVATE_KEY_PATH`

Recommended GitHub App private key path on the VM:

- `/home/bob/ghreplica/secrets/github-app.private-key.pem`

Create the secrets directory on the VM if needed:

```bash
mkdir -p ~/ghreplica/secrets
chmod 700 ~/ghreplica/secrets
```

Important runtime requirements:

- the GitHub App private key must be readable by the container user
- the mounted git-mirror data directory must be owned by the container user

In production we hit both of these after switching to a Debian-based image that runs as the `ghreplica` user.

If the private key is only readable by the host account, GitHub-authenticated sync and structural search can fail with `permission denied`.

If the mounted mirror root is owned by a different UID from the container user, Git can reject mirrored repositories with a `dubious ownership` error.

The simplest working fix on the VM is:

```bash
chmod 755 ~/ghreplica/secrets
chmod 644 ~/ghreplica/secrets/github-app.private-key.pem
sudo chown -R 999:999 ~/ghreplica/data
```

That matches the current container user in the shipped image.

## 4. Run Migrations And Start The Stack

From the repo root on the VM:

```bash
docker compose --env-file deploy/gcp/ghreplica.env -f deploy/gcp/docker-compose.yml --profile ops run --rm ghreplica-migrate
docker compose --env-file deploy/gcp/ghreplica.env -f deploy/gcp/docker-compose.yml up -d --build
```

If Docker on the VM requires root, use:

```bash
sudo docker compose --env-file deploy/gcp/ghreplica.env -f deploy/gcp/docker-compose.yml --profile ops run --rm ghreplica-migrate
sudo docker compose --env-file deploy/gcp/ghreplica.env -f deploy/gcp/docker-compose.yml up -d --build
```

## 5. Verify

Once DNS and TLS settle, these should succeed:

```bash
curl https://ghreplica.dutiful.dev/healthz
curl https://ghreplica.dutiful.dev/readyz
curl https://ghreplica.dutiful.dev/v1/github/repos/dutifuldev/ghreplica
```

If you want to verify the two permission-sensitive runtime dependencies inside the container, these should also work:

```bash
sudo docker exec gcp-ghreplica-1 sh -lc 'test -r /home/bob/ghreplica/secrets/github-app.private-key.pem && echo key-ok'
sudo docker exec gcp-ghreplica-1 sh -lc 'git -C /app/data/git-mirrors/openclaw/openclaw.git show-ref | head -5'
```

## 6. Add The GitHub Webhook

Set the repository webhook URL to:

```text
https://ghreplica.dutiful.dev/webhooks/github
```

The configured secret must exactly match `GITHUB_WEBHOOK_SECRET`.

## Notes

- The app itself is not published directly; only `caddy` exposes ports.
- `cloud-sql-proxy` runs with IAM auth, using the VM service account.
- Prefer GitHub App auth over `GITHUB_TOKEN` for this deployment.
- This is a staging-first deployment shape. Add user-facing auth before opening the API broadly to other people.
