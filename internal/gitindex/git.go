package gitindex

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (s *Service) mirrorPath(owner, repo string) string {
	return filepath.Join(s.mirrorRoot, owner, repo+".git")
}

func (s *Service) ensureMirror(ctx context.Context, owner, repo, remoteURL string) (string, error) {
	path := s.mirrorPath(owner, repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, err := s.runGit(ctx, "", "init", "--bare", path); err != nil {
			return "", err
		}
	}
	if _, err := s.runGit(ctx, path, "remote", "remove", "origin"); err != nil {
		if !strings.Contains(err.Error(), "No such remote") {
			return "", err
		}
	}
	if _, err := s.runGit(ctx, path, "remote", "add", "origin", remoteURL); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) runGit(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.gitBin, args...)
	if repoPath != "" {
		cmd.Args = append([]string{s.gitBin, "-C", repoPath}, args...)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if s.authHeader != "" {
		cmd.Env = append(os.Environ(), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0="+s.authHeader)
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func basicAuthHeader(token string) string {
	if strings.TrimSpace(token) == "" {
		return ""
	}
	value := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return "AUTHORIZATION: basic " + value
}
