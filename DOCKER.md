# Docker and Docker Compose Guide

This project ships with both local Docker build instructions and prebuilt Docker Hub images.

## GitHub

- Source: [billnice250/ollama-chat-client](https://github.com/billnice250/ollama-chat-client)
- Releases: [GitHub Releases](https://github.com/billnice250/ollama-chat-client/releases)

## Environment Variables

Create a local env file before running containers:

```bash
cp .env.example .env
```

Starter values:

```env
APP_NAME="Ollama Chat Tone"
ADDR=":8080"
SESSION_SECRET="change-me-to-a-long-random-value"
DB_PATH="./app.db"

OLLAMA_URL="http://localhost:11434"
OLLAMA_TIMEOUT="5"
DEFAULT_MODEL="llama3.2"
OPEN_BROWSER="false"

BASIC_AUTH_USER="admin"
BASIC_AUTH_PASSWORD="change-me"
```

Container notes:

- Set `DB_PATH=/data/app.db` when using the Docker volume examples in this guide.
- For Docker Desktop with Ollama on host, set `OLLAMA_URL=http://host.docker.internal:11434`.
- For an external Ollama server, set `OLLAMA_URL` to that server URL.

## Docker Hub

- Repository: [billnice250/chattone](https://hub.docker.com/r/billnice250/chattone)
- Pull latest:

```bash
docker pull billnice250/chattone:latest
```

- Pull a pinned release tag:

```bash
docker pull billnice250/chattone:<version>
```

Tag guidance:

- `latest`: newest published build
- `<version>`: pinned release tag for reproducible deployments

## Docker (Build and Run Locally)

Build the image from this repository:

```bash
docker build -t ollama-chat-tone:local .
```

Run the container:

```bash
docker run --rm -p 8080:8080 --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data ollama-chat-tone:local
```

Run the Docker Hub image:

```bash
docker run --rm -p 8080:8080 --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data billnice250/chattone:latest
```

If Ollama runs on your Docker Desktop host, set:

```env
OLLAMA_URL="http://host.docker.internal:11434"
```

For Linux Docker hosts, add host gateway mapping:

```bash
docker run --rm -p 8080:8080 --add-host=host.docker.internal:host-gateway --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data ollama-chat-tone:local
```

Open:

```text
http://localhost:8080
```

## Docker Compose

Start app + Ollama services with the included compose file:

```bash
docker compose up --build
```

Use the copyable example directly:

```bash
docker compose -f docker-compose.ollama.example.yml up --build
```

Pull model(s) via the compose helper service:

```bash
docker compose run --rm ollama-models
```

To use an external Ollama server, set `OLLAMA_URL` accordingly and remove (or ignore) the `ollama` service in compose.

## Runtime Config Reload in Containers

Reload most `.env` settings without recreating the container:

```bash
docker kill --signal=HUP <container-name-or-id>
```

With Compose:

```bash
docker compose kill -s HUP ollama-chat-tone
```

This updates runtime config values (for example `APP_NAME`, `OLLAMA_URL`, `OLLAMA_TIMEOUT`, and auth settings), but not code, embedded assets, bind address, or DB connection path.
