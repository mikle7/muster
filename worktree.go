package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// worktreeAdd creates a sibling worktree <repo>__wt/<branch> on branch
// mstr/<branch> (created from repo HEAD if missing). Visible, not hidden.
func worktreeAdd(repo, branch string) (dir string, fullBranch string, err error) {
	repo, err = filepath.Abs(repo)
	if err != nil {
		return "", "", err
	}
	if _, gerr := git(repo, "rev-parse", "--git-dir"); gerr != nil {
		return "", "", fmt.Errorf("%s is not a git repo", repo)
	}
	fullBranch = branch
	if !strings.Contains(branch, "/") {
		fullBranch = "mstr/" + branch
	}
	safe := strings.ReplaceAll(strings.TrimPrefix(fullBranch, "mstr/"), "/", "-")
	dir = repo + "__wt" + string(filepath.Separator) + safe
	if _, err := os.Stat(dir); err == nil {
		return "", "", fmt.Errorf("worktree dir %s already exists", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", err
	}
	args := []string{"worktree", "add"}
	if _, berr := git(repo, "rev-parse", "--verify", "refs/heads/"+fullBranch); berr != nil {
		args = append(args, "-b", fullBranch, dir) // new branch from HEAD
	} else {
		args = append(args, dir, fullBranch)
	}
	if out, err := git(repo, args...); err != nil {
		return "", "", fmt.Errorf("git worktree add: %v: %s", err, out)
	}
	copyWorktreeInclude(repo, dir)
	return dir, fullBranch, nil
}

// copyWorktreeInclude copies gitignored files matching patterns in
// .worktreeinclude (one per line, gitignore-ish glob) from repo to wt.
// The #1 complaint about every worktree tool is missing .env files.
func copyWorktreeInclude(repo, wt string) {
	b, err := os.ReadFile(filepath.Join(repo, ".worktreeinclude"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		pat := strings.TrimSpace(line)
		if pat == "" || strings.HasPrefix(pat, "#") {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(repo, pat))
		for _, m := range matches {
			rel, err := filepath.Rel(repo, m)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			dst := filepath.Join(wt, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				continue
			}
			if data, err := os.ReadFile(m); err == nil {
				_ = os.WriteFile(dst, data, 0o600)
			}
		}
	}
}

// worktreeDirty reports why a worktree is unsafe to remove ("" = clean:
// no uncommitted changes, no untracked files, no unpushed commits).
func worktreeDirty(dir string) string {
	if out, err := git(dir, "status", "--porcelain"); err == nil && out != "" {
		return "uncommitted or untracked changes"
	}
	// unpushed: any commit not on any remote branch
	if out, err := git(dir, "log", "--branches", "--not", "--remotes", "--oneline", "-1", "HEAD"); err == nil && out != "" {
		return "unpushed commits"
	}
	return ""
}

func worktreeRemove(repo, dir string) error {
	if out, err := git(repo, "worktree", "remove", dir); err != nil {
		return fmt.Errorf("git worktree remove: %v: %s", err, out)
	}
	return nil
}
