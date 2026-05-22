#!/bin/zsh
# Render launchd plists with $HOME and install them under ~/Library/LaunchAgents/.
# Idempotent: bootstrap → unload existing first.
set -euo pipefail

PROJECT_DIR="${0:A:h:h}"
SRC_DIR="$PROJECT_DIR/launchd"
DEST_DIR="$HOME/Library/LaunchAgents"
LOG_DIR="$HOME/Library/Logs/backlog-board"

# launchctl は /bin にしか無い場合があるので絶対パスを優先
LAUNCHCTL="$(command -v launchctl || true)"
[[ -z "$LAUNCHCTL" && -x /bin/launchctl ]] && LAUNCHCTL=/bin/launchctl
[[ -z "$LAUNCHCTL" ]] && { echo "ERROR: launchctl not found" >&2; exit 1; }

mkdir -p "$DEST_DIR" "$LOG_DIR"

# BACKLOG_API_KEY は ~/.zshenv 等で export されている前提だが、
# launchd は GUI セッションの env を継承しないため、明示的に inject する。
KEY_FROM_SHELL="$(zsh -ic 'print -r -- ${BACKLOG_API_KEY:-}' 2>/dev/null || true)"

if [[ -z "$KEY_FROM_SHELL" ]]; then
    echo "ERROR: BACKLOG_API_KEY not found in shell env. Set it in ~/.zshenv before installing." >&2
    exit 1
fi

render_plist() {
    local src="$1" dest="$2"
    sed -e "s|__HOME__|$HOME|g" \
        -e "s|__PROJECT__|$PROJECT_DIR|g" \
        -e "s|__BACKLOG_API_KEY__|$KEY_FROM_SHELL|g" \
        "$src" > "$dest"
    chmod 600 "$dest"
}

reload_plist() {
    local label="$1" path="$2"
    if "$LAUNCHCTL" print "gui/$UID/$label" >/dev/null 2>&1; then
        "$LAUNCHCTL" bootout "gui/$UID/$label" 2>/dev/null || true
        # bootout は非同期。完全に unload されるまで待つ（KeepAlive=true の server 対策）
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            "$LAUNCHCTL" print "gui/$UID/$label" >/dev/null 2>&1 || break
            /bin/sleep 0.5
        done
    fi
    "$LAUNCHCTL" bootstrap "gui/$UID" "$path"
    # bootstrap は RunAtLoad=true でも実起動を skip する場合がある（bootout 直後の
    # 再登録レース等）。明示的に kickstart して確実に起動させる。
    "$LAUNCHCTL" kickstart "gui/$UID/$label"
    echo "loaded: $label"
}

# 旧 Label が残っていれば bootout + plist 削除（後方互換、1 度きりのマイグレーション用）
for legacy in com.user.backlog-mentions.fetch com.user.backlog-mentions.server com.user.backlog-hub.server com.user.backlog-hub.fetch; do
    if "$LAUNCHCTL" print "gui/$UID/$legacy" >/dev/null 2>&1; then
        "$LAUNCHCTL" bootout "gui/$UID/$legacy" 2>/dev/null || true
        echo "removed legacy label: $legacy"
    fi
    rm -f "$DEST_DIR/$legacy.plist"
done

for src in "$SRC_DIR"/*.plist; do
    name="$(basename "$src")"
    label="${name%.plist}"
    dest="$DEST_DIR/$name"
    render_plist "$src" "$dest"
    reload_plist "$label" "$dest"
done

echo "Done. Logs: $LOG_DIR"
echo "Server: http://localhost:8082"
