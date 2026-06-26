package backlog

import "fmt"

// EventVersion は cold 層レコードのスキーマ版。append-only でスキーマが育つ前提のため、
// 各行に持たせて将来の reader が旧行を判別できるようにする。
const EventVersion = 1

// イベント種別。snapshot の連続ペアの差分から導出する。
const (
	EventMentionReceived = "mention_received"
	EventMentionReplied  = "mention_replied"
	EventMentionStarred  = "mention_starred"
	EventStatusChanged   = "status_changed"
)

// アーカイブ理由（prev で追跡していた安定集合のどこから外れたか）。
const (
	ArchiveAssignedLeft = "assigned_left"  // 担当課題（origin=assigned）が取得対象から消えた（完了 or 担当替え）
	ArchiveCategoryLeft = "category_left"  // カテゴリ取込課題が消えた（完了 or カテゴリ設定変更）
	ArchiveStaleLeft    = "stale_left"     // stale 残留課題が消えた（完了）
	ArchivePassedClear  = "passed_cleared" // パス済みが消えた（自分へ戻った or 完了）
)

// Event は cold 層イベントログ 1 行分。本文は持たず参照＋メタのみ（1 行 200〜400B 想定）。
// Key は決定的な dedup キー。連続 snapshot を毎回差分するため通常は重複しないが、
// 「append 後・snapshot 保存前のクラッシュ → 再起動時の再 fetch」でのみ同一イベントが
// 二重 append され得る。その際は Key で下流が dedup できる。
type Event struct {
	V              int    `json:"v"`
	Type           string `json:"type"`
	TS             string `json:"ts"`
	Key            string `json:"key"`
	IssueID        int    `json:"issue_id,omitempty"`
	IssueKey       string `json:"issue_key,omitempty"`
	CommentID      int    `json:"comment_id,omitempty"`
	NotificationID int64  `json:"notification_id,omitempty"`
	Status         string `json:"status,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
}

// ArchiveEntry は追跡対象から外れた課題の最終既知状態。完了/パス課題を後から振り返るための保存。
// 低頻度（課題が外れた時のみ）なのでコメント履歴込みの重い record を丸ごと保持してよい。
type ArchiveEntry struct {
	V            int                `json:"v"`
	ArchivedAt   string             `json:"archived_at"`
	Reason       string             `json:"reason"`
	Source       string             `json:"source"` // my_issue|backlog_extra|backlog_stale|passed
	IssueID      int                `json:"issue_id"`
	IssueKey     string             `json:"issue_key"`
	IssueSummary string             `json:"issue_summary,omitempty"`
	LastStatus   string             `json:"last_status,omitempty"`
	MyIssue      *MyIssueRecord     `json:"my_issue,omitempty"`
	Passed       *PassedIssueRecord `json:"passed,omitempty"`
}

// DiffEvents は連続する 2 つの snapshot から差分イベントとアーカイブ対象を導出する純粋関数。
//
// prev==nil（初回起動 or cache 削除直後）は「ベースライン投入」とみなし何も発行しない。
// 過去の状態を知らないため received/replied/archived を一斉発行すると実時刻と乖離したノイズに
// なるからで、イベント記録は 2 回目の fetch 以降に開始する（バックフィルは不可）。
func DiffEvents(prev, snap *Snapshot) (events []Event, archived []ArchiveEntry) {
	if prev == nil || snap == nil {
		return nil, nil
	}
	ts := snap.FetchedAt

	// --- mention イベント（Records の差分） ---
	// received は通知単位（NotificationID）、replied/starred は対象コメント単位（CommentID）で判定する。
	// 返信/★ は「そのコメントに対して」一度行えば済む行為なので、同一コメントに別通知が来ても
	// 前回 replied/starred 済みなら再発行しない（実際に行われた行為の時刻と乖離させない）。
	prevNotif := make(map[int64]bool, len(prev.Records))
	prevReplied := make(map[int]bool, len(prev.Records))
	prevStarred := make(map[int]bool, len(prev.Records))
	for i := range prev.Records {
		r := &prev.Records[i]
		prevNotif[r.NotificationID] = true
		if r.Replied {
			prevReplied[r.CommentID] = true
		}
		if r.Starred {
			prevStarred[r.CommentID] = true
		}
	}

	emittedReply := map[int]bool{}
	emittedStar := map[int]bool{}
	for i := range snap.Records {
		r := &snap.Records[i]
		if !prevNotif[r.NotificationID] {
			events = append(events, Event{
				V: EventVersion, Type: EventMentionReceived, TS: ts,
				Key:            fmt.Sprintf("recv:%d", r.NotificationID),
				IssueID:        r.IssueID,
				IssueKey:       r.IssueKey,
				CommentID:      r.CommentID,
				NotificationID: r.NotificationID,
				Status:         r.Status,
			})
		}
		if r.Replied && !prevReplied[r.CommentID] && !emittedReply[r.CommentID] {
			emittedReply[r.CommentID] = true
			events = append(events, Event{
				V: EventVersion, Type: EventMentionReplied, TS: ts,
				Key:       fmt.Sprintf("reply:%d", r.CommentID),
				IssueID:   r.IssueID,
				IssueKey:  r.IssueKey,
				CommentID: r.CommentID,
			})
		}
		if r.Starred && !prevStarred[r.CommentID] && !emittedStar[r.CommentID] {
			emittedStar[r.CommentID] = true
			events = append(events, Event{
				V: EventVersion, Type: EventMentionStarred, TS: ts,
				Key:       fmt.Sprintf("star:%d", r.CommentID),
				IssueID:   r.IssueID,
				IssueKey:  r.IssueKey,
				CommentID: r.CommentID,
			})
		}
	}

	// --- status_changed（issue 単位のステータス遷移） ---
	prevStatus := statusByIssue(prev)
	snapStatus := statusByIssue(snap)
	keyByIssue := issueKeyByIssue(snap)
	for id, to := range snapStatus {
		from, ok := prevStatus[id]
		if !ok || from == to || to == "" {
			continue
		}
		events = append(events, Event{
			V: EventVersion, Type: EventStatusChanged, TS: ts,
			Key:      fmt.Sprintf("status:%d:%s:%s", id, from, to),
			IssueID:  id,
			IssueKey: keyByIssue[id],
			From:     from,
			To:       to,
		})
	}

	// --- archive（prev で追跡していた安定集合から消えた課題） ---
	present := presentIssueIDs(snap)
	seen := map[int]bool{}
	addArchive := func(source, reason string, id int, key, summary, status string, mi *MyIssueRecord, pi *PassedIssueRecord) {
		if id <= 0 || present[id] || seen[id] {
			return
		}
		seen[id] = true
		archived = append(archived, ArchiveEntry{
			V: EventVersion, ArchivedAt: ts, Reason: reason, Source: source,
			IssueID: id, IssueKey: key, IssueSummary: summary, LastStatus: status,
			MyIssue: mi, Passed: pi,
		})
	}
	// 優先度: 担当 > パス > カテゴリ > stale（同一課題が複数集合にいた場合に意味の強い理由を採る）。
	// addArchive は seen で先着勝ちにするため、呼び出し順 = 優先度の降順にする。
	for i := range prev.MyIssues {
		m := prev.MyIssues[i]
		addArchive("my_issue", ArchiveAssignedLeft, m.IssueID, m.IssueKey, m.IssueSummary, m.IssueStatus, &m, nil)
	}
	for i := range prev.PassedIssues {
		p := prev.PassedIssues[i]
		addArchive("passed", ArchivePassedClear, p.IssueID, p.IssueKey, p.IssueSummary, "", nil, &p)
	}
	for i := range prev.BacklogExtra {
		m := prev.BacklogExtra[i]
		addArchive("backlog_extra", ArchiveCategoryLeft, m.IssueID, m.IssueKey, m.IssueSummary, m.IssueStatus, &m, nil)
	}
	for i := range prev.BacklogStale {
		m := prev.BacklogStale[i]
		addArchive("backlog_stale", ArchiveStaleLeft, m.IssueID, m.IssueKey, m.IssueSummary, m.IssueStatus, &m, nil)
	}

	return events, archived
}

// statusByIssue は snapshot から issue_id → status を構築する。
// records を先に置き、my_issues / backlog_extra / backlog_stale で上書きする（API 直取得の
// 課題ステータスを通知由来より優先）。この 3 集合は Fetch 側で targetIDs により互いに素に
// 構築されるため、3 集合間での上書き衝突は起きない。
func statusByIssue(s *Snapshot) map[int]string {
	m := make(map[int]string)
	for i := range s.Records {
		r := &s.Records[i]
		if r.IssueStatus != "" {
			m[r.IssueID] = r.IssueStatus
		}
	}
	overlay := func(rs []MyIssueRecord) {
		for i := range rs {
			if rs[i].IssueStatus != "" {
				m[rs[i].IssueID] = rs[i].IssueStatus
			}
		}
	}
	overlay(s.MyIssues)
	overlay(s.BacklogExtra)
	overlay(s.BacklogStale)
	return m
}

// issueKeyByIssue は snapshot から issue_id → issue_key を構築する（status_changed の補助情報用）。
func issueKeyByIssue(s *Snapshot) map[int]string {
	m := make(map[int]string)
	put := func(id int, key string) {
		if id > 0 && key != "" {
			if _, ok := m[id]; !ok {
				m[id] = key
			}
		}
	}
	for i := range s.Records {
		put(s.Records[i].IssueID, s.Records[i].IssueKey)
	}
	for i := range s.MyIssues {
		put(s.MyIssues[i].IssueID, s.MyIssues[i].IssueKey)
	}
	for i := range s.BacklogExtra {
		put(s.BacklogExtra[i].IssueID, s.BacklogExtra[i].IssueKey)
	}
	for i := range s.BacklogStale {
		put(s.BacklogStale[i].IssueID, s.BacklogStale[i].IssueKey)
	}
	return m
}

// presentIssueIDs は snapshot のあらゆる経路で「まだ見えている」課題 ID 集合を返す。
// records にメンションが残っている課題はまだ追跡中なので、archive 誤検知を防ぐため含める。
func presentIssueIDs(s *Snapshot) map[int]bool {
	m := make(map[int]bool)
	for i := range s.Records {
		m[s.Records[i].IssueID] = true
	}
	for i := range s.MyIssues {
		m[s.MyIssues[i].IssueID] = true
	}
	for i := range s.BacklogExtra {
		m[s.BacklogExtra[i].IssueID] = true
	}
	for i := range s.BacklogStale {
		m[s.BacklogStale[i].IssueID] = true
	}
	for i := range s.PassedIssues {
		m[s.PassedIssues[i].IssueID] = true
	}
	return m
}
