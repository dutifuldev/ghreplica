package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
