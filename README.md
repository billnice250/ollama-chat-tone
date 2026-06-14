# Ollama Chat Client

Self-contained Go web client for Ollama with local login, optional OIDC/OAuth login, streaming responses, per-user conversations, and embedded static assets.

## Configuration

Create a local `.env` from the example:

```bash
cp .env.example .env
```

Common settings:

```env
APP_NAME="Ollama Chat"
ADDR=":8080"
SESSION_SECRET="change-me-to-a-long-random-value"
DB_PATH="./app.db"

OLLAMA_URL="http://localhost:11434"
OLLAMA_TIMEOUT="5"
DEFAULT_MODEL="llama3.2"

BASIC_AUTH_USER="admin"
BASIC_AUTH_PASSWORD="change-me"
```

`OLLAMA_TIMEOUT` defaults to `5` minutes. It also accepts Go duration values such as `30s`, `5m`, or `1h`.

`ADDR=":0"` is supported. The app binds a free port and logs the actual URL.

## Run A Release Binary

Download the matching archive from GitHub Releases, unpack it, create `.env`, then run the binary:

```bash
cp .env.example .env
./ollama-chat-client
```

Windows:

```powershell
copy .env.example .env
.\ollama-chat-client.exe
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

- `ollama-chat-client_<version>_darwin_amd64.tar.gz`
- `ollama-chat-client_<version>_darwin_arm64.tar.gz`
- `ollama-chat-client_<version>_linux_amd64.tar.gz`
- `ollama-chat-client_<version>_linux_arm64.tar.gz`
- `ollama-chat-client_<version>_windows_amd64.zip`
- `ollama-chat-client_<version>_windows_arm64.zip`
- `checksums.txt`

Set a release version explicitly:

```bash
make release VERSION=v1.0.0
```

## Docker

Build and run the app container:

```bash
docker build -t ollama-chat-client:local .
docker run --rm -p 8080:8080 --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-data:/data ollama-chat-client:local
```

If Ollama is running on the host machine from Docker Desktop, set:

```env
OLLAMA_URL="http://host.docker.internal:11434"
```

For Linux Docker hosts, use the host gateway:

```bash
docker run --rm -p 8080:8080 --add-host=host.docker.internal:host-gateway --env-file .env -e DB_PATH=/data/app.db -v ollama-chat-data:/data ollama-chat-client:local
```

## Docker Compose

Run the app with an Ollama service:

```bash
docker compose up --build
```

The included [compose.yml](compose.yml) uses the official Ollama image:

```yaml
services:
  ollama-chat:
    build: .
    image: ollama-chat-client:local
    ports:
      - "8080:8080"
    environment:
      APP_NAME: "Ollama Chat"
      ADDR: ":8080"
      DB_PATH: "/data/app.db"
      OLLAMA_URL: "http://ollama:11434"
      OLLAMA_TIMEOUT: "5"
      DEFAULT_MODEL: "llama3.2"
      BASIC_AUTH_USER: "admin"
      BASIC_AUTH_PASSWORD: "change-me"
    volumes:
      - app-data:/data
    depends_on:
      - ollama

  ollama:
    image: ollama/ollama:latest
    ports:
      - "11434:11434"
    volumes:
      - ollama-data:/root/.ollama

volumes:
  app-data:
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
docker compose exec ollama ollama pull llama3.2
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
ALLOWED_EMAILS="you@example.com"
```

When OIDC settings are available, the login page shows an OAuth button. If local auth is also configured, users can choose either local login or OAuth.
