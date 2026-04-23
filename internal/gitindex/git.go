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
	"time"
)

func (s *Service) mirrorPath(owner, repo string) string {
	return filepath.Join(s.mirrorRoot, owner, repo+".git")
}

func (s *Service) lockPath(owner, repo string) string {
	return filepath.Join(s.mirrorRoot, "_locks", owner, repo+".lock")
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

func (s *Service) withRepoLock(ctx context.Context, owner, repo string, fn func() error) (err error) {
	lockPath := s.lockPath(owner, repo)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}

	lockFile, err := openRepoLockFile(lockPath)
	if err != nil {
		return err
	}
	defer func() {
		err = firstRepoLockErr(err, lockFile.Close())
	}()
	if err := waitForRepoLock(ctx, lockFile); err != nil {
		return err
	}
	defer func() {
		err = firstRepoLockErr(err, unlockRepoFile(lockFile))
	}()

	return fn()
}

func openRepoLockFile(lockPath string) (*os.File, error) {
	return os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
}

func firstRepoLockErr(current, next error) error {
	if current != nil || next == nil {
		return current
	}
	return next
}

func waitForRepoLock(ctx context.Context, lockFile *os.File) error {
	for {
		err := lockRepoFile(lockFile)
		switch {
		case err == nil:
			return nil
		case !lockWouldBlock(err):
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
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
