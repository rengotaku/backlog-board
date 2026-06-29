package backlog

import "testing"

func TestHasReplyAfter(t *testing.T) {
	const me = 417740
	const other = 360342
	const notifiedAt = "2026-05-27T10:06:38Z"

	tests := []struct {
		name     string
		comments []Comment
		want     bool
	}{
		{
			name: "本文付きの自分のコメントが通知後にある → 返信済",
			comments: []Comment{
				{ID: 1, Content: "@剣持 ご確認ありがとうございます", Created: "2026-05-27T11:00:00Z", CreatedUser: &User{ID: me}},
			},
			want: true,
		},
		{
			name: "本文空(実績時間入力等の changeLog のみ)の自分のコメントは返信とみなさない",
			comments: []Comment{
				{ID: 1, Content: "", Created: "2026-05-28T00:22:43Z", CreatedUser: &User{ID: me}, ChangeLog: []ChangeLog{{Field: "actualHours"}}},
			},
			want: false,
		},
		{
			name: "本文付きでも通知より前なら返信済にしない",
			comments: []Comment{
				{ID: 1, Content: "先に書いた返信", Created: "2026-05-27T09:50:58Z", CreatedUser: &User{ID: me}},
			},
			want: false,
		},
		{
			name: "他人のコメントは返信にカウントしない",
			comments: []Comment{
				{ID: 1, Content: "@岸良 ありがとうございます", Created: "2026-05-27T11:00:00Z", CreatedUser: &User{ID: other}},
			},
			want: false,
		},
		{
			name: "空白のみの本文も返信とみなさない",
			comments: []Comment{
				{ID: 1, Content: "   \n  ", Created: "2026-05-27T11:00:00Z", CreatedUser: &User{ID: me}},
			},
			want: false,
		},
		{
			name: "空イベント後に本文付き返信があれば返信済",
			comments: []Comment{
				{ID: 1, Content: "", Created: "2026-05-27T11:00:00Z", CreatedUser: &User{ID: me}, ChangeLog: []ChangeLog{{Field: "actualHours"}}},
				{ID: 2, Content: "改めて返信します", Created: "2026-05-27T12:00:00Z", CreatedUser: &User{ID: me}},
			},
			want: true,
		},
		{
			name: "本文付き返信の後に本文なし担当変更をしても返信済のまま",
			comments: []Comment{
				{ID: 1, Content: "@剣持 確認しました、対応します", Created: "2026-05-27T11:00:00Z", CreatedUser: &User{ID: me}},
				{ID: 2, Content: "", Created: "2026-05-27T11:05:00Z", CreatedUser: &User{ID: me}, ChangeLog: []ChangeLog{{Field: "assigner"}}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasReplyAfter(tt.comments, me, notifiedAt); got != tt.want {
				t.Errorf("hasReplyAfter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveParentsFromKnown(t *testing.T) {
	c := &Client{Domain: "example.backlog.com"}
	known := map[int]Issue{
		100: {ID: 100, IssueKey: "DSC-100", Summary: "親課題"},
	}
	records := []MyIssueRecord{
		{IssueID: 1, IssueKey: "DSC-1", ParentIssueID: 100},                            // known から解決
		{IssueID: 2, IssueKey: "DSC-2", ParentIssueID: 100},                            // 同じ親（dedup）
		{IssueID: 3, IssueKey: "DSC-3"},                                                // 親なし → 変化しない
		{IssueID: 4, IssueKey: "DSC-4", ParentIssueID: 100, ParentIssueKey: "DSC-100"}, // 解決済み → 変化しない
	}

	c.resolveParents(records, known)

	if records[0].ParentIssueKey != "DSC-100" {
		t.Errorf("records[0] ParentIssueKey = %q, want DSC-100", records[0].ParentIssueKey)
	}
	wantURL := "https://example.backlog.com/view/DSC-100"
	if records[0].ParentIssueURL != wantURL {
		t.Errorf("records[0] ParentIssueURL = %q, want %q", records[0].ParentIssueURL, wantURL)
	}
	if records[0].ParentIssueSummary != "親課題" {
		t.Errorf("records[0] ParentIssueSummary = %q, want 親課題", records[0].ParentIssueSummary)
	}
	if records[1].ParentIssueKey != "DSC-100" {
		t.Errorf("records[1] ParentIssueKey = %q, want DSC-100 (dedup)", records[1].ParentIssueKey)
	}
	if records[2].ParentIssueKey != "" || records[2].ParentIssueURL != "" {
		t.Errorf("records[2] should stay empty (no parent), got key=%q url=%q", records[2].ParentIssueKey, records[2].ParentIssueURL)
	}
	if records[3].ParentIssueURL != "" {
		t.Errorf("records[3] already-resolved should not be touched, got url=%q", records[3].ParentIssueURL)
	}
}
