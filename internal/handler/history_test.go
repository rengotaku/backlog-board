package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rengotaku/backlog-board/internal/backlog"
	"github.com/rengotaku/backlog-board/internal/store"
)

func newHistoryRouter(t *testing.T) (http.Handler, *store.EventLog) {
	t.Helper()
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapshot.json")
	ev := store.NewEventLog(snapPath)
	if err := ev.AppendEvents([]backlog.Event{
		{V: 1, Type: backlog.EventMentionReceived, TS: "2026-05-10T09:00:00+09:00", Key: "r1", IssueID: 100, IssueKey: "X-1"},
		{V: 1, Type: backlog.EventStatusChanged, TS: "2026-06-10T09:00:00+09:00", Key: "s1", IssueID: 100, IssueKey: "X-1", From: "未対応", To: "処理中"},
		{V: 1, Type: backlog.EventMentionReceived, TS: "2026-06-11T09:00:00+09:00", Key: "r2", IssueID: 200, IssueKey: "X-2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ev.AppendArchive([]backlog.ArchiveEntry{
		{V: 1, ArchivedAt: "2026-06-26T09:00:00+09:00", Reason: backlog.ArchiveAssignedLeft, IssueID: 100, IssueKey: "X-1"},
		{V: 1, ArchivedAt: "2026-06-26T10:00:00+09:00", Reason: backlog.ArchivePassedClear, IssueID: 300, IssueKey: "X-3"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(store.New(snapPath), map[string]*template.Template{}, os.DirFS(dir), nil, Options{Events: ev})
	return h.Routes(), ev
}

func getJSON(t *testing.T, router http.Handler, url string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var body map[string]json.RawMessage
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &body)
	}
	return w.Code, body
}

func TestAPIArchiveSortedDesc(t *testing.T) {
	router, _ := newHistoryRouter(t)
	code, body := getJSON(t, router, "/api/archive")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var entries []backlog.ArchiveEntry
	if err := json.Unmarshal(body["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// 新しい順（10:00 が先）。
	if entries[0].IssueID != 300 {
		t.Fatalf("expected newest (issue 300) first, got %d", entries[0].IssueID)
	}
}

func TestAPIHistoryByIssue(t *testing.T) {
	router, _ := newHistoryRouter(t)
	code, body := getJSON(t, router, "/api/history?issue_id=100")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var events []backlog.Event
	if err := json.Unmarshal(body["events"], &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events for issue 100, got %d", len(events))
	}
	// 新しい順（6 月の status_changed が先）。
	if events[0].Type != backlog.EventStatusChanged {
		t.Fatalf("expected newest event first, got %s", events[0].Type)
	}
}

func TestAPIHistoryByMonth(t *testing.T) {
	router, _ := newHistoryRouter(t)
	code, body := getJSON(t, router, "/api/history?month=2026-06")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var events []backlog.Event
	if err := json.Unmarshal(body["events"], &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 june events, got %d", len(events))
	}
}

func TestAPIHistoryDefaultsToLatestMonth(t *testing.T) {
	router, _ := newHistoryRouter(t)
	code, body := getJSON(t, router, "/api/history")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var month string
	if err := json.Unmarshal(body["month"], &month); err != nil {
		t.Fatal(err)
	}
	if month != "2026-06" {
		t.Fatalf("expected latest month 2026-06, got %q", month)
	}
}

func TestAPIHistoryRejectsBadParams(t *testing.T) {
	router, _ := newHistoryRouter(t)
	// パストラバーサルを狙う month は弾く。
	if code, _ := getJSON(t, router, "/api/history?month=..%2F..%2Fetc"); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal month, got %d", code)
	}
	if code, _ := getJSON(t, router, "/api/history?month=2026-6"); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed month, got %d", code)
	}
	if code, _ := getJSON(t, router, "/api/history?issue_id=abc"); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad issue_id, got %d", code)
	}
}
