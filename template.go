package main

// Role templates: the reusable shape of a teammate. "One really good at
// backend, one at frontend, one at infra — and add more as demand grows"
// becomes a first-class object instead of a naming convention: a template
// bundles the role charter, the model, the skills worth naming in a brief,
// a default project, a pool cap, and (optionally) a warm spare policy.
// `muster spawn --as backend` mints backend-1, backend-2, … and dispatch
// (dispatch.go) routes tasks to whichever pool member is free.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Template struct {
	Name    string   `json:"name"`
	Role    string   `json:"role,omitempty"`    // charter, same as spec.Role
	Model   string   `json:"model,omitempty"`   // opus|sonnet|haiku|full id
	Skills  []string `json:"skills,omitempty"`  // skill names worth citing in a brief
	Cmd     []string `json:"cmd,omitempty"`     // full command override (default: claude)
	Project string   `json:"project,omitempty"` // default project name
	Cap     int      `json:"cap,omitempty"`     // max pool size (default 3)
	Warm    bool     `json:"warm,omitempty"`    // keep one pre-booted spare ready
}

func (t Template) cap() int {
	if t.Cap > 0 {
		return t.Cap
	}
	return 3
}

func templatesPath() string { return filepath.Join(dataDir(), "templates.json") }

func loadTemplates() []Template {
	b, err := os.ReadFile(templatesPath())
	if err != nil {
		return nil
	}
	var ts []Template
	if json.Unmarshal(b, &ts) != nil {
		return nil
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	return ts
}

func saveTemplates(ts []Template) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	b, _ := json.MarshalIndent(ts, "", "  ")
	return atomicWrite(templatesPath(), b)
}

func findTemplate(name string) *Template {
	for _, t := range loadTemplates() {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

// poolName mints the next free member name for a template: backend-1,
// backend-2, … skipping names that exist as specs. Pure over the spec list
// for testability.
func poolName(tmpl string, specs []*AgentSpec) string {
	taken := map[string]bool{}
	for _, s := range specs {
		taken[s.Name] = true
	}
	for i := 1; ; i++ {
		n := fmt.Sprintf("%s-%d", tmpl, i)
		if !taken[n] {
			return n
		}
	}
}

// poolMembers returns the live pool for a template, in name order.
func poolMembers(tmpl string, specs []*AgentSpec) []*AgentSpec {
	var out []*AgentSpec
	for _, s := range specs {
		if s.Template == tmpl {
			out = append(out, s)
		}
	}
	return out
}

// templateBriefing is the extra system-prompt paragraph a templated agent
// gets: its specialty and the skills it should reach for.
func templateBriefing(t *Template) string {
	if t == nil {
		return ""
	}
	b := "You were hired from the '" + t.Name + "' template — that is your specialty; tasks routed to " +
		"you will match it."
	if len(t.Skills) > 0 {
		b += " Skills you should reach for on matching tasks: /" + strings.Join(t.Skills, ", /") + "."
	}
	return b
}

// ---- CLI ---------------------------------------------------------------

func cmdTemplate(args []string) int {
	if len(args) < 1 {
		return fail(errf("usage: muster template ls | add <name> [flags] | rm <name> | show <name>"))
	}
	switch args[0] {
	case "ls":
		ts := loadTemplates()
		if len(ts) == 0 {
			fmt.Println("no templates. create one:\n  muster template add backend --role 'backend services' --model sonnet --proj <project> --warm")
			return 0
		}
		specs, _ := listSpecs()
		for _, t := range ts {
			pool := poolMembers(t.Name, specs)
			warm := ""
			if t.Warm {
				warm = " · warm spare"
			}
			model := t.Model
			if model == "" {
				model = "default"
			}
			fmt.Printf("%-14s %-8s pool %d/%d%s  %s\n", t.Name, model, len(pool), t.cap(), warm, t.Role)
			if len(t.Skills) > 0 {
				fmt.Printf("%-14s skills: /%s\n", "", strings.Join(t.Skills, " /"))
			}
		}
		return 0
	case "show":
		if len(args) != 2 {
			return fail(errf("usage: muster template show <name>"))
		}
		t := findTemplate(args[1])
		if t == nil {
			return fail(errf("no template %q", args[1]))
		}
		b, _ := json.MarshalIndent(t, "", "  ")
		fmt.Println(string(b))
		return 0
	case "rm":
		if len(args) != 2 {
			return fail(errf("usage: muster template rm <name>"))
		}
		ts := loadTemplates()
		kept := ts[:0]
		found := false
		for _, t := range ts {
			if t.Name == args[1] {
				found = true
				continue
			}
			kept = append(kept, t)
		}
		if !found {
			return fail(errf("no template %q", args[1]))
		}
		if err := saveTemplates(kept); err != nil {
			return fail(err)
		}
		fmt.Println("removed template", args[1], "(existing pool agents untouched)")
		return 0
	case "add":
		fs := flag.NewFlagSet("template add", flag.ExitOnError)
		role := fs.String("role", "", "charter, e.g. 'backend services and db'")
		model := fs.String("model", "", "opus|sonnet|haiku|full model id")
		proj := fs.String("proj", "", "default project name")
		capN := fs.Int("cap", 3, "max pool size")
		warm := fs.Bool("warm", false, "keep one pre-booted spare ready to claim")
		var skills multiFlag
		fs.Var(&skills, "skill", "skill to cite in briefs (repeatable)")
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return fail(errf("usage: muster template add <name> [--role txt] [--model m] [--proj p] [--cap n] [--warm] [--skill s]..."))
		}
		name := args[1]
		if err := validName(name); err != nil {
			return fail(err)
		}
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *proj != "" && !projectExists(*proj) {
			return fail(errf("no project %q (muster project ls)", *proj))
		}
		ts := loadTemplates()
		nt := Template{Name: name, Role: *role, Model: *model, Skills: skills,
			Project: *proj, Cap: *capN, Warm: *warm}
		replaced := false
		for i, t := range ts {
			if t.Name == name {
				ts[i], replaced = nt, true
			}
		}
		if !replaced {
			ts = append(ts, nt)
		}
		if err := saveTemplates(ts); err != nil {
			return fail(err)
		}
		verb := "added"
		if replaced {
			verb = "updated"
		}
		fmt.Printf("%s template %s — spawn with: muster spawn --as %s · dispatch with: @%s in the palette\n",
			verb, name, name, name)
		return 0
	}
	return fail(errf("unknown template subcommand %q", args[0]))
}
