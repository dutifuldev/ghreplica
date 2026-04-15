FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/ghreplica ./cmd/ghreplica

FROM node:22-alpine AS astgrep

WORKDIR /opt/ast-grep

RUN npm init -y >/dev/null 2>&1 \
    && npm install @ast-grep/cli@0.42.1 >/dev/null 2>&1 \
    && cp node_modules/@ast-grep/cli/ast-grep /out-ast-grep

FROM alpine:3.22

RUN addgroup -S ghreplica && adduser -S -G ghreplica ghreplica \
    && apk add --no-cache ca-certificates tzdata git

WORKDIR /app

COPY --from=builder /out/ghreplica /usr/local/bin/ghreplica
COPY --from=astgrep /out-ast-grep /usr/local/bin/ast-grep
COPY migrations ./migrations

RUN mkdir -p /app/data/git-mirrors && chown -R ghreplica:ghreplica /app/data

USER ghreplica

ENTRYPOINT ["ghreplica"]
