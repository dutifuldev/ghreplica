package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	OpenPRInventoryMaxAge   time.Duration
	RepoLeaseTTL            time.Duration
	BackfillMaxRuntime      time.Duration
	BackfillMaxPRsPerPass   int
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
		WebhookFetchDebounce:    durationDefault("WEBHOOK_REFRESH_DEBOUNCE", 15*time.Second),
		OpenPRInventoryMaxAge:   durationDefault("OPEN_PR_INVENTORY_MAX_AGE", 10*time.Minute),
		RepoLeaseTTL:            durationDefault("REPO_CHANGE_LEASE_TTL", 15*time.Minute),
		BackfillMaxRuntime:      durationDefault("BACKFILL_MAX_RUNTIME", 5*time.Minute),
		BackfillMaxPRsPerPass:   intDefault("BACKFILL_MAX_PRS_PER_PASS", 100),
	}
}

func (c Config) ValidateDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	return nil
}

func (c Config) ValidateServeRuntime() error {
	if err := c.validateGitMirrorRoot(); err != nil {
		return err
	}
	if err := c.validateASTGrepBinary(); err != nil {
		return err
	}
	if err := c.validateGitHubAppPrivateKey(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateGitMirrorRoot() error {
	root := strings.TrimSpace(c.GitMirrorRoot)
	if root == "" {
		return errors.New("GIT_MIRROR_ROOT is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("GIT_MIRROR_ROOT %q is not usable: %w", root, err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("GIT_MIRROR_ROOT %q is not readable: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("GIT_MIRROR_ROOT %q is not a directory", root)
	}

	probe, err := os.CreateTemp(root, ".ghreplica-write-check-*")
	if err != nil {
		return fmt.Errorf("GIT_MIRROR_ROOT %q is not writable: %w", root, err)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		return fmt.Errorf("GIT_MIRROR_ROOT %q write probe failed: %w", root, err)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("GIT_MIRROR_ROOT %q cleanup failed: %w", root, err)
	}
	return nil
}

func (c Config) validateASTGrepBinary() error {
	bin := strings.TrimSpace(c.ASTGrepBin)
	if bin == "" {
		return errors.New("AST_GREP_BIN is required")
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("ast-grep binary %q is not available: %w", bin, err)
	}
	return nil
}

func (c Config) validateGitHubAppPrivateKey() error {
	if strings.TrimSpace(c.GitHubAppPrivateKeyPEM) != "" {
		return nil
	}

	path := strings.TrimSpace(c.GitHubAppPrivateKeyPath)
	if path == "" {
		return nil
	}

	cleanPath := filepath.Clean(path)
	body, err := os.ReadFile(cleanPath)
	if err != nil {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY_PATH %q is not readable: %w", cleanPath, err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY_PATH %q is empty", cleanPath)
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
