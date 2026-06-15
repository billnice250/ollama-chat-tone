# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
ARG VERSION=""

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN set -eu; \
    resolved_version="$VERSION"; \
    head_sha=""; \
    if [ -f .git/HEAD ]; then \
      head_ref="$(tr -d '\r\n' < .git/HEAD)"; \
      case "$head_ref" in \
        ref:\ *) \
          ref_path="${head_ref#ref: }"; \
          if [ -f ".git/$ref_path" ]; then \
            head_sha="$(tr -d '\r\n' < ".git/$ref_path")"; \
          elif [ -f .git/packed-refs ]; then \
            head_sha="$(awk -v ref="$ref_path" '$2==ref { print $1; exit }' .git/packed-refs || true)"; \
          fi ;; \
        *) head_sha="$head_ref" ;; \
      esac; \
    fi; \
    if [ -z "$resolved_version" ]; then \
      resolved_version="$( \
        { grep -Rsl "^$head_sha$" .git/refs/tags 2>/dev/null || true; } \
        | sed 's#^.git/refs/tags/##' \
        | sort \
        | head -n 1 \
      )"; \
    fi; \
    if [ -z "$resolved_version" ] && [ -n "$head_sha" ] && [ -f .git/packed-refs ]; then \
      resolved_version="$(awk -v sha="$head_sha" '$1==sha && $2 ~ /^refs\\/tags\\// { sub(/^refs\\/tags\\//, "", $2); print $2; exit }' .git/packed-refs || true)"; \
    fi; \
    if [ -z "$resolved_version" ]; then \
      resolved_version="$(printf '%s' "$head_sha" | cut -c1-6)"; \
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
