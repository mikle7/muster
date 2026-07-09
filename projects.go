package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project is a registered repo/directory agents get grouped under in the UI
// and spawned into from the spawn form. Just a name + path — anything more
// belongs in the repo itself.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func projectsPath() string { return filepath.Join(dataDir(), "projects.json") }

func loadProjects() []Project {
	b, err := os.ReadFile(projectsPath())
	if err != nil {
		return nil
	}
	var ps []Project
	if json.Unmarshal(b, &ps) != nil {
		return nil
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps
}

func saveProjects(ps []Project) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	b, _ := json.MarshalIndent(ps, "", "  ")
	return atomicWrite(projectsPath(), b)
}

// addProject registers path (idempotent by path; name defaults to basename).
func addProject(path, name string) (Project, error) {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return Project{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Project{}, errf("not a directory: %s", abs)
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	ps := loadProjects()
	for _, p := range ps {
		if p.Path == abs {
			return p, nil // already registered
		}
		if p.Name == name {
			return Project{}, errf("project name %q taken (path %s)", name, p.Path)
		}
	}
	p := Project{Name: name, Path: abs}
	if err := saveProjects(append(ps, p)); err != nil {
		return Project{}, err
	}
	return p, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git")) // dir or file (worktree)
	return err == nil
}

// ---- the current space ------------------------------------------------------
// The folder you launch muster from IS your space (herdr-style): it gets
// auto-registered (git repos only) and floats to the top of the sidebar.

func spacePath() string { return filepath.Join(dataDir(), "space") }

// setSpace records dir as the current space; git repos are auto-registered
// as projects so agents group under them immediately. A subdir resolves to
// its repo root.
func setSpace(dir string) {
	if dir == "" {
		return
	}
	if root := gitRoot(dir); root != "" {
		dir = root
		_, _ = addProject(dir, "")
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = atomicWrite(spacePath(), []byte(dir))
}

// gitRoot walks up from dir to the enclosing git repo root ("" if none).
func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if isGitRepo(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

func currentSpace() string {
	b, err := os.ReadFile(spacePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- repo discovery (the [+ project] picker) --------------------------------

// repoRoots are scanned for git repos. Env beats config beats defaults,
// because everyone's layout differs.
func repoRoots() []string {
	if v := os.Getenv("MUSTER_REPO_ROOTS"); v != "" {
		return filepath.SplitList(v)
	}
	if c := loadConfig(); len(c.RepoRoots) > 0 {
		out := make([]string, len(c.RepoRoots))
		for i, r := range c.RepoRoots {
			out[i] = expandHome(r)
		}
		return out
	}
	h, _ := os.UserHomeDir()
	return []string{
		filepath.Join(h, "Repos"), filepath.Join(h, "repos"),
		filepath.Join(h, "code"), filepath.Join(h, "src"),
		filepath.Join(h, "Projects"), filepath.Join(h, "dev"),
		filepath.Join(h, "work"),
	}
}

// pruneDirs never contain user projects — skipping them keeps the walk fast.
var pruneDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "venv": true, "__pycache__": true,
}

// discoverRepos walks the roots up to repo_depth levels for git repos that
// aren't registered projects yet — so ~/repos/PixelPioneers/<proj> is found,
// not just ~/repos/<proj>. A found repo isn't descended into (nested repos
// belong to their parent), and muster worktree dirs (X__wt) are skipped.
func discoverRepos(registered []Project) []string {
	taken := map[string]bool{}
	for _, p := range registered {
		taken[p.Path] = true
	}
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		if !seen[dir] && !taken[dir] && !strings.HasSuffix(dir, "__wt") {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sep := string(filepath.Separator)
	maxDepth := repoDepth()
	for _, root := range repoRoots() {
		root := filepath.Clean(root)
		rootDepth := strings.Count(root, sep)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if p != root && (strings.HasPrefix(d.Name(), ".") || pruneDirs[d.Name()]) {
				return fs.SkipDir
			}
			if isGitRepo(p) {
				add(p)
				return fs.SkipDir
			}
			if strings.Count(p, sep)-rootDepth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// projectFor matches an agent dir to a registered project by path prefix,
// longest path first (so nested projects win). Worktree agents match via
// their Repo. "" = no project.
func projectFor(ps []Project, r lsRow) string {
	dir := r.Repo
	if dir == "" {
		dir = r.Dir
	}
	best := ""
	bestLen := -1
	for _, p := range ps {
		if (dir == p.Path || strings.HasPrefix(dir, p.Path+string(filepath.Separator))) && len(p.Path) > bestLen {
			best, bestLen = p.Name, len(p.Path)
		}
	}
	return best
}

func cmdProject(args []string) int {
	if len(args) < 1 {
		return fail(errf("usage: muster project add <path> [--name n] | ls | rm <name>"))
	}
	switch args[0] {
	case "add":
		rest := args[1:]
		name := ""
		var paths []string
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--name" && i+1 < len(rest) {
				name = rest[i+1]
				i++
				continue
			}
			paths = append(paths, rest[i])
		}
		if len(paths) != 1 {
			return fail(errf("usage: muster project add <path> [--name n]"))
		}
		p, err := addProject(paths[0], name)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("project %s → %s\n", p.Name, p.Path)
		return 0
	case "ls":
		ps := loadProjects()
		if len(ps) == 0 {
			fmt.Println("no projects. add one: muster project add <path>")
			return 0
		}
		for _, p := range ps {
			fmt.Printf("%-20s %s\n", p.Name, collapseHome(p.Path))
		}
		return 0
	case "rm":
		if len(args) != 2 {
			return fail(errf("usage: muster project rm <name>"))
		}
		ps := loadProjects()
		kept := ps[:0]
		found := false
		for _, p := range ps {
			if p.Name == args[1] {
				found = true
				continue
			}
			kept = append(kept, p)
		}
		if !found {
			return fail(errf("no project %q", args[1]))
		}
		if err := saveProjects(kept); err != nil {
			return fail(err)
		}
		fmt.Println("removed project", args[1], "(agents and dirs untouched)")
		return 0
	}
	return fail(errf("unknown project subcommand %q", args[0]))
}
