package testfixtures

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type LocalPullRepo struct {
	RemoteURL string
	BaseSHA   string
	Pulls     map[int]LocalPullRef
}

type LocalPullRef struct {
	Number  int
	HeadRef string
	HeadSHA string
}

func CreateLocalPullRepo(t testing.TB) LocalPullRepo {
	t.Helper()

	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	runGit(t, "", "init", "--bare", remotePath)

	worktree := filepath.Join(root, "worktree")
	runGit(t, "", "clone", remotePath, worktree)
	runGit(t, worktree, "config", "user.name", "Test User")
	runGit(t, worktree, "config", "user.email", "test@example.com")
	runGit(t, worktree, "checkout", "-b", "main")

	writeFile(t, filepath.Join(worktree, "app", "service.go"), strings.TrimSpace(`
package app

func run() {
	stepOne()
	stepTwo()
	stepThree()
}
`)+"\n")
	writeFile(t, filepath.Join(worktree, "docs", "readme.md"), "hello\n")
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "initial")
	baseSHA := strings.TrimSpace(runGit(t, worktree, "rev-parse", "HEAD"))
	runGit(t, worktree, "push", "origin", "HEAD:refs/heads/main")

	pulls := map[int]LocalPullRef{
		101: createPullRef(t, worktree, "feature-overlap-one", 101, map[string]string{
			"app/service.go": strings.TrimSpace(`
package app

func run() {
	stepOne()
	parseOne()
	parseTwo()
}
`) + "\n",
			"pkg/alpha.txt": "alpha\n",
		}),
		102: createPullRef(t, worktree, "feature-overlap-two", 102, map[string]string{
			"app/service.go": strings.TrimSpace(`
package app

func run() {
	stepOne()
	validateOne()
	validateTwo()
}
`) + "\n",
			"pkg/beta.txt": "beta\n",
		}),
		103: createPullRef(t, worktree, "feature-unrelated", 103, map[string]string{
			"docs/readme.md": "hello\nupdated docs\n",
		}),
	}

	return LocalPullRepo{
		RemoteURL: "file://" + remotePath,
		BaseSHA:   baseSHA,
		Pulls:     pulls,
	}
}

func createPullRef(t testing.TB, worktree, branch string, number int, files map[string]string) LocalPullRef {
	t.Helper()

	runGit(t, worktree, "checkout", "-B", branch, "main")
	for relativePath, content := range files {
		writeFile(t, filepath.Join(worktree, filepath.FromSlash(relativePath)), content)
	}
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", fmt.Sprintf("update %s", branch))
	headSHA := strings.TrimSpace(runGit(t, worktree, "rev-parse", "HEAD"))
	runGit(t, worktree, "push", "--force", "origin", "HEAD:refs/heads/"+branch)
	runGit(t, worktree, "push", "--force", "origin", fmt.Sprintf("HEAD:refs/pull/%d/head", number))
	return LocalPullRef{
		Number:  number,
		HeadRef: branch,
		HeadSHA: headSHA,
	}
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t testing.TB, worktree string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if worktree != "" {
		cmd.Dir = worktree
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
