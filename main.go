// muster — tmux-first agent-army manager on the ppz mesh.
//
// Agents are plain tmux sessions. Specs (the exact launch command) live in
// ~/.local/share/muster/agents/. Status comes from Claude Code hooks and
// ppz heartbeats — never screen scraping. Resume replays your exact argv.
package main

import (
	"fmt"
	"os"
)

const usage = `muster — herd an army of coding agents with tmux + ppz pipes

  (no command)            open the muster UI

  spawn <name> [-C dir | --repo dir -b branch] [-e K=V]... [--] [cmd...]
                          start an agent (default cmd: $MUSTER_DEFAULT_CMD or claude)
  ls [--json] [--watch]   every agent: state, unread, age
  attach <name>           go to an agent (switch-client inside tmux)
  menu                    tmux popup picker (bind a key to this)
  resume <name>|--all     restart dead agents with their EXACT original command
  kill <name> [--rm]      kill session; --rm also removes worktree+spec (guarded)
  project add|ls|rm       register repos/dirs — the UI groups agents by project

  send <name> <text>      message an agent over ppz (delivered when idle)
  broadcast <text>        message all live agents
  inbox <name>            peek an agent's inbox (no cursor move)
  cron add <name> (--every 4h|--cron "0 9 * * 1"|--at +10m) <prompt>
  cron ls | rm <id>       durable server-side schedules (fire while you sleep)

  init                    write claude hooks settings + tmux snippet
  doctor                  environment checks
  hook                    (internal) claude hook sink

state: ⚙ working  ✋ blocked  ✔ idle  ☠ dead   env: MUSTER_DEFAULT_CMD, MUSTER_PPZ, MUSTER_STATE_DIR
`

func main() {
	if len(os.Args) < 2 {
		os.Exit(cmdTUI(nil))
	}
	cmds := map[string]func([]string) int{
		"ui": cmdTUI, "spawn": cmdSpawn, "ls": cmdLs, "attach": cmdAttach, "menu": cmdMenu,
		"resume": cmdResume, "kill": cmdKill,
		"send": cmdSend, "broadcast": cmdBroadcast, "inbox": cmdInbox,
		"cron": cmdCron, "project": cmdProject,
		"init": cmdInit, "doctor": cmdDoctor, "hook": cmdHook,
	}
	if os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		fmt.Print(usage)
		os.Exit(0)
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "muster: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	os.Exit(fn(os.Args[2:]))
}
