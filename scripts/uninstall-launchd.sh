#!/bin/zsh
# Unload launchd agents and remove their plists.
set -euo pipefail

DEST_DIR="$HOME/Library/LaunchAgents"

LAUNCHCTL="$(command -v launchctl || true)"
[[ -z "$LAUNCHCTL" && -x /bin/launchctl ]] && LAUNCHCTL=/bin/launchctl
[[ -z "$LAUNCHCTL" ]] && { echo "ERROR: launchctl not found" >&2; exit 1; }

for label in com.user.backlog-board.server com.user.backlog-board.fetch com.user.backlog-hub.server com.user.backlog-hub.fetch com.user.backlog-mentions.fetch com.user.backlog-mentions.server; do
    "$LAUNCHCTL" bootout "gui/$UID/$label" 2>/dev/null || true
    rm -f "$DEST_DIR/$label.plist"
    echo "removed: $label"
done

echo "Done."
