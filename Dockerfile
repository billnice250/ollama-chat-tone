# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)" && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.buildTime=${build_time}" \
    -o /out/server \
    ./cmd/server

RUN echo "embedded Go build metadata:" && \
    go version -m /out/server && \
    echo "embedded VCS metadata:" && \
    go version -m /out/server | grep -E 'build[[:space:]]+vcs' || \
    echo "WARNING: no embedded VCS metadata found; check that .git is included in the Docker build context"


FROM alpine:latest AS production
WORKDIR /app

COPY --from=build /out/server /app/server

VOLUME ["/data"]

ENV ADDR=":8080"

EXPOSE 8080

ENTRYPOINT ["/app/server"]
