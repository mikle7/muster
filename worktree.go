package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// worktreeFetchTimeout caps the best-effort fetch so a slow/hung network never
// stalls a spawn — on timeout we just fall back to the local base.
const worktreeFetchTimeout = 20 * time.Second

// freshBase resolves the ref a NEW worktree branch should be cut from so the
// agent starts at real latest master (herdr-parity), not whatever stale commit
// the local checkout happens to sit on. Best-effort: fetch origin's default
// branch and return "origin/<default>". On ANY problem — fetch disabled, no
// remote, unset origin/HEAD, offline — return "" so the caller falls back to
// the local HEAD; a spawn must never block or fail on this. note is a one-line
// message to surface (empty = say nothing; true of the common no-remote case,
// which keeps local test repos silent and hermetic).
func freshBase(repo string) (ref, note string) {
	if os.Getenv("MUSTER_WORKTREE_NO_FETCH") != "" {
		return "", ""
	}
	// no remote at all (local-only repo, most tests): nothing to fetch, stay quiet
	if out, err := git(repo, "remote"); err != nil || strings.TrimSpace(out) == "" {
		return "", ""
	}
	branch := baseBranch(repo)
	if branch == "" {
		return "", "" // detached HEAD / can't tell: fall back to local
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeFetchTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "fetch", "origin", branch)
	c.Dir = repo
	if _, ferr := c.CombinedOutput(); ferr != nil {
		return "", "muster: worktree base: fetch failed, using local HEAD (offline?)"
	}
	return "origin/" + branch, "muster: worktree base: origin/" + branch + " (latest)"
}

