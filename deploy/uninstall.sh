#!/usr/bin/env bash
#
# Uninstall the realtime assistant systemd service.
#
# Usage:
#   deploy/uninstall.sh [--user|--system] [--purge]
#
#   --user    (default) remove the per-user unit
#   --system  remove the root-level unit (uses sudo)
#   --purge   also delete the config file and installed binary
set -euo pipefail

SERVICE=realtime-assistant

LEVEL=user
PURGE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)    LEVEL=user ;;
    --system)  LEVEL=system ;;
    --purge)   PURGE=1 ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^#//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
  shift
done

if [[ $LEVEL == user ]]; then
  UNIT="$HOME/.config/systemd/user/$SERVICE.service"
  CONF_DIR="$HOME/.config/$SERVICE"
  BIN="$HOME/.local/bin/$SERVICE"
  CTL="systemctl --user"
  SUDO=""
else
  UNIT="/etc/systemd/system/$SERVICE.service"
  CONF_DIR="/etc/$SERVICE"
  BIN="/usr/local/bin/$SERVICE"
  CTL="sudo systemctl"
  SUDO="sudo"
fi

$CTL disable --now "$SERVICE.service" 2>/dev/null || true
$SUDO rm -f "$UNIT"
$CTL daemon-reload 2>/dev/null || true
echo ">> removed unit $UNIT"

if [[ $PURGE -eq 1 ]]; then
  $SUDO rm -rf "$CONF_DIR"
  $SUDO rm -f "$BIN"
  echo ">> purged config $CONF_DIR and binary $BIN"
else
  echo ">> kept config $CONF_DIR and binary $BIN (use --purge to remove)"
fi
