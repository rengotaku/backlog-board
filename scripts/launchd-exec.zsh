#!/bin/zsh
# launchd から backlog-board-server を起動するラッパー。
# API キーは plist に焼き込まず、起動時に password-store から取得する。
set -euo pipefail

PROJECT_DIR="${0:A:h:h}"
PASS_NS="backlog-board"
PASS_ENTRY="$PASS_NS/BACKLOG_API_KEY"

# launchd は対話シェルの PATH を継承しないため明示する（pass / gpg は homebrew 配下）
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"

command -v pass >/dev/null 2>&1 || {
    print -u2 "ERROR: pass not found in PATH. brew install pass pinentry-mac"
    exit 1
}

# fail-closed: 復号失敗・空値のまま起動させない
# （空の API キーで起動すると Backlog 認証が黙って全滅する）
VAULT_DIR="$(mktemp -d -t backlog-board-XXXXXX)"
trap 'rm -rf "$VAULT_DIR"' EXIT
VAULT_FILE="$VAULT_DIR/key"

pass "$PASS_ENTRY" > "$VAULT_FILE" 2>/dev/null || {
    print -u2 "ERROR: $PASS_ENTRY の復号に失敗（gpg-agent / password-store を確認）"
    exit 1
}
[[ -s "$VAULT_FILE" ]] || {
    print -u2 "ERROR: $PASS_ENTRY が空"
    exit 1
}

# argv に載せず env 経由で渡す（代入プレフィックス。env(1) を挟むと argv に載る）
BACKLOG_API_KEY="$(head -n 1 "$VAULT_FILE")"
rm -rf "$VAULT_DIR"
trap - EXIT
export BACKLOG_API_KEY

exec "$PROJECT_DIR/bin/backlog-board-server"
