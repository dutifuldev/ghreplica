FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/ghreplica ./cmd/ghreplica

FROM node:22-bookworm-slim AS astgrep

WORKDIR /opt/ast-grep

RUN npm init -y >/dev/null 2>&1 \
    && npm install @ast-grep/cli@0.42.1 >/dev/null 2>&1 \
    && cp node_modules/@ast-grep/cli/ast-grep /out-ast-grep

FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN groupadd --system ghreplica \
    && useradd --system --gid ghreplica --home-dir /app ghreplica \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/ghreplica /usr/local/bin/ghreplica
COPY --from=astgrep /out-ast-grep /usr/local/bin/ast-grep
COPY migrations ./migrations

RUN mkdir -p /app/data/git-mirrors && chown -R ghreplica:ghreplica /app/data

USER ghreplica

ENTRYPOINT ["ghreplica"]
