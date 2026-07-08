package main

import (
	"encoding/json"
	"fmt"
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
