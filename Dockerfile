# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
ARG VERSION=""

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w ${VERSION:+-X main.version=${VERSION}}" \
    -o /out/server \
    ./cmd/server


FROM alpine:latest AS production
WORKDIR /app

COPY --from=build /out/server /app/server

VOLUME ["/data"]

ENV ADDR=":12129"

EXPOSE 12129

ENTRYPOINT ["/app/server"]
