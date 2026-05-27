package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSafeURL_NoAllowlist(t *testing.T) {
	h := &Handler{} // linkAllowPrefixes 未設定 → scheme チェックのみ
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
			if got := h.safeURL(tt.in); got != tt.want {
				t.Errorf("safeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeURL_WithAllowlist(t *testing.T) {
	// 「自テナント (Backlog) + 業務 GitHub repo のみ許可」のシナリオ。
	h := &Handler{linkAllowPrefixes: []string{
		"https://jccapital.backlog.com/",
		"https://github.com/jccapital/fundoor/",
	}}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"backlog tenant root", "https://jccapital.backlog.com/view/X-1", "https://jccapital.backlog.com/view/X-1"},
		{"github allowed repo path", "https://github.com/jccapital/fundoor/pull/1", "https://github.com/jccapital/fundoor/pull/1"},
		{"github other repo blocked", "https://github.com/jccapital/other-repo/pull/1", "#"},
		{"unrelated https blocked", "https://example.com/", "#"},
		{"unrelated http blocked", "http://example.com/", "#"},
		{"mailto blocked when allowlist set", "mailto:me@example.com", "#"},
		{"javascript still blocked", "javascript:alert(1)", "#"},
		{"prefix substring not matched", "https://jccapital.backlog.com.evil.example/", "#"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := h.safeURL(tt.in); got != tt.want {
				t.Errorf("safeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderInlineBlocksJavascript(t *testing.T) {
	// Backlog コメントに混入した [click](javascript:...) が renderInline を通すと
	// href="javascript:..." として出力されないこと（安全な "#" に置換される）。
	h := &Handler{}
	in := `[click](javascript:alert(1))`
	got := h.renderInline(in)
	if contains(got, "javascript:") {
		t.Errorf("renderInline kept javascript: scheme; out=%q", got)
	}
	if !contains(got, `href="#"`) {
		t.Errorf("renderInline did not replace href with #; out=%q", got)
	}
}

func TestRenderInline_AutolinkBareURL_NoAllowlist(t *testing.T) {
	h := &Handler{}
	in := `see https://github.com/jccapital/fundoor/pull/20660#pullrequestreview-4342025233 for review`
	got := h.renderInline(in)
	want := `href="https://github.com/jccapital/fundoor/pull/20660#pullrequestreview-4342025233"`
	if !contains(got, want) {
		t.Errorf("expected autolinked anchor; got=%q", got)
	}
}

func TestRenderInline_AutolinkBareURL_WithAllowlist(t *testing.T) {
	h := &Handler{linkAllowPrefixes: []string{
		"https://jccapital.backlog.com/",
		"https://github.com/jccapital/fundoor/",
	}}
	in := `inside https://github.com/jccapital/fundoor/pull/1 and outside https://external.example/x`
	got := h.renderInline(in)
	if !contains(got, `href="https://github.com/jccapital/fundoor/pull/1"`) {
		t.Errorf("allowed URL should be linkified; got=%q", got)
	}
	if contains(got, `href="https://external.example/x"`) {
		t.Errorf("disallowed URL must not be linkified; got=%q", got)
	}
	if !contains(got, `https://external.example/x`) {
		t.Errorf("disallowed URL should still appear as plain text; got=%q", got)
	}
	if contains(got, `href="#"`) {
		t.Errorf("disallowed URL should NOT produce href=#; got=%q", got)
	}
}

func TestRenderInline_NoDoubleLinkInsideMarkdownLink(t *testing.T) {
	// [text](url) ブロック内の URL が autolink で 2 重リンク化されないこと
	h := &Handler{}
	in := `[click](https://github.com/jccapital/fundoor/pull/1)`
	got := h.renderInline(in)
	// <a ...>click</a> が 1 つだけで、URL を含む追加の <a> が無い
	if got != `<a href="https://github.com/jccapital/fundoor/pull/1" target="_blank" rel="noopener">click</a>` {
		t.Errorf("unexpected output (possible double-link); got=%q", got)
	}
}

func TestAutolinkAndEscape_PreservesEscape(t *testing.T) {
	// ContentExcerpt 用途で template.HTML として出すため、HTML 特殊文字が
	// 通常通りエスケープされること（XSS にならないこと）を確認する。
	h := &Handler{}
	got := h.autolinkAndEscape(`<script>x</script> see https://example.com/?q=<x> end`)
	if contains(got, `<script>`) {
		t.Errorf("must escape literal <script>; got=%q", got)
	}
	if !contains(got, `href="https://example.com/?q=&lt;x&gt;"`) && !contains(got, `href="https://example.com/?q=`) {
		t.Errorf("URL should be linkified with escaped href; got=%q", got)
	}
}

func TestTrimURLTrailingPunct(t *testing.T) {
	tests := []struct {
		in        string
		wantCore  string
		wantTrail string
	}{
		{"https://example.com/path", "https://example.com/path", ""},
		{"https://example.com/path.", "https://example.com/path", "."},
		{"https://example.com/path,", "https://example.com/path", ","},
		{"https://example.com/path).", "https://example.com/path", ")."},
		{"https://en.wikipedia.org/wiki/Go_(programming_language)", "https://en.wikipedia.org/wiki/Go_(programming_language)", ""},
		{"https://example.com/x?a=1!", "https://example.com/x?a=1", "!"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			core, trail := trimURLTrailingPunct(tt.in)
			if core != tt.wantCore || trail != tt.wantTrail {
				t.Errorf("trimURLTrailingPunct(%q) = (%q, %q), want (%q, %q)", tt.in, core, trail, tt.wantCore, tt.wantTrail)
			}
		})
	}
}

func TestRenderInline_AllowlistEnforced(t *testing.T) {
	h := &Handler{linkAllowPrefixes: []string{"https://jccapital.backlog.com/"}}
	got := h.renderInline(`[blocked](https://external.example/x) [ok](https://jccapital.backlog.com/view/X-1)`)
	if contains(got, `href="https://external.example/x"`) {
		t.Errorf("external link should be neutralized; out=%q", got)
	}
	if !contains(got, `href="#"`) {
		t.Errorf("external link should become href=#; out=%q", got)
	}
	if !contains(got, `href="https://jccapital.backlog.com/view/X-1"`) {
		t.Errorf("allowed link should pass through; out=%q", got)
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

func init() { gin.SetMode(gin.TestMode) }

func newTestRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	for _, m := range mw {
		r.Use(m)
	}
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.POST("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func TestRequireAllowedHost(t *testing.T) {
	allowed := []string{"127.0.0.1:8082", "localhost:8082"}
	r := newTestRouter(requireAllowedHost(allowed))

	tests := []struct {
		host string
		want int
	}{
		{"127.0.0.1:8082", http.StatusOK},
		{"localhost:8082", http.StatusOK},
		{"attacker.example:8082", http.StatusForbidden},
		{"127.0.0.1:80", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Errorf("host=%q code=%d want=%d", tt.host, w.Code, tt.want)
			}
		})
	}
}

func TestRequireSameOrigin(t *testing.T) {
	allowed := []string{"http://127.0.0.1:8082", "http://localhost:8082"}
	r := newTestRouter(requireSameOrigin(allowed))

	// GET は Origin 検証スキップ
	t.Run("GET no origin passes", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET without Origin: code=%d want=200", w.Code)
		}
	})

	postCases := []struct {
		name   string
		origin string
		ref    string
		want   int
	}{
		{"POST same origin Origin", "http://127.0.0.1:8082", "", http.StatusOK},
		{"POST same origin Referer", "", "http://127.0.0.1:8082/foo", http.StatusOK},
		{"POST attacker", "http://evil.example", "", http.StatusForbidden},
		{"POST no origin or referer", "", "", http.StatusForbidden},
		{"POST localhost origin", "http://localhost:8082", "", http.StatusOK},
	}
	for _, tt := range postCases {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.ref != "" {
				req.Header.Set("Referer", tt.ref)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Errorf("%s: code=%d want=%d", tt.name, w.Code, tt.want)
			}
		})
	}
}

func TestStaleLabel(t *testing.T) {
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		iso  string
		want string
	}{
		{"45min ago", now.Add(-45 * time.Minute).Format(time.RFC3339), "⚠ stale (45分 fetch 停止)"},
		{"3h ago", now.Add(-3 * time.Hour).Format(time.RFC3339), "⚠ stale (3時間 fetch 停止)"},
		{"7h ago", now.Add(-7 * time.Hour).Format(time.RFC3339), "⚠ stale (7時間 fetch 停止)"},
		{"2d ago", now.Add(-50 * time.Hour).Format(time.RFC3339), "⚠ stale (2日 fetch 停止)"},
		{"unparseable", "not-a-date", "⚠ stale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := staleLabel(now, tt.iso)
			if got != tt.want {
				t.Errorf("staleLabel(%q) = %q, want %q", tt.iso, got, tt.want)
			}
		})
	}
}
