# Ollama Chat Tone

Self-contained Go web client for Ollama with local login, optional OIDC/OAuth login, streaming responses, per-user conversations, and embedded static assets.

## Configuration

Create a local `.env` from the example:

```bash
cp .env.example .env
```

Common settings:

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

`OLLAMA_TIMEOUT` defaults to `5` minutes. It also accepts Go duration values such as `30s`, `5m`, or `1h`. Set it to `0` to remove the client-side deadline for very long Ollama generations.

`ADDR=":0"` is supported. The app binds a free port and logs the actual URL.

If the configured address is already in use, the app assumes another instance is already running, opens that URL in your browser, and exits cleanly.

## Reload Configuration

You can reload most `.env` settings without restarting the app. This does not reload a newly built binary or new embedded web assets; deploys still need the container or process to be restarted/recreated. Use the sidebar `Reload config` button, or call the API:

```bash
curl -X POST http://localhost:8080/api/config/reload
```

When running in Docker, send `SIGHUP` to the running process:

```bash
docker kill --signal=HUP <container-name-or-id>
```

With Docker Compose:

```bash
docker compose kill -s HUP ollama-chat-tone
```

Reload updates runtime settings such as `APP_NAME`, `OLLAMA_URL`, `OLLAMA_TIMEOUT`, `DEFAULT_MODEL`, and auth settings. Code changes, embedded static files, `ADDR`, and `DB_PATH` require a restart because the executable, listener, and database connection are already open. The reload control lives on the admin page with the Ollama settings.

## Run A Release Binary

Download the matching archive from GitHub Releases, unpack it, create `.env`, then run the binary:

```bash
cp .env.example .env
./ollama-chat-tone
```

Windows:

```powershell
copy .env.example .env
.\ollama-chat-tone.exe
```

The binary is self-contained: the web UI and templates are embedded in the executable. You only need the executable and a `.env` file.

## Build Release Archives

Build all GitHub release artifacts:

```bash
make
```

Equivalent explicit target:

```bash
make all
```

Or:

```bash
make release
```

Build one platform group:

```bash
make build-mac
make build-linux
make build-windows
```

Artifacts are written to `dist/`:

- `ollama-chat-tone_<version>_darwin_amd64.tar.gz`
- `ollama-chat-tone_<version>_darwin_arm64.tar.gz`
- `ollama-chat-tone_<version>_linux_amd64.tar.gz`
- `ollama-chat-tone_<version>_linux_arm64.tar.gz`
- `ollama-chat-tone_<version>_windows_amd64.zip`
- `ollama-chat-tone_<version>_windows_arm64.zip`
- `checksums.txt`

macOS archives include both the CLI binary and `Ollama Chat Tone.app`. When the `.app` is launched from Finder, logs are written to:

```text
~/Library/Logs/Ollama Chat Tone/ollama-chat-tone.log
```

The macOS `.app` opens the app URL in your default browser automatically.

Windows archives include `ollama-chat-tone.exe`, `logo.ico`, and `run-with-logs.cmd`. Use `run-with-logs.cmd` when launching by double-click if you want logs written to:

```text
%LOCALAPPDATA%\Ollama Chat Tone\Logs\ollama-chat-tone.log
```

`run-with-logs.cmd` opens the app URL in your default browser automatically. For direct CLI or server use, set `OPEN_BROWSER=true` in `.env` if you want the app to open the browser after startup.

Set a release version explicitly:

```bash
make release VERSION=v1.0.0
```

GitHub Releases are built by `.github/workflows/release.yml`. When a release is published, the workflow builds the archives above and uploads them as release assets. The workflow can also be run manually from GitHub Actions.

## Docker

For a dedicated container guide (Docker Hub, Docker run, and Docker Compose), see [DOCKER.md](DOCKER.md).

### Docker Hub Overview

Prebuilt images are published to Docker Hub:

