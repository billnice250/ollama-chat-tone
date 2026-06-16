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

	// BaseURL is used to build absolute links in emails.
	BaseURL string

	// SMTP settings for transactional email (verification, password reset).
	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string
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
	fileEnv := dotenv(".env")
	secret := getenv(fileEnv, "SESSION_SECRET", "")
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
		AppName:          getenv(fileEnv, "APP_NAME", "Ollama Chat Tone"),
		Addr:             getenv(fileEnv, "ADDR", ":12129"),
		SessionSecret:    getenv(fileEnv, "SESSION_SECRET", secret),
		DBPath:           getenv(fileEnv, "DB_PATH", "./app.db"),
		OllamaURL:        getenv(fileEnv, "OLLAMA_URL", "http://ollama:11434"),
		OllamaTimeout:    durationMinutes(fileEnv, "OLLAMA_TIMEOUT", 5),
		DefaultModel:     getenv(fileEnv, "DEFAULT_MODEL", "llama3.2"),
		OpenBrowser:      boolEnv(fileEnv, "OPEN_BROWSER", false),
		BasicUser:        getenv(fileEnv, "BASIC_AUTH_USER", ""),
		BasicPass:        getenv(fileEnv, "BASIC_AUTH_PASSWORD", ""),
		OIDCIssuer:       getenv(fileEnv, "OIDC_ISSUER", ""),
		OIDCClientID:     getenv(fileEnv, "OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getenv(fileEnv, "OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  getenv(fileEnv, "OIDC_REDIRECT_URL", "/auth/callback"),
		BaseURL:          getenv(fileEnv, "BASE_URL", ""),
		SMTPHost:         getenv(fileEnv, "SMTP_HOST", ""),
		SMTPPort:         getenv(fileEnv, "SMTP_PORT", "587"),
		SMTPUser:         getenv(fileEnv, "SMTP_USER", ""),
		SMTPPass:         getenv(fileEnv, "SMTP_PASS", ""),
		SMTPFrom:         getenv(fileEnv, "SMTP_FROM", ""),
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

func dotenv(paths ...string) map[string]string {
	values := map[string]string{}
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
			if key == "" {
				continue
			}

			values[key] = parseEnvValue(strings.TrimSpace(value))
		}

		_ = file.Close()
	}
	return values
}

func (c Config) CookieSecure() bool {
	return boolEnv(dotenv(".env"), "COOKIE_SECURE", false)
}

func getenv(fileEnv map[string]string, k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if v := fileEnv[k]; v != "" {
		return v
	}
	return def
}

func durationMinutes(fileEnv map[string]string, k string, def int) time.Duration {
	v := strings.TrimSpace(getenv(fileEnv, k, ""))
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

func boolEnv(fileEnv map[string]string, k string, def bool) bool {
	v := strings.TrimSpace(getenv(fileEnv, k, ""))
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
