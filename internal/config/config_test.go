package config

import "testing"

func TestValidateRejectsInvalidLogLevel(t *testing.T) {
	cfg := Config{
		Addr:     ":8080",
		LogLevel: "verbose",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid LOG_LEVEL")
	}
}

func TestValidateAcceptsSupportedLogLevel(t *testing.T) {
	cfg := Config{
		Addr:     ":8080",
		LogLevel: "debug",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}
