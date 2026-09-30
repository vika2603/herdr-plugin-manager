package updates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a repository whose history is: add plugin/, change only a
// root file, then change plugin/. It returns its file URL and the commits.
func gitRepo(t *testing.T) (url string, commits []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(file, content string) {
		t.Helper()
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", file)
		commits = append(commits, git("rev-parse", "HEAD"))
	}
	git("init", "-q")
	// A fetch by commit and a filtered fetch both need the serving side to
	// allow them, as GitHub does.
	git("config", "uploadpack.allowFilter", "true")
	git("config", "uploadpack.allowAnySHA1InWant", "true")
	commit("plugin/main.go", "v1")
	commit("README.md", "docs")
	commit("plugin/main.go", "v2")
	return "file://" + dir, commits
}

func TestSameDir(t *testing.T) {
	url, c := gitRepo(t)
	ctx := context.Background()
	if same, err := (Git{}).SameDir(ctx, url, "plugin", c[0], c[1]); err != nil || !same {
		t.Errorf("a commit outside plugin/: same = %v, %v; want true", same, err)
	}
	if same, err := (Git{}).SameDir(ctx, url, "plugin", c[1], c[2]); err != nil || same {
		t.Errorf("a change inside plugin/: same = %v, %v; want false", same, err)
	}
	if _, err := (Git{}).SameDir(ctx, url, "missing", c[0], c[2]); err == nil {
		t.Error("a directory absent at both commits should be an error")
	}
}
