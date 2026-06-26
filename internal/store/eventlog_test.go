package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rengotaku/backlog-board/internal/backlog"
)

func TestEventLogMonthlyRotation(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))

	events := []backlog.Event{
		{V: 1, Type: backlog.EventMentionReceived, TS: "2026-06-26T09:15:00+09:00", Key: "recv:1", NotificationID: 1},
		{V: 1, Type: backlog.EventMentionReplied, TS: "2026-06-27T10:00:00+09:00", Key: "reply:2", CommentID: 2},
		{V: 1, Type: backlog.EventStatusChanged, TS: "2026-07-01T08:00:00+09:00", Key: "status:3:a:b", IssueID: 3},
	}
	if err := l.AppendEvents(events); err != nil {
		t.Fatalf("append: %v", err)
	}

	june, err := l.ReadEvents("2026-06")
	if err != nil {
		t.Fatalf("read june: %v", err)
	}
	if len(june) != 2 {
		t.Fatalf("expected 2 june events, got %d", len(june))
	}
	july, err := l.ReadEvents("2026-07")
	if err != nil {
		t.Fatalf("read july: %v", err)
	}
	if len(july) != 1 {
		t.Fatalf("expected 1 july event, got %d", len(july))
	}

	// 月ファイルが実際に分かれていること。
	if _, err := os.Stat(filepath.Join(dir, "history", "events-2026-06.jsonl")); err != nil {
		t.Fatalf("june file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history", "events-2026-07.jsonl")); err != nil {
		t.Fatalf("july file missing: %v", err)
	}
}

func TestEventLogAppendAccumulates(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	first := []backlog.Event{{V: 1, Type: "x", TS: "2026-06-26T09:00:00+09:00", Key: "a"}}
	second := []backlog.Event{{V: 1, Type: "y", TS: "2026-06-26T09:30:00+09:00", Key: "b"}}
	if err := l.AppendEvents(first); err != nil {
		t.Fatal(err)
	}
	if err := l.AppendEvents(second); err != nil {
		t.Fatal(err)
	}
	got, err := l.ReadEvents("2026-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected append-only accumulation of 2, got %d", len(got))
	}
}

func TestEventLogPermissions(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	if err := l.AppendEvents([]backlog.Event{{V: 1, Type: "x", TS: "2026-06-26T09:00:00+09:00", Key: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.AppendArchive([]backlog.ArchiveEntry{{V: 1, ArchivedAt: "2026-06-26T09:00:00+09:00", IssueID: 1}}); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(filepath.Join(dir, "history"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("history dir perm = %o, want 700", perm)
	}
	for _, name := range []string{"events-2026-06.jsonl", "archive.jsonl"} {
		fi, err := os.Stat(filepath.Join(dir, "history", name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s perm = %o, want 600", name, perm)
		}
	}
}

func TestEventLogReadSkipsCorruptLine(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	if err := l.AppendEvents([]backlog.Event{{V: 1, Type: "x", TS: "2026-06-26T09:00:00+09:00", Key: "a"}}); err != nil {
		t.Fatal(err)
	}
	// 途中書き込みで壊れた末尾行を模す。
	f, err := os.OpenFile(filepath.Join(dir, "history", "events-2026-06.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"type":"x","ts":"2026-06`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := l.ReadEvents("2026-06")
	if err != nil {
		t.Fatalf("read should tolerate corrupt line: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 valid event (corrupt skipped), got %d", len(got))
	}
}

func TestEventLogReadMissingMonth(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	got, err := l.ReadEvents("2099-01")
	if err != nil {
		t.Fatalf("missing month should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events, got %d", len(got))
	}
}

func TestEventLogReadArchive(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	entries := []backlog.ArchiveEntry{
		{V: 1, ArchivedAt: "2026-06-26T09:00:00+09:00", Reason: backlog.ArchiveAssignedLeft, IssueID: 100, IssueKey: "X-1"},
		{V: 1, ArchivedAt: "2026-06-26T10:00:00+09:00", Reason: backlog.ArchivePassedClear, IssueID: 300, IssueKey: "X-3"},
	}
	if err := l.AppendArchive(entries); err != nil {
		t.Fatal(err)
	}
	got, err := l.ReadArchive()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 archive entries, got %d", len(got))
	}
	// 未作成時は空。
	got2, err := NewEventLog(filepath.Join(t.TempDir(), "snapshot.json")).ReadArchive()
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("expected empty archive, got %d", len(got2))
	}
}

func TestEventLogListEventMonths(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	events := []backlog.Event{
		{V: 1, Type: "x", TS: "2026-07-01T09:00:00+09:00", Key: "a"},
		{V: 1, Type: "x", TS: "2026-05-01T09:00:00+09:00", Key: "b"},
		{V: 1, Type: "x", TS: "2026-06-01T09:00:00+09:00", Key: "c"},
	}
	if err := l.AppendEvents(events); err != nil {
		t.Fatal(err)
	}
	months, err := l.ListEventMonths()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-05", "2026-06", "2026-07"}
	if len(months) != len(want) {
		t.Fatalf("expected %v, got %v", want, months)
	}
	for i := range want {
		if months[i] != want[i] {
			t.Fatalf("months not sorted ascending: got %v", months)
		}
	}
}

func TestEventLogReadEventsForIssue(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	events := []backlog.Event{
		{V: 1, Type: backlog.EventMentionReceived, TS: "2026-05-10T09:00:00+09:00", Key: "r1", IssueID: 100},
		{V: 1, Type: backlog.EventStatusChanged, TS: "2026-06-10T09:00:00+09:00", Key: "s1", IssueID: 100, From: "未対応", To: "処理中"},
		{V: 1, Type: backlog.EventMentionReceived, TS: "2026-06-11T09:00:00+09:00", Key: "r2", IssueID: 200},
	}
	if err := l.AppendEvents(events); err != nil {
		t.Fatal(err)
	}
	got, err := l.ReadEventsForIssue(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events for issue 100 across months, got %d", len(got))
	}
	for _, e := range got {
		if e.IssueID != 100 {
			t.Fatalf("got event for wrong issue: %+v", e)
		}
	}
}

func TestEventLogEmptyIsNoop(t *testing.T) {
	dir := t.TempDir()
	l := NewEventLog(filepath.Join(dir, "snapshot.json"))
	if err := l.AppendEvents(nil); err != nil {
		t.Fatal(err)
	}
	if err := l.AppendArchive(nil); err != nil {
		t.Fatal(err)
	}
	// 空 append では history ディレクトリすら作らない。
	if _, err := os.Stat(filepath.Join(dir, "history")); !os.IsNotExist(err) {
		t.Fatalf("expected no history dir for empty appends, err=%v", err)
	}
}
