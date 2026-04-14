FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/ghreplica ./cmd/ghreplica

FROM alpine:3.22

RUN addgroup -S ghreplica && adduser -S -G ghreplica ghreplica \
    && apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/ghreplica /usr/local/bin/ghreplica
COPY migrations ./migrations

USER ghreplica

ENTRYPOINT ["ghreplica"]
