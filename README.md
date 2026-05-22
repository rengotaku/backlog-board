# backlog-board

Backlog のベル通知（自分宛メンション）と担当チケットをローカルで HTTP 配信する read-only ビューア。

- **fetch**: cron 相当（macOS launchd）が `BACKLOG_API_KEY` で `/notifications` 等を取得し、`~/.local/share/backlog-board/snapshot.json` に書き出す
- **server**: `:8082` で SSR HTML を返す。キャッシュファイルを毎リクエスト読み直すだけなので Backlog API への負荷ゼロ
- **claude 経由なし**: 取得もレンダリングも Go バイナリ単体で完結

## セットアップ

1. 初期セットアップ:

   ```bash
   make setup              # config.toml 配置 + 依存取得 + 環境変数チェック
   ```

   `~/.config/backlog-board/config.toml` が作成される（既存ならスキップ）。`domain` を自分の Backlog スペースに書き換える。

2. `~/.zshenv` 等で API キーを export:

   ```bash
   export BACKLOG_API_KEY="..."
   ```

3. ビルド & 起動:

   ```bash
   make build              # bin/backlog-board-server をビルド
   make run                # http://localhost:8082
   ```

## ステータス判定

`internal/backlog/mentions.go`:

| Status | 判定条件 |
|---|---|
| 返信済 | 通知発生時刻以降に自分が同じチケットへコメント |
| 確認済 | 通知対象コメントに自分のスター |
| 未対応 | 上記いずれでもない（コメント履歴を本文に同梱） |

## 設定

設定値は **TOML ファイル** と **環境変数** に分かれている。

### 設定ファイル (`~/.config/backlog-board/config.toml`)

個人セットアップ・運用パラメータはここに書く。詳細は `config.example.toml` を参照。

| キー | デフォルト | 用途 |
|---|---|---|
| `domain` | （必須） | Backlog スペースのドメイン（例: `yourspace.backlog.com`） |
| `port` | `8082` | サーバーポート |
| `cache_path` | `~/.local/share/backlog-board/snapshot.json` | snapshot.json のパス |
| `shutdown_timeout` | `10s` | graceful shutdown のタイムアウト |

`XDG_CONFIG_HOME` が設定されていればそちらが優先される。`BACKLOG_BOARD_CONFIG` 環境変数でパスを上書き可能。

### 環境変数

秘密情報とデバッグ時の動的切り替えのみ。

| 変数 | デフォルト | 用途 |
|---|---|---|
| `BACKLOG_API_KEY` | （必須） | Backlog API キー |
| `LOG_LEVEL` | `INFO` | `DEBUG` / `INFO` / `WARN` / `ERROR` |
| `BACKLOG_BOARD_CONFIG` | _(未設定)_ | 設定ファイルのパスを上書き（テスト用） |

## launchd で常駐

```bash
./scripts/install-launchd.sh    # server を LaunchAgent として登録
./scripts/uninstall-launchd.sh  # 解除
```

`install-launchd.sh` は `~/.zshenv` 由来の `BACKLOG_API_KEY` を plist に inject する。
ログは `~/Library/Logs/backlog-board/`。

## ディレクトリ構成

```
backlog-board/
├── cmd/
│   └── server/main.go    # HTTP server (SSR) + 内蔵 fetch goroutine
├── internal/
│   ├── backlog/          # Backlog API クライアント + メンション抽出
│   ├── config/           # TOML 設定ローダ
│   ├── handler/          # gin ハンドラ + index 描画
│   └── store/            # snapshot.json read/write
├── launchd/              # plist テンプレート (__HOME__ 置換あり)
├── scripts/              # install/uninstall-launchd.sh
├── web/
│   ├── embed.go          # templates / static 埋め込み
│   ├── static/css/       # 素の CSS（Tailwind なし）
│   └── templates/        # base.html / index.html
├── config.example.toml
├── Makefile
└── README.md
```

## TODO

- [ ] フィルタ UI（issue_key, sender 等）
- [ ] last-modified ヘッダで条件付き GET
