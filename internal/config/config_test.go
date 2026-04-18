package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateServeRuntimeSucceeds(t *testing.T) {
	mirrorRoot := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "github-app.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"), 0o600))

	astGrepBin := writeExecutable(t, "ast-grep")
	cfg := Config{
		GitMirrorRoot:           mirrorRoot,
		ASTGrepBin:              astGrepBin,
		GitHubAppPrivateKeyPath: keyPath,
	}

	require.NoError(t, cfg.ValidateServeRuntime())
}

func TestValidateServeRuntimeFailsWhenASTGrepMissing(t *testing.T) {
	cfg := Config{
		GitMirrorRoot: t.TempDir(),
		ASTGrepBin:    filepath.Join(t.TempDir(), "missing-ast-grep"),
	}

	err := cfg.ValidateServeRuntime()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ast-grep binary")
}

func TestValidateServeRuntimeFailsWhenKeyUnreadable(t *testing.T) {
	cfg := Config{
		GitMirrorRoot:           t.TempDir(),
		ASTGrepBin:              writeExecutable(t, "ast-grep"),
		GitHubAppPrivateKeyPath: filepath.Join(t.TempDir(), "missing.pem"),
	}

	err := cfg.ValidateServeRuntime()
	require.Error(t, err)
	require.Contains(t, err.Error(), "GITHUB_APP_PRIVATE_KEY_PATH")
}

func TestValidateServeRuntimeFailsWhenMirrorRootIsFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o600))

	cfg := Config{
		GitMirrorRoot: root,
		ASTGrepBin:    writeExecutable(t, "ast-grep"),
	}

	err := cfg.ValidateServeRuntime()
	require.Error(t, err)
	require.Contains(t, err.Error(), "GIT_MIRROR_ROOT")
}

func TestLoadIncludesWebhookJobAndDatabasePoolDefaults(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONTROL_MAX_OPEN_CONNS", "")
	t.Setenv("DB_CONTROL_MAX_IDLE_CONNS", "")
	t.Setenv("DB_SYNC_MAX_OPEN_CONNS", "")
	t.Setenv("DB_SYNC_MAX_IDLE_CONNS", "")
	t.Setenv("WEBHOOK_JOB_QUEUE_CONCURRENCY", "")
	t.Setenv("WEBHOOK_JOB_TIMEOUT", "")
	t.Setenv("WEBHOOK_JOB_MAX_ATTEMPTS", "")

	cfg := Load()
	require.Equal(t, 10, cfg.DatabaseMaxOpenConns)
	require.Equal(t, 5, cfg.DatabaseMaxIdleConns)
	require.Equal(t, 6, cfg.ControlDBMaxOpenConns)
	require.Equal(t, 3, cfg.ControlDBMaxIdleConns)
	require.Equal(t, 6, cfg.SyncDBMaxOpenConns)
	require.Equal(t, 2, cfg.SyncDBMaxIdleConns)
	require.Equal(t, 3, cfg.WebhookJobQueueConcurrency)
	require.Equal(t, 30*time.Second, cfg.WebhookJobTimeout)
	require.Equal(t, 8, cfg.WebhookJobMaxAttempts)
}

func TestLoadReadsWebhookJobAndDatabasePoolOverrides(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "14")
	t.Setenv("DB_MAX_IDLE_CONNS", "7")
	t.Setenv("DB_CONTROL_MAX_OPEN_CONNS", "9")
	t.Setenv("DB_CONTROL_MAX_IDLE_CONNS", "4")
	t.Setenv("DB_SYNC_MAX_OPEN_CONNS", "11")
	t.Setenv("DB_SYNC_MAX_IDLE_CONNS", "5")
	t.Setenv("WEBHOOK_JOB_QUEUE_CONCURRENCY", "4")
	t.Setenv("WEBHOOK_JOB_TIMEOUT", "45s")
	t.Setenv("WEBHOOK_JOB_MAX_ATTEMPTS", "9")

	cfg := Load()
	require.Equal(t, 14, cfg.DatabaseMaxOpenConns)
	require.Equal(t, 7, cfg.DatabaseMaxIdleConns)
	require.Equal(t, 9, cfg.ControlDBMaxOpenConns)
	require.Equal(t, 4, cfg.ControlDBMaxIdleConns)
	require.Equal(t, 11, cfg.SyncDBMaxOpenConns)
	require.Equal(t, 5, cfg.SyncDBMaxIdleConns)
	require.Equal(t, 4, cfg.WebhookJobQueueConcurrency)
	require.Equal(t, 45*time.Second, cfg.WebhookJobTimeout)
	require.Equal(t, 9, cfg.WebhookJobMaxAttempts)
}

func writeExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)

	var content []byte
	if runtime.GOOS == "windows" {
		path += ".bat"
		content = []byte("@echo off\r\nexit /b 0\r\n")
	} else {
		content = []byte("#!/bin/sh\nexit 0\n")
	}

	require.NoError(t, os.WriteFile(path, content, 0o755))
	return path
}
