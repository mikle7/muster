#!/bin/sh
# .dev/dogfood.sh — nuke local muster state and stage a fresh dogfood fleet.
# Run from anywhere: ./dev/dogfood.sh (it finds the repo from its own path).
# What it does: rebuild+install muster, kill every agent + the workspace,
# wipe ~/.local/share/muster, re-init, check the mesh, create two demo
# repos (webshop has a .muster/setup hook, .worktreeinclude, and a planted
# bug), spawn a 3-agent fleet (peter=reviewer, dave=worktree, alice).
# Companion walkthrough: docs/DOGFOOD.md
set -e
say() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }

REPO="$(cd "$(dirname "$0")/.." && pwd)"
DOG="$HOME/muster-dogfood"

say "1/7 build + test + install"
cd "$REPO"
go vet ./... && go test ./...
go build -o /tmp/muster-dogfood-build .
rm -f "$HOME/.local/bin/muster" # rm first — macOS SIGKILLs binaries replaced in place
cp /tmp/muster-dogfood-build "$HOME/.local/bin/muster"
muster help >/dev/null && echo "installed: $(command -v muster)"

say "2/7 kill the workspace + every agent session"
tmux kill-session -t '=muster' 2>/dev/null && echo "killed workspace" || true
for s in $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^mstr-' || true); do
  tmux kill-session -t "=$s" && echo "killed $s"
done

say "3/7 wipe ALL local muster state"
rm -rf "$HOME/.local/share/muster"
echo "removed ~/.local/share/muster (specs, statuses, projects, config)"

say "4/7 muster init"
muster init

say "5/7 mesh check"
if ppz status 2>/dev/null | grep -q 'daemon: logged in'; then
  echo "mesh OK"
else
  echo "mesh not ready — trying the local stack"
  "$REPO/.dev/ppz-local/start.sh" || true
  sleep 2
  ppz status 2>/dev/null | head -5 || echo "WARNING: no mesh — rooms/review/standup will be offline"
fi

say "6/7 demo repos in $DOG"
rm -rf "$DOG"
mkdir -p "$DOG/webshop/.muster" "$DOG/gamesrv"

cd "$DOG/webshop"
git init -qb main
git config user.email dog@food.local && git config user.name dogfood
cat > server.js <<'JS'
// webshop — checkout service (dogfood fixture)
function cartTotal(items) {
  let total = 0;
  for (const it of items) {
    total += it.price; // BUG: ignores it.qty — dave's task is to fix this
  }
  return total;
}
function checkout(cart) {
  return { total: cartTotal(cart.items), currency: "USD" };
}
module.exports = { cartTotal, checkout };
JS
cat > server_test.js <<'JS'
const { cartTotal } = require("./server");
const got = cartTotal([{ price: 5, qty: 3 }]);
console.log(got === 15 ? "PASS" : `FAIL: want 15, got ${got}`);
JS
printf 'PORT=3000\nSECRET=dogfood-local-only\n' > .env
printf '.env\n' > .worktreeinclude
printf '.env\nnode_modules/\n' > .gitignore
cat > .muster/setup <<'SH'
#!/bin/sh
# runs IN the agent's pane, in the fresh worktree, BEFORE the agent starts
echo "[muster setup] worktree: $(pwd)"
[ -f .env ] && echo "[muster setup] .env copied by .worktreeinclude: OK" || echo "[muster setup] .env MISSING"
echo "[muster setup] pretending to install deps…"; sleep 2
echo "[muster setup] done — agent starts now"
SH
chmod +x .muster/setup
git add -A && git commit -qm "webshop: checkout service with quantity bug + tests"

cd "$DOG/gamesrv"
git init -qb main
git config user.email dog@food.local && git config user.name dogfood
printf '# gamesrv\n\nToy repo for the second project in the sidebar.\n' > README.md
git add -A && git commit -qm "gamesrv base"

say "7/7 spawn the fleet (sonnet)"
muster project add "$DOG/webshop" >/dev/null
muster project add "$DOG/gamesrv" >/dev/null
muster spawn peter --role 'code reviewer: when asked to review a branch, read the checkout dir, reply APPROVE or CHANGES with reasons' \
  -C "$DOG/webshop" -- claude --model sonnet
muster spawn dave --role 'implements webshop features and fixes' \
  --repo "$DOG/webshop" -b qty-fix -- claude --model sonnet
muster spawn alice --role 'game developer on gamesrv' \
  -C "$DOG/gamesrv" -- claude --model sonnet

echo
echo "ready. next:"
echo "  cd $DOG/webshop && muster"
echo "then follow docs/DOGFOOD.md step by step."
