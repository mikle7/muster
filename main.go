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

  spawn <name> [--role txt] [-C dir | --repo dir -b branch] [-e K=V]... [--] [cmd...]
                          start an agent (default cmd: $MUSTER_DEFAULT_CMD or claude)
  q [cmd...]              quick spawn: auto-named chat-XXXX in cwd, workspace bucket
  ls [--json] [--watch]   every agent: state, unread, age
  attach <name>           go to an agent (switch-client inside tmux)
  menu                    tmux popup picker (bind a key to this)
  resume <name>|--all     restart dead agents with their EXACT original command
  kill <name> [--rm]      kill session; --rm also removes worktree+spec (guarded)
  refresh <name>          context reset without amnesia: flush handoff → /clear → re-inject
  recap <name>            the 10-second catch-up: state, events, git, inbox
  done <name> [--squash]  merge the worktree branch back, kill, clean up (guarded)
  review <name> [--by r]  hand the branch to a reviewer agent over the mesh
  project add|ls|rm       register repos/dirs — the UI groups agents by project
  project conventions <n> [text|--clear]  per-project workflow prompt (injected at spawn)

  send <name> <text>      message an agent over ppz (delivered when idle)
  broadcast <text>        message all live agents
  standup                 ask every agent for status — replies collect in the UI (T)
  inbox <name>            peek an agent's inbox (no cursor move)
  room <project> [--watch] the space's chat: all agent↔agent/you traffic, read receipts
  cron add <name> (--every 4h|--cron "0 9 * * 1"|--at +10m) <prompt>
  cron ls | rm <id>       durable server-side schedules (fire while you sleep)

  init                    write claude hooks settings + tmux snippet
  doctor                  environment checks
  hook                    (internal) claude hook sink

state: ⚙ working  ⌛ stalled  ✋ blocked (needs input)  → idle+unread  ✔ idle  ☠ dead
env: MUSTER_DEFAULT_CMD, MUSTER_PPZ, MUSTER_STATE_DIR, MUSTER_STALL_MIN, MUSTER_REFRESH_PCT
config: <state>/config.json — skip_permissions (default true), repo_roots, repo_depth, stall_after_min, refresh_ctx_pct (auto context refresh at this ctx% when idle; 0 off, default 75)
worktree env: .worktreeinclude copies files; .muster/setup runs in the pane before the agent
`

func main() {
	if len(os.Args) < 2 {
		os.Exit(cmdTUI(nil))
	}
	cmds := map[string]func([]string) int{
		"ui": cmdTUI, "spawn": cmdSpawn, "q": cmdQ, "ls": cmdLs, "attach": cmdAttach, "menu": cmdMenu,
		"resume": cmdResume, "kill": cmdKill, "refresh": cmdRefresh,
		"recap": cmdRecap, "done": cmdDone, "review": cmdReview,
		"send": cmdSend, "broadcast": cmdBroadcast, "inbox": cmdInbox,
		"cron": cmdCron, "project": cmdProject, "standup": cmdStandup, "room": cmdRoom,
		"init": cmdInit, "doctor": cmdDoctor, "hook": cmdHook,
		"rmenu": cmdRmenu, "fmenu": cmdFmenu, "wpin": cmdWpin, // internal: tmux menu callbacks
		"source-destroy": cmdSourceDestroy, // internal: clear offline mesh agent
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
