package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppName       string
	Addr          string
	SessionSecret string
	DBPath        string
	OllamaURL     string
	DefaultModel  string

	BasicUser string
	BasicPass string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	AllowedEmails    map[string]bool
}

func Load() Config {
	dotenv(".env")
	return Config{
		AppName:          getenv("APP_NAME", "Ollama Chat"),
		Addr:             getenv("ADDR", ":8080"),
		SessionSecret:    getenv("SESSION_SECRET", "dev-change-me"),
		DBPath:           getenv("DB_PATH", "./app.db"),
		OllamaURL:        getenv("OLLAMA_URL", "http://ollama:11434"),
		DefaultModel:     getenv("DEFAULT_MODEL", "llama3.2"),
		BasicUser:        getenv("BASIC_AUTH_USER", ""),
		BasicPass:        getenv("BASIC_AUTH_PASSWORD", ""),
		OIDCIssuer:       getenv("OIDC_ISSUER", ""),
		OIDCClientID:     getenv("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getenv("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  getenv("OIDC_REDIRECT_URL", "/auth/callback"),
		AllowedEmails:    csvSet(getenv("ALLOWED_EMAILS", "")),
	}
}

func (c Config) AuthMode() string {
	if c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != "" {
		return "oidc"
	}
	if c.BasicUser != "" && c.BasicPass != "" {
		return "local"
	}
	return "none"
}

func dotenv(paths ...string) {
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")

			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}

			key = strings.TrimSpace(key)
			if key == "" || os.Getenv(key) != "" {
				continue
			}

			value = parseEnvValue(strings.TrimSpace(value))
			_ = os.Setenv(key, value)
		}

		_ = file.Close()
	}
}

func (c Config) CookieSecure() bool {
	v, _ := strconv.ParseBool(getenv("COOKIE_SECURE", "false"))
	return v
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseEnvValue(value string) string {
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '"' || quote == '\'') && value[len(value)-1] == quote {
			value = value[1 : len(value)-1]
			if quote == '"' {
				value = strings.NewReplacer(
					`\\`, `\`,
					`\n`, "\n",
					`\r`, "\r",
					`\t`, "\t",
					`\"`, `"`,
				).Replace(value)
			}
			return value
		}
	}

	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return value
}

func csvSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			m[p] = true
		}
	}
	return m
}
