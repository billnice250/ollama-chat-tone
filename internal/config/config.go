package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppName       string
	Addr          string
	SessionSecret string
	DBPath        string
	OllamaURL     string
	OllamaTimeout time.Duration
	DefaultModel  string
	OpenBrowser   bool

	BasicUser string
	BasicPass string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	AllowedEmails    map[string]bool
}

func GenerateSessionSecret() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err == nil {
		return hex.EncodeToString(b)
	}
	log.Panicf("failed to generate session secret: %v", err)
	os.Exit(1)
	return ""
}

func Load() Config {
	dotenv(".env")
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Println("WARNING: SESSION_SECRET not set, generating random secret. This will invalidate all sessions on restart.")
		secret = GenerateSessionSecret()
		os.Setenv("SESSION_SECRET", secret)
	} else if len(secret) < 32 {
		log.Println("WARNING: SESSION_SECRET is less than 32 characters, which may be insecure.")
	} else {
		log.Println("Using provided SESSION_SECRET.")
	}
	return Config{
		AppName:          getenv("APP_NAME", "Ollama Chat Tone"),
		Addr:             getenv("ADDR", ":12129"),
		SessionSecret:    getenv("SESSION_SECRET", secret),
		DBPath:           getenv("DB_PATH", "./app.db"),
		OllamaURL:        getenv("OLLAMA_URL", "http://ollama:11434"),
		OllamaTimeout:    durationMinutes("OLLAMA_TIMEOUT", 5),
		DefaultModel:     getenv("DEFAULT_MODEL", "llama3.2"),
		OpenBrowser:      boolEnv("OPEN_BROWSER", false),
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

func (c Config) Validate() error {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("ADDR must be in host:port form: %w", err)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("ADDR port must be numeric: %w", err)
	}
	return nil
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
	return boolEnv("COOKIE_SECURE", false)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durationMinutes(k string, def int) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return time.Duration(def) * time.Minute
	}
	if minutes, err := strconv.Atoi(v); err == nil {
		return time.Duration(minutes) * time.Minute
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return time.Duration(def) * time.Minute
	}
	return d
}

func boolEnv(k string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return parsed
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
