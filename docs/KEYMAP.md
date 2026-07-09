# muster keymap

Design rules, in priority order:

1. **tmux is the substrate — never fight it.** Everything tmux already does
   (pane focus, zoom, scrollback, copy-mode, splits) keeps its native keys
   under the user's own prefix. muster only adds *fleet* verbs, and puts them
   on unprefixed single keys in the sidebar — the sidebar is muster's "mode",
   so it can own bare keys the way copy-mode does.
2. **vim motion, tmux muscle memory.** j/k move, l/enter descend (into the
   agent), esc backs out. Where tmux has a convention (z = zoom, ! = break
   out a shell) muster reuses the letter.
3. **Everything mouse-reachable is key-reachable, and vice versa.** Every
   right-click menu item names its sidebar key. Menus are just the
   discoverable spelling of the keymap.
4. **Uppercase = wider blast radius.** s sends to one agent, b broadcasts;
   r resumes one, R resumes all; T standups everyone; K kills.

## Sidebar (unprefixed — the sidebar pane is focused)

| key | action | scope |
|-----|--------|-------|
| j / k, ↓ / ↑ | select next/previous agent | selection |
| enter, l, tab | focus the agent's terminal (resume if dead) | selected |
| esc | leave room chat / back | view |
| a, S | spawn form (project preselected) | fleet |
| P | add project (repo picker, filter-as-you-type) | fleet |
| t | shell split in the agent's dir (space dir if none) | selected |
| v | file menu: files mentioned on the agent's screen — fully keyboard, `v` then `1`–`9`/`a`–`c` (or arrows + enter) | selected |
| V | open the *most recently mentioned* file instantly, no menu | selected |
| s | send message | selected |
| b | broadcast | all live |
| T | standup — every agent reports | all live |
| i | inbox | selected |
| c / C | schedule prompt / list schedules | selected / all |
| M | pipes/mesh view (team, traffic, connect) | mesh |
| K | kill (y confirms, y --rm removes) | selected |
| r / R | resume selected / all dead | selected / all |
| o | sort: attention ⇄ project grouping | view |
| g | refresh now | view |
| z | zoom agent pane | layout |
| ? | key help | — |
| d | leave workspace (fleet keeps running) | session |
| q | quit workspace (kills the muster session, not agents) | session |

Hidden: F6–F10 are menu→TUI callbacks (spawn/worktree/remove/room/terminal
for the right-clicked project). Not for fingers.

## Mouse

| gesture | where | action |
|---------|-------|--------|
| left-click agent row | sidebar | select (right pane follows) |
| left-click project row | sidebar | **room chat** — the space's traffic |
| left-click the `+` on a project row | sidebar | spawn form for that project |
| left-click title bar | sidebar | mesh view |
| right-click agent/project row | sidebar | context menu (opens on release, stays open) |
| right-click | **agent pane / pinned panes** | agent menu for *that* pane's agent |
| wheel | sidebar / room | scroll |

The right-pane menu is self-contained (native tmux prompts): send and
schedule prompt at the bottom of the screen, inbox opens a popup, kill asks
y/n. No sidebar focus needed. Its split right/down/up/left (l/j/u/h) open a
shell in the agent's dir on that side of the pane — herdr semantics; the
sidebar menu's splits instead pin extra views of the agent.

## Inside the agent pane

It's a real terminal — type as normal. tmux-native, with the user's own
prefix (this machine: C-a):

| key | action |
|-----|--------|
| prefix z | zoom / unzoom the pane |
| prefix ← | back to the sidebar |
| prefix prefix [ | scrollback of the *inner* (agent) session |
| q | closes a file-viewer split (it's just a pager) |
| ctrl-d / `exit` | closes a quick-terminal strip (it's just a shell) |
| prefix x | force-kill any split (tmux native, y to confirm) |

## Room chat (right pane, after clicking a project)

A compose line at the bottom is always focused — type and hit enter to
message every agent in the room at once (fans out to each member's ppz
handle; there's no server-side broadcast pipe). Because typing is live, `q`
no longer closes the room — only esc/ctrl-c do.

| key | action |
|-----|--------|
| (typing) | composes a message to the whole room |
| enter | send to every member |
| ↑/↓, wheel, pgup/pgdn | scroll history (auto-follows tail until you scroll up) |
| esc | clear the draft, or close if the draft is already empty |
| ctrl-c | close (or just select an agent in the sidebar) |

## Candidate future keys (unassigned, deliberately)

- `/` filter agents by name (when fleets outgrow one screen)
- `u` jump to the most attention-worthy agent (first blocked)
- number keys 1–9: jump to Nth agent
