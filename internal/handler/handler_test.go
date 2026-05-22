package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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
