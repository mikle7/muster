package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is muster's single settings file: <state>/config.json, hand-editable,
// written with defaults by `muster init`. Env vars win over the file
// (MUSTER_SKIP_PERMISSIONS, MUSTER_REPO_ROOTS) so tests and one-offs can
// override without editing it.
type Config struct {
	SkipPermissions *bool    `json:"skip_permissions,omitempty"` // default true
	RepoRoots       []string `json:"repo_roots,omitempty"`       // picker scan roots
	RepoDepth       int      `json:"repo_depth,omitempty"`       // picker scan depth, default 3
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }

func loadConfig() Config {
	var c Config
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// skipPermissions: claude agents launch with --dangerously-skip-permissions
// by default — a fleet you babysit through permission prompts isn't a fleet.
// {"skip_permissions": false} or MUSTER_SKIP_PERMISSIONS=0 restores prompts.
func skipPermissions() bool {
	if v := os.Getenv("MUSTER_SKIP_PERMISSIONS"); v != "" {
		return v != "0"
	}
	if c := loadConfig(); c.SkipPermissions != nil {
		return *c.SkipPermissions
	}
	return true
}

func repoDepth() int {
	if c := loadConfig(); c.RepoDepth > 0 {
		return c.RepoDepth
	}
	return 3
}

// writeDefaultConfig materialises the defaults so users can see what's
// tweakable (init calls this; no-op when the file exists).
func writeDefaultConfig() {
	if _, err := os.Stat(configPath()); err == nil {
		return
	}
	on := true
	c := Config{
		SkipPermissions: &on,
		RepoRoots:       []string{"~/Repos", "~/repos", "~/code", "~/src", "~/Projects", "~/dev", "~/work"},
		RepoDepth:       3,
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if os.MkdirAll(dataDir(), 0o755) == nil {
		_ = atomicWrite(configPath(), b)
	}
}
