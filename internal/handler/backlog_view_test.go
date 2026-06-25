package handler

import (
	"testing"
	"time"

	"github.com/rengotaku/backlog-board/internal/backlog"
)

func keysOf(views []myIssueView) []string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.IssueKey
	}
	return out
}

func TestBuildMyIssueBacklog(t *testing.T) {
	now := time.Now()
	snap := &backlog.Snapshot{
		MyIssues: []backlog.MyIssueRecord{
			{IssueID: 1, IssueKey: "X-1", UpdatedAt: "2026-06-25T01:00:00Z"},
			{IssueID: 2, IssueKey: "X-2", UpdatedAt: "2026-06-25T03:00:00Z"},
			{IssueID: 3, IssueKey: "X-3", UpdatedAt: "2026-06-25T02:00:00Z"},
		},
	}
	h := &Handler{}

	t.Run("order respected, rest sorted by updated desc", func(t *testing.T) {
		// 優先順は X-3, X-1 を指定。X-2 は未設定。
		top, rest := h.buildMyIssueBacklog(snap, []int{3, 1}, now)
		if got, want := keysOf(top), []string{"X-3", "X-1"}; !equalStrings(got, want) {
			t.Errorf("top = %v, want %v", got, want)
		}
		if got, want := keysOf(rest), []string{"X-2"}; !equalStrings(got, want) {
			t.Errorf("rest = %v, want %v", got, want)
		}
	})

	t.Run("unknown id in order is dropped", func(t *testing.T) {
		// 999 は担当課題に存在しない（完了して担当外れた等）→ 脱落。
		top, rest := h.buildMyIssueBacklog(snap, []int{999, 2}, now)
		if got, want := keysOf(top), []string{"X-2"}; !equalStrings(got, want) {
			t.Errorf("top = %v, want %v", got, want)
		}
		// rest は X-1, X-3 を updated 降順 → X-3(02:00) が X-1(01:00) より先。
		if got, want := keysOf(rest), []string{"X-3", "X-1"}; !equalStrings(got, want) {
			t.Errorf("rest = %v, want %v", got, want)
		}
	})

	t.Run("empty order puts everything in rest by updated desc", func(t *testing.T) {
		top, rest := h.buildMyIssueBacklog(snap, nil, now)
		if len(top) != 0 {
			t.Errorf("top = %v, want empty", keysOf(top))
		}
		if got, want := keysOf(rest), []string{"X-2", "X-3", "X-1"}; !equalStrings(got, want) {
			t.Errorf("rest = %v, want %v", got, want)
		}
	})
}

func TestBuildMyIssueBacklog_MergesExtraAndStale(t *testing.T) {
	now := time.Now()
	snap := &backlog.Snapshot{
		MyIssues: []backlog.MyIssueRecord{
			{IssueID: 1, IssueKey: "X-1", UpdatedAt: "2026-06-25T01:00:00Z", Origin: "assigned"},
		},
		BacklogExtra: []backlog.MyIssueRecord{
			{IssueID: 2, IssueKey: "X-2", UpdatedAt: "2026-06-25T05:00:00Z", Origin: "category"},
			// 1 は担当課題と重複 → dedup で落ちる
			{IssueID: 1, IssueKey: "X-1", UpdatedAt: "2026-06-25T01:00:00Z", Origin: "category"},
		},
		BacklogStale: []backlog.MyIssueRecord{
			{IssueID: 3, IssueKey: "X-3", UpdatedAt: "2026-06-25T02:00:00Z", Origin: "stale", Stale: true},
		},
	}
	h := &Handler{}

	t.Run("stale id in order shows in top, flagged", func(t *testing.T) {
		top, rest := h.buildMyIssueBacklog(snap, []int{3, 1}, now)
		if got, want := keysOf(top), []string{"X-3", "X-1"}; !equalStrings(got, want) {
			t.Fatalf("top = %v, want %v", got, want)
		}
		if !top[0].Stale {
			t.Errorf("X-3 should be Stale")
		}
		// category X-2 は order に無い → rest
		if got, want := keysOf(rest), []string{"X-2"}; !equalStrings(got, want) {
			t.Errorf("rest = %v, want %v", got, want)
		}
	})

	t.Run("no order: all three sources land in rest by updated desc, deduped", func(t *testing.T) {
		_, rest := h.buildMyIssueBacklog(snap, nil, now)
		// updated desc: X-2(05) > X-3(02) > X-1(01)
		if got, want := keysOf(rest), []string{"X-2", "X-3", "X-1"}; !equalStrings(got, want) {
			t.Errorf("rest = %v, want %v", got, want)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
