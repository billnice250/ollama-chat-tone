# Ollama Chat Client

Tiny Go web client for Ollama with Basic Auth or OIDC authentication.

## Run

```bash
docker compose up --build
```

Open http://localhost:8080 and login with Basic Auth:

- user: `admin`
- password: `change-me`

Pull a model:

```bash
docker compose exec ollama ollama pull llama3.2
```

## Auth modes

- If `OIDC_ISSUER`, `OIDC_CLIENT_ID`, and `OIDC_CLIENT_SECRET` are set: OIDC mode.
- Else if `BASIC_AUTH_USER` and `BASIC_AUTH_PASSWORD` are set: Basic Auth mode.
- Else: no auth. Do not use no-auth on the internet.

## External Ollama

Set:

```yaml
OLLAMA_URL: "http://host.docker.internal:11434"
```

and remove the `ollama` service.
