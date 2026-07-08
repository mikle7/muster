#!/bin/bash
# Restart the local ppz dev mesh (server + daemon) after a reboot.
# Safe to re-run: exits early if the server is already up.
set -euo pipefail
DEV="$(cd "$(dirname "$0")" && pwd)"
PPZ_BIN_DIR="$DEV/../../../ppz/bin"   # ../ppz repo, built with `make build`

if curl -fsS http://localhost:8080/healthz >/dev/null 2>&1; then
  echo "ppz-server already running"
else
  pg_isready >/dev/null || { echo "postgres not running: brew services start postgresql@14"; exit 1; }
  set -a; . "$DEV/nats.env"; set +a          # trust root — NEVER regenerate
  export PPZ_DB_URL="postgres://michaelbell@localhost:5432/ppz?sslmode=disable"
  export PPZ_HTTP_ADDR=":8080"
  export PPZ_NATS_ADDR="127.0.0.1:4222"
  export PPZ_BASE_URL="http://localhost:8080"
  export PPZ_SESSION_KEY="muster-local-dev-session-key-0123456789abcdef"
  export PPZ_DEV_LOGIN="true"
  export PPZ_JETSTREAM_STORE_DIR="$DEV/jetstream"
  nohup "$PPZ_BIN_DIR/ppz-server" >> "$DEV/server.log" 2>&1 &
  sleep 2
  curl -fsS http://localhost:8080/healthz && echo " ...server up"
fi

ppz daemon start || true
ppz status || echo "if logged out: ppz login http://localhost:8080 -apikey \"\$(cat $DEV/seed/key-alpha.txt)\""
