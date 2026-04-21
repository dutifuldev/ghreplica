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
	AppAddr                         string
	DatabaseDialer                  string
	DatabaseURL                     string
	DatabaseMaxOpenConns            int
	DatabaseMaxIdleConns            int
	DatabaseConnectionBudget        int
	ControlDBMaxOpenConns           int
	ControlDBMaxIdleConns           int
	WebhookDBMaxOpenConns           int
	WebhookDBMaxIdleConns           int
	QueueDBMaxOpenConns             int
	QueueDBMaxIdleConns             int
	SyncDBMaxOpenConns              int
	SyncDBMaxIdleConns              int
	GitMirrorRoot                   string
	GitIndexTimeout                 time.Duration
	ASTGrepTimeout                  time.Duration
	ASTGrepBin                      string
	GitHubBaseURL                   string
	GitHubToken                     string
	GitHubAppID                     string
	GitHubInstallationID            string
	GitHubAppPrivateKeyPEM          string
	GitHubAppPrivateKeyPath         string
	GitHubWebhookSecret             string
	ChangeSyncPollInterval          time.Duration
	WebhookFetchDebounce            time.Duration
	OpenPRInventoryMaxAge           time.Duration
	RepoLeaseTTL                    time.Duration
	BackfillMaxRuntime              time.Duration
	BackfillMaxPRsPerPass           int
	WebhookJobQueueConcurrency      int
	WebhookJobTimeout               time.Duration
	WebhookJobMaxAttempts           int
	WebhookDeliveryRetention        time.Duration
	WebhookDeliveryCleanupInterval  time.Duration
	WebhookDeliveryCleanupBatchSize int
	CloudSQLInstanceConnectionName  string
	CloudSQLUseIAMAuthN             bool
}

func Load() Config {
	return Config{
		AppAddr:                         getenvDefault("APP_ADDR", "127.0.0.1:8080"),
		DatabaseDialer:                  getenvDefault("DB_DIALER", "postgres"),
		DatabaseURL:                     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DatabaseMaxOpenConns:            intDefault("DB_MAX_OPEN_CONNS", 10),
		DatabaseMaxIdleConns:            intDefault("DB_MAX_IDLE_CONNS", 5),
		DatabaseConnectionBudget:        intValue("DB_CONNECTION_BUDGET"),
		ControlDBMaxOpenConns:           intDefault("DB_CONTROL_MAX_OPEN_CONNS", 4),
		ControlDBMaxIdleConns:           intDefault("DB_CONTROL_MAX_IDLE_CONNS", 2),
		WebhookDBMaxOpenConns:           intDefault("DB_WEBHOOK_MAX_OPEN_CONNS", 4),
		WebhookDBMaxIdleConns:           intDefault("DB_WEBHOOK_MAX_IDLE_CONNS", 2),
		QueueDBMaxOpenConns:             intDefault("DB_QUEUE_MAX_OPEN_CONNS", 4),
		QueueDBMaxIdleConns:             intDefault("DB_QUEUE_MAX_IDLE_CONNS", 2),
		SyncDBMaxOpenConns:              intDefault("DB_SYNC_MAX_OPEN_CONNS", 8),
		SyncDBMaxIdleConns:              intDefault("DB_SYNC_MAX_IDLE_CONNS", 2),
		GitMirrorRoot:                   getenvDefault("GIT_MIRROR_ROOT", ".data/git-mirrors"),
		GitIndexTimeout:                 durationDefault("GIT_INDEX_TIMEOUT", 5*time.Minute),
		ASTGrepTimeout:                  durationDefault("AST_GREP_TIMEOUT", time.Minute),
		ASTGrepBin:                      getenvDefault("AST_GREP_BIN", "ast-grep"),
		GitHubBaseURL:                   getenvDefault("GITHUB_BASE_URL", "https://api.github.com"),
		GitHubToken:                     strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		GitHubAppID:                     strings.TrimSpace(os.Getenv("GITHUB_APP_ID")),
		GitHubInstallationID:            strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALLATION_ID")),
		GitHubAppPrivateKeyPEM:          strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM")),
		GitHubAppPrivateKeyPath:         strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")),
		GitHubWebhookSecret:             strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")),
		ChangeSyncPollInterval:          durationDefault("CHANGE_SYNC_POLL_INTERVAL", 5*time.Second),
		WebhookFetchDebounce:            durationDefault("WEBHOOK_REFRESH_DEBOUNCE", 15*time.Second),
		OpenPRInventoryMaxAge:           durationDefault("OPEN_PR_INVENTORY_MAX_AGE", 6*time.Hour),
		RepoLeaseTTL:                    durationDefault("REPO_CHANGE_LEASE_TTL", 15*time.Minute),
		BackfillMaxRuntime:              durationDefault("BACKFILL_MAX_RUNTIME", 30*time.Minute),
		BackfillMaxPRsPerPass:           intDefault("BACKFILL_MAX_PRS_PER_PASS", 1000),
		WebhookJobQueueConcurrency:      intDefault("WEBHOOK_JOB_QUEUE_CONCURRENCY", 1),
		WebhookJobTimeout:               durationDefault("WEBHOOK_JOB_TIMEOUT", 30*time.Second),
		WebhookJobMaxAttempts:           intDefault("WEBHOOK_JOB_MAX_ATTEMPTS", 8),
		WebhookDeliveryRetention:        optionalDuration("WEBHOOK_DELIVERY_RETENTION"),
		WebhookDeliveryCleanupInterval:  durationDefault("WEBHOOK_DELIVERY_CLEANUP_INTERVAL", 15*time.Minute),
		WebhookDeliveryCleanupBatchSize: intDefault("WEBHOOK_DELIVERY_CLEANUP_BATCH_SIZE", 500),
		CloudSQLInstanceConnectionName:  strings.TrimSpace(os.Getenv("CLOUDSQL_INSTANCE_CONNECTION_NAME")),
		CloudSQLUseIAMAuthN:             boolDefault("CLOUDSQL_USE_IAM_AUTHN", false),
	}
}

func (c Config) ValidateDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.DatabaseDialer)) {
	case "", "postgres":
		return nil
	case "cloudsql":
		if strings.TrimSpace(c.CloudSQLInstanceConnectionName) == "" {
			return errors.New("CLOUDSQL_INSTANCE_CONNECTION_NAME is required for DB_DIALER=cloudsql")
		}
		return nil
	default:
		return fmt.Errorf("unsupported DB_DIALER %q", c.DatabaseDialer)
	}
}

func (c Config) ValidateServeRuntime() error {
	if err := c.validateDatabasePoolBudget(); err != nil {
		return err
	}
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

func (c Config) validateDatabasePoolBudget() error {
	if c.DatabaseConnectionBudget <= 0 {
		return nil
	}
	total := c.ControlDBMaxOpenConns + c.WebhookDBMaxOpenConns + c.QueueDBMaxOpenConns + c.SyncDBMaxOpenConns
	if total > c.DatabaseConnectionBudget {
		return fmt.Errorf("database pool budget exceeded: control=%d webhook=%d queue=%d sync=%d total=%d budget=%d",
			c.ControlDBMaxOpenConns,
			c.WebhookDBMaxOpenConns,
			c.QueueDBMaxOpenConns,
			c.SyncDBMaxOpenConns,
			total,
			c.DatabaseConnectionBudget,
		)
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

func optionalDuration(key string) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0
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

func intValue(key string) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func boolDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
