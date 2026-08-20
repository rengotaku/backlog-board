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

# API キーは plist に焼き込まない。
# scripts/launchd-exec.zsh が起動時に password-store から取得する。
# ここでは fail-closed に「復号できて空でないこと」だけを確認する（値は表示しない）。
PASS_NS="backlog-board"
PASS_ENTRY="$PASS_NS/BACKLOG_API_KEY"

if ! command -v pass >/dev/null 2>&1; then
    echo "ERROR: pass not found. brew install pass pinentry-mac" >&2
    exit 1
fi

PROBE_DIR="$(mktemp -d -t backlog-board-XXXXXX)"
trap 'rm -rf "$PROBE_DIR"' EXIT
PROBE_FILE="$PROBE_DIR/probe"
if ! pass "$PASS_ENTRY" > "$PROBE_FILE" 2>/dev/null; then
    echo "ERROR: $PASS_ENTRY の復号に失敗しました。gpg-agent と password-store を確認してください。" >&2
    exit 1
fi
if [[ ! -s "$PROBE_FILE" ]]; then
    echo "ERROR: $PASS_ENTRY が空です。空の API キーでは Backlog 認証が黙って全滅します。" >&2
    exit 1
fi
rm -f "$PROBE_FILE"

render_plist() {
    local src="$1" dest="$2"
    sed -e "s|__HOME__|$HOME|g" \
        -e "s|__PROJECT__|$PROJECT_DIR|g" \
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
