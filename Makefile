.PHONY: setup build run stop status lint test test-cov check ci clean help

.DEFAULT_GOAL := help

BINARY_DIR := bin
SERVER_BIN := $(BINARY_DIR)/backlog-board-server
PORT ?= 8082
COVERAGE_FILE := coverage.out

CONFIG_HOME := $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)
CONFIG_DIR := $(CONFIG_HOME)/backlog-board
CONFIG_FILE := $(CONFIG_DIR)/config.toml

## setup: 初回セットアップ（config.toml 配置 + 依存取得 + 環境変数チェック）
setup:
	@install -d -m 700 $(CONFIG_DIR)
	@chmod 700 $(CONFIG_DIR)
	@if [ -f $(CONFIG_FILE) ]; then \
		echo "config: $(CONFIG_FILE) (already exists, enforce perms)"; \
	else \
		cp config.example.toml $(CONFIG_FILE); \
		echo "config: $(CONFIG_FILE) (created)"; \
		echo "  → domain を自分の Backlog スペースに書き換えてください"; \
	fi
	@chmod 600 $(CONFIG_FILE)
	@go mod download
	@go mod tidy
	@if [ -z "$$BACKLOG_API_KEY" ]; then \
		echo ""; \
		echo "WARN: BACKLOG_API_KEY が未設定です"; \
		echo "  ~/.zshenv 等で export BACKLOG_API_KEY=\"...\" を追加してください"; \
	fi

## build: server をビルド
build:
	@mkdir -p $(BINARY_DIR)
	go build -o $(SERVER_BIN) ./cmd/server

## run: server を起動 (PORT=8082)
run:
	go run ./cmd/server

## stop: PORT を握っているプロセスを kill
stop:
	@lsof -ti :$(PORT) | xargs kill 2>/dev/null || true

## status: PORT で待ち受け中か確認
status:
	@lsof -i :$(PORT) >/dev/null 2>&1 && echo "backlog-board: running (:$(PORT))" || echo "backlog-board: stopped"

## lint: golangci-lint を実行
lint:
	golangci-lint run

## test: 全テスト
test:
	go test ./...

## test-cov: カバレッジ付きテスト
test-cov:
	go test -coverprofile=$(COVERAGE_FILE) ./...
	go tool cover -func=$(COVERAGE_FILE)

## check: lint + test
check: lint test

## ci: lint + test-cov
ci: lint test-cov

## clean: ビルド成果物を削除
clean:
	rm -rf $(BINARY_DIR)/ $(COVERAGE_FILE)

## help: ターゲット一覧
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
