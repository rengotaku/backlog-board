package handler

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

// mdRenderer は Backlog の課題本文（GFM 寄りの markdown）を安全な HTML へ変換する。
// WithUnsafe は付けない → 本文中の生 HTML はエスケープされ、XSS を防ぐ。
// HardWraps: Backlog の本文は改行をそのまま改行として扱う運用が多いため有効化する。
var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(ghtml.WithHardWraps()),
)

// renderMarkdown は markdown 文字列を HTML に変換する。変換失敗時は
// エスケープした素テキストにフォールバックする。
func renderMarkdown(md string) template.HTML {
	if md == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(md), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(md))
	}
	return template.HTML(buf.String())
}
