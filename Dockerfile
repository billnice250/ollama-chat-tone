# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
ARG VERSION=""

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN set -eu; \
    resolved_version="$VERSION"; \
    if [ -z "$resolved_version" ]; then \
      resolved_version="$(git tag --points-at HEAD | head -n 1 || true)"; \
    fi; \
    if [ -z "$resolved_version" ]; then \
      resolved_version="$(git rev-parse --short=6 HEAD 2>/dev/null || true)"; \
    fi; \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w ${resolved_version:+-X main.version=${resolved_version}}" \
      -o /out/server \
      ./cmd/server


FROM alpine:latest AS production
WORKDIR /app

COPY --from=build /out/server /app/server

VOLUME ["/data"]

ENV ADDR=":12129"

EXPOSE 12129

ENTRYPOINT ["/app/server"]