- Repository: [billnice250/chattone](https://hub.docker.com/r/billnice250/chattone)
- Pulls:
  - `docker pull billnice250/chattone:latest`
  - `docker pull billnice250/chattone:<version>`

Quick run example:

```bash
docker run --rm -p 8080:8080 --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data billnice250/chattone:latest
```

Tag guidance:

- `latest`: newest published build.
- `<version>`: pinned release tag for reproducible deployments.

Build and run the app container:

```bash
docker build -t ollama-chat-tone:local .
docker run --rm -p 8080:8080 --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data ollama-chat-tone:local
```

Image builds set the app-page version from Go VCS metadata automatically. The Docker build context must include `.git`; this repo does not ignore `.git` in [.dockerignore](.dockerignore). The displayed version is the last 7 characters of the commit hash, plus `-dirty` when Go reports local changes. If VCS metadata is unavailable, Makefile and Docker builds inject a UTC build timestamp instead.

If Ollama is running on the host machine from Docker Desktop, set:

```env
OLLAMA_URL="http://host.docker.internal:11434"
```

For Linux Docker hosts, use the host gateway:

```bash
docker run --rm -p 8080:8080 --add-host=host.docker.internal:host-gateway --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-tone-data:/data ollama-chat-tone:local
```

## Docker Compose

Run the app with an Ollama service:

```bash
docker compose up --build
```

The included [compose.yml](compose.yml) uses the official Ollama image:

```yaml
name: ollama-chat-tone

services:
  ollama-chat-tone:
    build: .
    ports:
      - "8080:8080"
    environment:
      APP_NAME: "Ollama Chat Tone"
      ADDR: ":8080"
      DB_PATH: "/data/app.db"
      OLLAMA_URL: "http://ollama:11434"
      OLLAMA_TIMEOUT: "5"
      DEFAULT_MODEL: "llama3.2"
      BASIC_AUTH_USER: "admin"
      BASIC_AUTH_PASSWORD: "change-me"
    volumes:
      - ollama-chat-tone-data:/data
    depends_on:
      ollama-models:
        condition: service_completed_successfully
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: 10s
    stop_grace_period: 25s
    restart: unless-stopped

  ollama:
    image: ollama/ollama:latest
    ports:
      - "11434:11434"
    volumes:
      - ollama-data:/root/.ollama
    restart: unless-stopped

  ollama-models:
    image: ollama/ollama:latest
    environment:
      OLLAMA_HOST: "http://ollama:11434"
    command: >
      sh -c "until ollama list >/dev/null 2>&1; do sleep 2; done;
      ollama pull llama3.2"
    depends_on:
      - ollama
    restart: "no"

volumes:
  ollama-chat-tone-data:
  ollama-data:
```

The same copyable example is available as [docker-compose.ollama.example.yml](docker-compose.ollama.example.yml):

```bash
docker compose -f docker-compose.ollama.example.yml up --build
```

Open:

```text
http://localhost:8080
```

Pull a model into the Compose Ollama service:

```bash
docker compose run --rm ollama-models
```

To use an external Ollama instead, set `OLLAMA_URL` to that server and remove or ignore the `ollama` service from `compose.yml`.

## Auth

No auth is enabled only when neither local auth nor OIDC is configured. Do not expose no-auth mode on the internet.

Local auth:

```env
BASIC_AUTH_USER="admin"
BASIC_AUTH_PASSWORD="change-me"
```

The configured local user is created as admin. Other users can register and wait for admin approval.

OIDC/OAuth:

```env
OIDC_ISSUER="https://accounts.google.com"
OIDC_CLIENT_ID="your-client-id"
OIDC_CLIENT_SECRET="your-client-secret"
OIDC_REDIRECT_URL="/auth/callback"
```

When OIDC settings are available, the login page shows an OAuth button. If local auth is also configured, users can choose either local login or OAuth. New OAuth users are registered as pending users until an admin approves them.
