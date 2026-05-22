package handler

import "testing"

func TestSafeURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https passthrough", "https://example.com/a", "https://example.com/a"},
		{"http passthrough", "http://example.com/a", "http://example.com/a"},
		{"mailto passthrough", "mailto:me@example.com", "mailto:me@example.com"},
		{"javascript blocked", "javascript:alert(1)", "#"},
		{"javascript mixed case blocked", "JaVaScRiPt:alert(1)", "#"},
		{"data blocked", "data:text/html,<script>alert(1)</script>", "#"},
		{"vbscript blocked", "vbscript:msgbox(1)", "#"},
		{"file blocked", "file:///etc/passwd", "#"},
		{"empty becomes hash", "", "#"},
		{"relative becomes hash (no scheme)", "/foo/bar", "#"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeURL(tt.in); got != tt.want {
				t.Errorf("safeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderInlineBlocksJavascript(t *testing.T) {
	// Backlog コメントに混入した [click](javascript:...) が renderInline を通すと
	// href="javascript:..." として出力されないこと（安全な "#" に置換される）。
	in := `[click](javascript:alert(1))`
	got := renderInline(in)
	if contains(got, "javascript:") {
		t.Errorf("renderInline kept javascript: scheme; out=%q", got)
	}
	if !contains(got, `href="#"`) {
		t.Errorf("renderInline did not replace href with #; out=%q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
