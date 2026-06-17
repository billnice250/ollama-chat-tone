# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)" && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.buildTime=${build_time}" \
    -o /out/server \
    ./cmd/server


FROM alpine:latest AS production
WORKDIR /app

COPY --from=build /out/server /app/server

VOLUME ["/data"]

ENV ADDR=":12129"

EXPOSE 12129

ENTRYPOINT ["/app/server"]
