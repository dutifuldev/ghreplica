package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppAddr                 string
	DatabaseURL             string
	GitMirrorRoot           string
	GitIndexTimeout         time.Duration
	ASTGrepTimeout          time.Duration
	ASTGrepBin              string
	GitHubBaseURL           string
	GitHubToken             string
	GitHubAppID             string
	GitHubInstallationID    string
	GitHubAppPrivateKeyPEM  string
	GitHubAppPrivateKeyPath string
	GitHubWebhookSecret     string
	ChangeSyncPollInterval  time.Duration
	WebhookFetchDebounce    time.Duration
	RepoMinFetchInterval    time.Duration
	RepoLeaseTTL            time.Duration
	OpenPRBackfillInterval  time.Duration
	RepoBackfillMaxRuntime  time.Duration
	RepoBackfillMaxPRs      int
}

func Load() Config {
	return Config{
		AppAddr:                 getenvDefault("APP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:             strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GitMirrorRoot:           getenvDefault("GIT_MIRROR_ROOT", ".data/git-mirrors"),
		GitIndexTimeout:         durationDefault("GIT_INDEX_TIMEOUT", 5*time.Minute),
		ASTGrepTimeout:          durationDefault("AST_GREP_TIMEOUT", time.Minute),
		ASTGrepBin:              getenvDefault("AST_GREP_BIN", "ast-grep"),
		GitHubBaseURL:           getenvDefault("GITHUB_BASE_URL", "https://api.github.com"),
		GitHubToken:             strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		GitHubAppID:             strings.TrimSpace(os.Getenv("GITHUB_APP_ID")),
		GitHubInstallationID:    strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALLATION_ID")),
		GitHubAppPrivateKeyPEM:  strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM")),
		GitHubAppPrivateKeyPath: strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")),
		GitHubWebhookSecret:     strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")),
		ChangeSyncPollInterval:  durationDefault("CHANGE_SYNC_POLL_INTERVAL", 5*time.Second),
		WebhookFetchDebounce:    durationDefault("WEBHOOK_FETCH_DEBOUNCE", 3*time.Second),
		RepoMinFetchInterval:    durationDefault("REPO_MIN_FETCH_INTERVAL", time.Minute),
		RepoLeaseTTL:            durationDefault("REPO_CHANGE_LEASE_TTL", 15*time.Minute),
		OpenPRBackfillInterval:  durationDefault("OPEN_PR_BACKFILL_INTERVAL", 5*time.Second),
		RepoBackfillMaxRuntime:  durationDefault("REPO_BACKFILL_MAX_RUNTIME", 3*time.Minute),
		RepoBackfillMaxPRs:      intDefault("REPO_BACKFILL_MAX_PRS", 25),
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

func durationDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func intDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
