package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	AppAddr                 string
	DatabaseURL             string
	GitMirrorRoot           string
	GitHubBaseURL           string
	GitHubToken             string
	GitHubAppID             string
	GitHubInstallationID    string
	GitHubAppPrivateKeyPEM  string
	GitHubAppPrivateKeyPath string
	GitHubWebhookSecret     string
}

func Load() Config {
	return Config{
		AppAddr:                 getenvDefault("APP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:             strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GitMirrorRoot:           getenvDefault("GIT_MIRROR_ROOT", ".data/git-mirrors"),
		GitHubBaseURL:           getenvDefault("GITHUB_BASE_URL", "https://api.github.com"),
		GitHubToken:             strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		GitHubAppID:             strings.TrimSpace(os.Getenv("GITHUB_APP_ID")),
		GitHubInstallationID:    strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALLATION_ID")),
		GitHubAppPrivateKeyPEM:  strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM")),
		GitHubAppPrivateKeyPath: strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")),
		GitHubWebhookSecret:     strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")),
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
