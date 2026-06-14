#!/usr/bin/env bash
#
# Install the realtime assistant as a systemd service.
#
# Usage:
#   deploy/install.sh [--user|--system] [--now] [--no-build] [--bin PATH]
#
#   --user      (default) per-user unit via `systemctl --user` + linger
#   --system    root-level unit in /etc/systemd/system (uses sudo)
#   --now       start the service after enabling (skipped if config was just seeded)
#   --no-build  don't compile; use the repo's ./assistant (or --bin)
#   --bin PATH  install this prebuilt binary instead of building
#
# Secrets never enter git: the real config lives outside the repo (chmod 600).
set -euo pipefail

SERVICE=realtime-assistant
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

LEVEL=user
START=0
BUILD=1
BIN=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)     LEVEL=user ;;
    --system)   LEVEL=system ;;
    --now)      START=1 ;;
    --no-build) BUILD=0 ;;
    --bin)      BIN="${2:?--bin needs a path}"; shift ;;
    -h|--help)  sed -n '2,18p' "$0" | sed 's/^#//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
  shift
done

# Resolve paths per level.
if [[ $LEVEL == user ]]; then
  BIN_DIR="$HOME/.local/bin"
  CONF_DIR="$HOME/.config/$SERVICE"
  UNIT_DIR="$HOME/.config/systemd/user"
  WANTEDBY="default.target"
  SUDO=""
  TARGET_USER="$USER"
else
  BIN_DIR="/usr/local/bin"
  CONF_DIR="/etc/$SERVICE"
  UNIT_DIR="/etc/systemd/system"
  WANTEDBY="multi-user.target"
  SUDO="sudo"
  TARGET_USER="${SUDO_USER:-$USER}"
fi
BIN_DST="$BIN_DIR/$SERVICE"
CONF="$CONF_DIR/assistant.env"
UNIT="$UNIT_DIR/$SERVICE.service"

# 1. Build (or locate) the binary. Needs CGO + ALSA headers when building.
if [[ -n $BIN ]]; then
  SRC_BIN="$BIN"
elif [[ $BUILD -eq 1 ]]; then
  echo ">> building $SERVICE (CGO_ENABLED=1) …"
  TMPDIR_BUILD="$(mktemp -d)"
  SRC_BIN="$TMPDIR_BUILD/$SERVICE"
  ( cd "$REPO_ROOT" && CGO_ENABLED=1 go build -o "$SRC_BIN" ./cmd/assistant )
else
  SRC_BIN="$REPO_ROOT/assistant"
fi
[[ -x $SRC_BIN ]] || { echo "binary not found/executable: $SRC_BIN" >&2; exit 1; }

# 2. Install the binary.
echo ">> installing binary -> $BIN_DST"
$SUDO install -D -m 0755 "$SRC_BIN" "$BIN_DST"

# 3. Seed the config on first install only.
SEEDED=0
if [[ ! -f $CONF ]]; then
  echo ">> seeding config -> $CONF  (edit it with your endpoints/token)"
  $SUDO install -D -m 0600 "$REPO_ROOT/deploy/$SERVICE.env.example" "$CONF"
  [[ $LEVEL == system ]] && $SUDO chown "$TARGET_USER" "$CONF" || true
  SEEDED=1
else
  echo ">> keeping existing config $CONF"
fi

# 4. Render + install the unit.
echo ">> installing unit -> $UNIT"
render_unit() {
  cat <<UNITEOF
[Unit]
Description=Realtime voice assistant (LocalAI) with primary/fallback failover
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
$( [[ $LEVEL == system ]] && printf 'User=%s\nSupplementaryGroups=audio\n' "$TARGET_USER" )EnvironmentFile=$CONF
ExecStart=$BIN_DST
Restart=always
RestartSec=5

[Install]
WantedBy=$WANTEDBY
UNITEOF
}
$SUDO mkdir -p "$UNIT_DIR"
render_unit | $SUDO tee "$UNIT" >/dev/null

# 5. Enable.
if [[ $LEVEL == user ]]; then
  loginctl enable-linger "$USER" >/dev/null 2>&1 || true
  systemctl --user daemon-reload
  systemctl --user enable "$SERVICE.service"
  CTL="systemctl --user"
  LOGS="journalctl --user -u $SERVICE -f"
else
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable "$SERVICE.service"
  CTL="sudo systemctl"
  LOGS="sudo journalctl -u $SERVICE -f"
fi

# 6. Start (only when requested and config is real).
if [[ $START -eq 1 ]]; then
  if [[ $SEEDED -eq 1 ]]; then
    echo "!! config was just seeded with placeholders — NOT starting."
    echo "   edit $CONF then: $CTL start $SERVICE"
  else
    $CTL restart "$SERVICE.service"
    echo ">> started. follow logs with: $LOGS"
  fi
fi

echo ">> done ($LEVEL).  bin=$BIN_DST  config=$CONF  unit=$UNIT"
