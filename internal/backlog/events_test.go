package backlog

import "testing"

func eventsByType(events []Event, typ string) []Event {
	var out []Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestDiffEventsNilPrevIsBaseline(t *testing.T) {
	snap := &Snapshot{
		FetchedAt: "2026-06-26T09:00:00+09:00",
		Records:   []Record{{NotificationID: 1, CommentID: 10, IssueID: 100}},
		MyIssues:  []MyIssueRecord{{IssueID: 100, IssueStatus: "処理中"}},
	}
	events, archived := DiffEvents(nil, snap)
	if len(events) != 0 || len(archived) != 0 {
		t.Fatalf("prev==nil should emit nothing, got %d events %d archived", len(events), len(archived))
	}
}

func TestDiffEventsMentionReceived(t *testing.T) {
	prev := &Snapshot{Records: []Record{{NotificationID: 1, CommentID: 10, IssueID: 100, IssueKey: "X-1"}}}
	snap := &Snapshot{
		FetchedAt: "2026-06-26T09:15:00+09:00",
		Records: []Record{
			{NotificationID: 1, CommentID: 10, IssueID: 100, IssueKey: "X-1"},
			{NotificationID: 2, CommentID: 20, IssueID: 200, IssueKey: "X-2", Status: StatusUnhandled},
		},
	}
	events, _ := DiffEvents(prev, snap)
	recv := eventsByType(events, EventMentionReceived)
	if len(recv) != 1 {
		t.Fatalf("expected 1 received event, got %d", len(recv))
	}
	got := recv[0]
	if got.NotificationID != 2 || got.CommentID != 20 || got.IssueKey != "X-2" {
		t.Fatalf("unexpected received event: %+v", got)
	}
	if got.Key != "recv:2" {
		t.Fatalf("unexpected dedup key: %q", got.Key)
	}
	if got.V != EventVersion {
		t.Fatalf("expected version %d, got %d", EventVersion, got.V)
	}
	if got.TS != snap.FetchedAt {
		t.Fatalf("expected ts %q, got %q", snap.FetchedAt, got.TS)
	}
}

func TestDiffEventsRepliedAndStarred(t *testing.T) {
	prev := &Snapshot{Records: []Record{{NotificationID: 1, CommentID: 10, IssueID: 100}}}
	snap := &Snapshot{
		FetchedAt: "2026-06-26T09:15:00+09:00",
		Records:   []Record{{NotificationID: 1, CommentID: 10, IssueID: 100, Replied: true, Starred: true}},
	}
	events, _ := DiffEvents(prev, snap)
	if n := len(eventsByType(events, EventMentionReplied)); n != 1 {
		t.Fatalf("expected 1 replied event, got %d", n)
	}
	if n := len(eventsByType(events, EventMentionStarred)); n != 1 {
		t.Fatalf("expected 1 starred event, got %d", n)
	}
	// 既に replied/starred だった場合は再発行しない（冪等）。
	events2, _ := DiffEvents(snap, snap)
	if n := len(eventsByType(events2, EventMentionReplied)); n != 0 {
		t.Fatalf("replied should not re-emit, got %d", n)
	}
	if n := len(eventsByType(events2, EventMentionStarred)); n != 0 {
		t.Fatalf("starred should not re-emit, got %d", n)
	}
}

func TestDiffEventsStatusChanged(t *testing.T) {
	prev := &Snapshot{MyIssues: []MyIssueRecord{{IssueID: 100, IssueKey: "X-1", IssueStatus: "未対応"}}}
	snap := &Snapshot{
		FetchedAt: "2026-06-26T09:15:00+09:00",
		MyIssues:  []MyIssueRecord{{IssueID: 100, IssueKey: "X-1", IssueStatus: "処理中"}},
	}
	events, _ := DiffEvents(prev, snap)
	sc := eventsByType(events, EventStatusChanged)
	if len(sc) != 1 {
		t.Fatalf("expected 1 status_changed, got %d", len(sc))
	}
	if sc[0].From != "未対応" || sc[0].To != "処理中" || sc[0].IssueKey != "X-1" {
		t.Fatalf("unexpected status event: %+v", sc[0])
	}
	if sc[0].Key != "status:100:未対応:処理中" {
		t.Fatalf("unexpected key: %q", sc[0].Key)
	}
}

func TestDiffEventsArchiveOnLeave(t *testing.T) {
	prev := &Snapshot{
		MyIssues:     []MyIssueRecord{{IssueID: 100, IssueKey: "X-1", IssueSummary: "done one", IssueStatus: "処理中", Origin: "assigned"}},
		PassedIssues: []PassedIssueRecord{{IssueID: 300, IssueKey: "X-3", IssueSummary: "passed one"}},
	}
	// snap では両方とも消えた（完了 or 担当替え）。
	snap := &Snapshot{FetchedAt: "2026-06-26T09:15:00+09:00"}
	_, archived := DiffEvents(prev, snap)
	if len(archived) != 2 {
		t.Fatalf("expected 2 archive entries, got %d", len(archived))
	}
	byID := map[int]ArchiveEntry{}
	for _, a := range archived {
		byID[a.IssueID] = a
	}
	if byID[100].Reason != ArchiveAssignedLeft || byID[100].Source != "my_issue" || byID[100].MyIssue == nil {
		t.Fatalf("unexpected assigned archive: %+v", byID[100])
	}
	if byID[300].Reason != ArchivePassedClear || byID[300].Passed == nil {
		t.Fatalf("unexpected passed archive: %+v", byID[300])
	}
}

func TestDiffEventsNoArchiveWhenStillPresent(t *testing.T) {
	prev := &Snapshot{MyIssues: []MyIssueRecord{{IssueID: 100, IssueKey: "X-1"}}}
	// my_issues からは消えたが、まだ mention（records）に残っている → 追跡中なのでアーカイブしない。
	snap := &Snapshot{
		FetchedAt: "2026-06-26T09:15:00+09:00",
		Records:   []Record{{NotificationID: 9, CommentID: 90, IssueID: 100, IssueKey: "X-1"}},
	}
	_, archived := DiffEvents(prev, snap)
	if len(archived) != 0 {
		t.Fatalf("expected no archive while still present in records, got %d", len(archived))
	}
}

func TestDiffEventsNoArchiveWhenPassedStillPresent(t *testing.T) {
	prev := &Snapshot{PassedIssues: []PassedIssueRecord{{IssueID: 300, IssueKey: "X-3"}}}
	// まだ passed に残っている → アーカイブしない。
	snap := &Snapshot{
		FetchedAt:    "2026-06-26T09:15:00+09:00",
		PassedIssues: []PassedIssueRecord{{IssueID: 300, IssueKey: "X-3"}},
	}
	_, archived := DiffEvents(prev, snap)
	if len(archived) != 0 {
		t.Fatalf("expected no archive while still present in passed, got %d", len(archived))
	}
}