// baseBranch names the branch a fresh worktree should track from origin.
// Prefers origin's default branch (origin/HEAD) when the local symbolic ref is
// set; otherwise the repo's own current branch — the common case (a checkout
// sitting on main) resolves to main either way. "" when HEAD is detached or
// unreadable, so the caller falls back to the local base. origin/HEAD is NOT
// always set (a clone of a push-populated bare repo has none), which is why
// current-branch is a required fallback, not a nicety.
func baseBranch(repo string) string {
	if out, err := git(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if d := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); d != "" {
			return d
		}
	}
	cur, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if cur = strings.TrimSpace(cur); err != nil || cur == "" || cur == "HEAD" {
		return ""
	}
	return cur
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
		// new branch: cut it from a freshly-fetched base so the agent starts at
		// real latest, not a stale local checkout. Best-effort — "" means the
		// fetch was skipped/failed and we fall back to local HEAD.
		base, note := freshBase(repo)
		if note != "" {
			fmt.Println(note)
		}
		if base != "" {
			args = append(args, "-b", fullBranch, dir, base)
		} else {
			args = append(args, "-b", fullBranch, dir) // local HEAD
		}
	} else {
		args = append(args, dir, fullBranch) // existing branch: leave it as-is
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

// worktreeRepoRoot resolves dir's main repo directory when dir sits inside
// a muster-created worktree (<repo>__wt/<branch>, from worktreeAdd above) —
// "" otherwise. projectFor matches Repo/Dir against a registered project by
// exact-string prefix (no symlink resolution), so this walks dir's own
// ancestors as plain strings rather than asking git — asking git would
// return a symlink-canonicalized path (e.g. macOS's /tmp -> /private/tmp),
// which would then silently mismatch a project registered via the literal,
// non-canonicalized path every other codepath here uses (filepath.Abs).
// Needed because a sibling worktree dir never prefix-matches the registered
// repo path on its own — spawning with `-C` into an existing worktree
// silently landed in no project bucket (2026-07-16, found by Michael).
func worktreeRepoRoot(dir string) string {
	for d := filepath.Clean(dir); ; {
		parent := filepath.Dir(d)
		if parent == d {
			return "" // reached filesystem root, no __wt ancestor
		}
		if base := filepath.Base(parent); strings.HasSuffix(base, "__wt") {
			return filepath.Join(filepath.Dir(parent), strings.TrimSuffix(base, "__wt"))
		}
		d = parent
	}
}

// liveBranch reads the branch actually checked out in dir. spec.Branch only
// knows what spawn created — regular agents have none, and any agent can
// switch branches mid-session; the sidebar should show what's really in that
// terminal (herdr got this right). Pure file reads (no git subprocess — this
// runs for the whole fleet on the TUI's 2s tick): .git may be a directory
// (clone) or a file ("gitdir: <path>", worktrees). Detached HEAD → short sha.
func liveBranch(dir string) string {
	for {
		if gitDir := resolveGitDir(dir); gitDir != "" {
			return headBranch(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == "" {
			return ""
		}
		dir = parent
	}
}

func resolveGitDir(dir string) string {
	p := filepath.Join(dir, ".git")
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		return p
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return gd
}

func headBranch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(head) >= 8 {
		return head[:8] // detached
	}
	return ""
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

// ---- worktree environment setup ----------------------------------------------
// "Worktrees isolate code, not environments" is the #1 recurring complaint
// about every worktree tool (missing node_modules, .env, ports, DBs).
// .worktreeinclude copies files; .muster/setup goes further: it runs IN the
// agent's pane, in the fresh worktree, before the agent starts — deps install
// visibly, and the agent begins in a working environment. Composed at spawn
// time only (fresh worktrees), never stored in Argv — resume stays faithful.

// setupScript returns the repo's worktree setup script, "" when none.
func setupScript(repo string) string {
	for _, p := range []string{
		filepath.Join(repo, ".muster", "setup"),
		filepath.Join(repo, ".muster-setup.sh"),
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// withSetup prefixes the pane command with the setup script run (best-effort:
// a failed setup still starts the agent — the error stays visible above it).
func withSetup(cmd, script string) string {
	if script == "" {
		return cmd
	}
	return "echo " + shQuote("muster: worktree setup — "+collapseHome(script)) +
		"; sh " + shQuote(script) + " || echo 'muster: setup failed (agent starts anyway)'; " + cmd
}

// ---- done: merge the branch back & clean up -----------------------------------
// uzi's `checkpoint` (one-command merge back to main) is the most-praised
// merge flow in the space, and "worktree not cleaned up after merge" is a
// top vibe-kanban bug. `muster done` = guarded merge → kill → remove.

// repoHeadBranch names the branch checked out in repo (the merge target).
func repoHeadBranch(repo string) (string, error) {
	out, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %s", out)
	}
	return out, nil
}

// mergeBranch merges branch into repo's checked-out branch. Guards: the repo
// working tree must be clean (a merge into a dirty checkout is how you lose
// work). On conflict the merge is aborted and the error says so — nothing is
// left half-merged. Returns the merge summary.
func mergeBranch(repo, branch, agent string, squash bool) (string, error) {
	if out, err := git(repo, "status", "--porcelain"); err != nil || out != "" {
		if err != nil {
			return "", fmt.Errorf("git status: %s", out)
		}
		return "", fmt.Errorf("repo %s has uncommitted changes — commit/stash them first (merging into a dirty tree risks your work)", repo)
	}
	head, err := repoHeadBranch(repo)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("muster done: merge %s (agent %s)", branch, agent)
	if squash {
		if out, err := git(repo, "merge", "--squash", branch); err != nil {
			_, _ = git(repo, "merge", "--abort")
			_, _ = git(repo, "reset", "--merge")
			return "", fmt.Errorf("squash merge of %s into %s conflicts:\n%s\nresolve by hand in %s, or ask the agent to rebase onto %s first", branch, head, out, repo, head)
		}
		if out, err := git(repo, "commit", "-m", msg); err != nil {
			// squash with nothing to commit (already merged) is fine
			if strings.Contains(out, "nothing to commit") {
				return "already merged — nothing to commit", nil
			}
			return "", fmt.Errorf("git commit: %s", out)
		}
		return "squashed " + branch + " into " + head, nil
	}
	out, err := git(repo, "merge", "--no-ff", "-m", msg, branch)
	if err != nil {
		_, _ = git(repo, "merge", "--abort")
		return "", fmt.Errorf("merge of %s into %s conflicts:\n%s\nresolve by hand in %s, or ask the agent to rebase onto %s first", branch, head, out, repo, head)
	}
	return "merged " + branch + " into " + head, nil
}

// branchAhead reports whether branch has commits not on repo HEAD.
func branchAhead(repo, branch string) bool {
	out, err := git(repo, "log", branch, "--not", "HEAD", "--oneline", "-1")
	return err == nil && out != ""
}
