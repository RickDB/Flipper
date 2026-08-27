FROM golang:1.25-alpine AS build

ARG VERSION

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X github.com/RickDB/Flipper/internal/version.Value=${VERSION}" \
    -o /flipper \
    ./cmd/flipper


FROM alpine:3.22

ARG VERSION

LABEL org.opencontainers.image.title="Flipper" \
      org.opencontainers.image.source="https://github.com/RickDB/Flipper" \
      org.opencontainers.image.description="Self-hosted web frontend that forwards Spotweb releases to SABnzbd" \
      org.opencontainers.image.version="${VERSION}"

RUN apk add --no-cache \
    bash \
    curl \
    ca-certificates \
    && addgroup -S flipper \
    && adduser -S -G flipper flipper

WORKDIR /app

COPY --from=build /flipper /usr/local/bin/flipper

RUN mkdir -p /app/data \
    && chown -R flipper:flipper /app

USER flipper

EXPOSE 19012

VOLUME ["/app/data"]

ENTRYPOINT ["/usr/local/bin/flipper"]
