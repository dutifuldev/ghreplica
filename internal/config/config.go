package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	AppAddr       string
	DatabaseURL   string
	GitHubBaseURL string
	GitHubToken   string
}

func Load() Config {
	return Config{
		AppAddr:       getenvDefault("APP_ADDR", ":8080"),
		DatabaseURL:   strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GitHubBaseURL: getenvDefault("GITHUB_BASE_URL", "https://api.github.com"),
		GitHubToken:   strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
	}
}

func (c Config) ValidateDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	return nil
}

func ParseFullName(fullName string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("repository must be in owner/repo form")
	}

	return parts[0], parts[1], nil
}

func getenvDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}
