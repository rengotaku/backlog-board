package web

import (
	"html/template"
	"testing"
)

// TestTemplatesParse は埋め込み済みテンプレートが base.html と組で正しくパースできることを保証する。
// loadTemplates (cmd/server) と同じ組み合わせを検証し、テンプレート構文エラーを
// 実行時ではなくテスト時に検出する。
func TestTemplatesParse(t *testing.T) {
	pages := []string{
		"templates/index.html",
		"templates/history.html",
	}
	for _, p := range pages {
		if _, err := template.ParseFS(FS, "templates/base.html", p); err != nil {
			t.Fatalf("parse %s with base: %v", p, err)
		}
	}
}
