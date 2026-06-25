package backlog

import (
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	StatusUnhandled = "未対応"
	StatusCC        = "CC"
	StatusChecked   = "確認済"
	StatusReplied   = "返信済"
	// 表示用の集約ステータス。Checked / Replied を統合した chip / 集計用ラベル。
	// 内部の Record.Status は引き続き Checked / Replied のままで、表示レイヤで束ねる。
	StatusHandled = "対応済"

	// Backlog Notification.Reason: 課題が登録された（起票時通知）。
	// 担当者として登録 / 通知先指定など、起票イベントに付随する受動通知。
	// 担当課題タブで拾えるため、メンションタブでは 連絡のみ として分類する。
	reasonIssueCreated = 3

	// CCReason は Record.Status が "CC" のときの発火由来。UI 側で
	// 「起票時の通知欄追加」と「cc: @自分 メンション」を見分けるために使う。
	CCReasonIssueCreated = "issue_created"
	CCReasonCCMention    = "cc_mention"

	commentTruncateChars = 200
	excerptChars         = 120
	historyLimit         = 10
)

type Record struct {
	NotificationID int64  `json:"notification_id"`
	NotifiedAt     string `json:"notified_at"`
	NotifiedAtJST  string `json:"notified_at_jst"`
	AlreadyRead    bool   `json:"already_read"`
	Reason         int    `json:"reason"`
	Sender         string `json:"sender"`
	IssueID        int    `json:"issue_id"`
	IssueKey       string `json:"issue_key"`
	IssueSummary   string `json:"issue_summary"`
	IssueStatus    string `json:"issue_status"`
	CommentID      int    `json:"comment_id"`
	IssueURL       string `json:"issue_url"`
	CommentURL     string `json:"comment_url"`
	ContentExcerpt string `json:"content_excerpt"`
	Assignee       string `json:"assignee"`
	Creator        string `json:"creator"`
	Starred        bool   `json:"starred"`
	Replied        bool   `json:"replied"`
	AtMentioned    bool   `json:"at_mentioned"`
	// SilentClose は「完了」遷移の本文なし changeLog 通知を自動で確認済に格上げしたケース。
	// 表示側で「対応済（自動）」と通常の対応済（★/返信）を見分けるために使う。
	SilentClose bool `json:"silent_close"`
	// IsEvent は本文が空で changeLog のみのコメント（担当者変更・ステータス変更等）かどうか。
	// UI で「本文なし（担当者変更）」のようなプレースホルダ表示を出すために使う。
	IsEvent     bool     `json:"is_event,omitempty"`
	EventFields []string `json:"event_fields,omitempty"`
	Status      string   `json:"status"`
	// CCReason は Status=="CC" のときに由来を示す。issue_created / cc_mention。
	// 非 CC ではゼロ値。`omitempty` で snapshot.json を肥大化させない。
	CCReason            string `json:"cc_reason,omitempty"`
	CommentHistoryTitle string `json:"comment_history_title,omitempty"`
	CommentHistory      string `json:"comment_history,omitempty"`
}

type MyIssueRecord struct {
	IssueID            int    `json:"issue_id"`
	IssueKey           string `json:"issue_key"`
	IssueSummary       string `json:"issue_summary"`
	IssueStatus        string `json:"issue_status"`
	Priority           string `json:"priority"`
	IssueType          string `json:"issue_type"`
	Creator            string `json:"creator"`
	DueDate            string `json:"due_date"`
	UpdatedAt          string `json:"updated_at"`
	UpdatedAtJST       string `json:"updated_at_jst"`
	IssueURL           string `json:"issue_url"`
	Overdue            bool   `json:"overdue"`
	ParentIssueID      int    `json:"parent_issue_id,omitempty"`
	ParentIssueKey     string `json:"parent_issue_key,omitempty"`
	ParentIssueSummary string `json:"parent_issue_summary,omitempty"`
	ParentIssueURL     string `json:"parent_issue_url,omitempty"`
	// 自分の最終活動時刻（コメント / 更新等、Backlog activities API ベース）。
	// fetched activities ウィンドウ内に該当が無ければ空文字。
	LastUserActivityAt    string `json:"last_user_activity_at,omitempty"`
	LastUserActivityAtJST string `json:"last_user_activity_at_jst,omitempty"`
	CommentHistoryTitle   string `json:"comment_history_title,omitempty"`
	CommentHistory        string `json:"comment_history,omitempty"`
	// Description はオーバーレイ表示用に事前格納する課題の詳細（本文）。
	Description string `json:"description,omitempty"`
	// Origin は My Backlog での由来: "" / "assigned"（担当課題）/ "category"（カテゴリ取込）/ "stale"（対象外残留）。
	Origin string `json:"origin,omitempty"`
	// Stale は priorities に居るが現在の取込対象（担当∪カテゴリ）に無く、完了でもないため
	// 警告付きで残している課題のとき true。
	Stale bool `json:"stale,omitempty"`
}

// PassedIssueRecord は「自分が担当だったが、自分の操作で別の担当者に振り直したチケット」1件分。
// /users/:userId/activities の content.changes から assigner field の old==自分・new!=自分 を抽出して構築する。
// 現時点で自分が担当に戻っている／完了済みのものは fetch 段階で除外される。
type PassedIssueRecord struct {
	IssueID             int    `json:"issue_id"`
	IssueKey            string `json:"issue_key"`
	IssueSummary        string `json:"issue_summary"`
	PassedAt            string `json:"passed_at"`
	PassedAtJST         string `json:"passed_at_jst"`
	PassedTo            string `json:"passed_to"`
	IssueURL            string `json:"issue_url"`
	CommentHistoryTitle string `json:"comment_history_title,omitempty"`
	CommentHistory      string `json:"comment_history,omitempty"`
	// IssueUpdated は comments cache 更新判定用。filterPassedByCurrentState で IssueByID から拾う。
	IssueUpdated string `json:"-"`
}

type Snapshot struct {
	FetchedAt          string              `json:"fetched_at"`
	OwnUserID          int                 `json:"own_user_id"`
	OwnUserName        string              `json:"own_user_name"`
	Domain             string              `json:"domain"`
	Records            []Record            `json:"records"`
	MyIssues           []MyIssueRecord     `json:"my_issues"`
	MyIssueStatusOrder []string            `json:"my_issue_status_order,omitempty"`
	PassedIssues       []PassedIssueRecord `json:"passed_issues,omitempty"`
	// BacklogExtra は特定カテゴリから取り込んだ課題（担当課題と重複しないもの、完了除く）。
	// My Backlog の対象は MyIssues ∪ BacklogExtra ∪ BacklogStale。担当課題タブには出さない。
	BacklogExtra []MyIssueRecord `json:"backlog_extra,omitempty"`
	// BacklogStale は priorities に居るが現在の取込対象に無く、完了でもない課題（警告付き残留）。
	BacklogStale []MyIssueRecord `json:"backlog_stale,omitempty"`
	APICallCount int             `json:"api_call_count,omitempty"`
	// CommentsCache は次回 Fetch 時にコメント取得をスキップするためのキャッシュ。
	// キーは issue_id。次回の issue.updated が一致する課題はキャッシュを流用する。
	// 未対応 mention の課題は常時取得するため、ここに乗っていても新規取得される。
	CommentsCache map[int]CommentsCacheEntry `json:"comments_cache,omitempty"`
}

// CommentsCacheEntry は 1 課題分のコメント一覧と、その取得時点での issue.updated を保持する。
type CommentsCacheEntry struct {
	UpdatedAt string    `json:"updated_at"`
	Comments  []Comment `json:"comments"`
}

func hasAtMention(content, name string) bool {
	if content == "" || name == "" {
		return false
	}
	return strings.Contains(content, "@"+name)
}

func isStarredBy(c *Comment, userID int) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Stars {
		if s.Presenter != nil && s.Presenter.ID == userID {
			return true
		}
	}
	return false
}

func hasReplyAfter(comments []Comment, userID int, afterISO string) bool {
	for _, c := range comments {
		if c.CreatedUser == nil || c.CreatedUser.ID != userID {
			continue
		}
		// 本文の無いコメント（実績時間入力・ステータス変更等の changeLog-only イベント）は
		// 実返信ではないため返信とみなさない。これを数えると、メンション後に実績時間を
		// 入力しただけで「返信済 → 対応済」に誤分類される（DSC-11641 で顕在化）。
		if strings.TrimSpace(c.Content) == "" {
			continue
		}
		if c.Created > afterISO {
			return true
		}
	}
	return false
}

func determineStatus(replied, starred, silentClose, isCC bool) string {
	switch {
	case replied:
		return StatusReplied
	case starred, silentClose:
		return StatusChecked
	case isCC:
		return StatusCC
	default:
		return StatusUnhandled
	}
}

// isCCMention は「cc: @<userName>」形式の受動メンションかどうかを判定する。
// "cc:" "cc：" "CC:" "Cc:" などのバリエーションに対応する。
// マッチ条件:
//   - 「cc」（大文字小文字不問）の直後にコロン（半角 ":" / 全角 "："）
//   - その後 0 個以上の空白を挟んで `@<userName>` が出現
func isCCMention(content, userName string) bool {
	if content == "" || userName == "" {
		return false
	}
	lower := strings.ToLower(content)
	mention := "@" + userName
	mentionLower := strings.ToLower(mention)
	idx := 0
	for {
		hit := strings.Index(lower[idx:], "cc")
		if hit < 0 {
			return false
		}
		pos := idx + hit + 2 // "cc" の直後
		// 直後がコロン（半角 / 全角）か確認
		rest := lower[pos:]
		var afterColon string
		switch {
		case strings.HasPrefix(rest, ":"):
			afterColon = rest[1:]
		case strings.HasPrefix(rest, "："):
			afterColon = rest[len("："):]
		default:
			idx = pos
			continue
		}
		// 直後の空白をスキップして @<user> が来るか確認
		trimmed := strings.TrimLeft(afterColon, " \t　")
		if strings.HasPrefix(trimmed, mentionLower) {
			return true
		}
		idx = pos
	}
}

func normalizeContent(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateComment(s string) string {
	flat := normalizeContent(s)
	if r := []rune(flat); len(r) > commentTruncateChars {
		return string(r[:commentTruncateChars]) + "…"
	}
	return flat
}

func truncateExcerpt(s string) string {
	flat := normalizeContent(s)
	if r := []rune(flat); len(r) > excerptChars {
		return string(r[:excerptChars])
	}
	return flat
}

func formatJST(isoUTC string) string {
	if isoUTC == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, strings.Replace(isoUTC, "Z", "+00:00", 1))
	if err != nil {
		return isoUTC
	}
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		jst = time.FixedZone("JST", 9*60*60)
	}
	return t.In(jst).Format("2006-01-02 15:04")
}

func formatDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

func fetchMyIssues(c *Client, userID int) ([]MyIssueRecord, error) {
	today := time.Now().Format("2006-01-02")
	params := url.Values{}
	params.Add("assigneeId[]", fmt.Sprintf("%d", userID))
	// statusId は指定しない。プロジェクトのカスタムステータス（例: 「本番反映待ち」）も含めて
	// 取得し、後段で「完了」のみクライアント側で除外する。
	params.Set("count", "100")
	params.Set("sort", "updated")
	params.Set("order", "desc")

	allIssues, err := c.Issues(params)
	if err != nil {
		return nil, err
	}

	// 「完了」ステータスのチケットは除外する。
	issues := make([]Issue, 0, len(allIssues))
	for _, iss := range allIssues {
		if iss.Status != nil && iss.Status.Name == "完了" {
			continue
		}
		issues = append(issues, iss)
	}

	// Build index of fetched issues by ID
	byID := map[int]Issue{}
	for _, iss := range issues {
		byID[iss.ID] = iss
	}

	// Fetch parent issues that are not already in the list
	parentInfo := map[int]Issue{}
	for _, iss := range issues {
		if iss.ParentIssueId == nil || *iss.ParentIssueId <= 0 {
			continue
		}
		pid := *iss.ParentIssueId
		if _, ok := parentInfo[pid]; ok {
			continue
		}
		if p, ok := byID[pid]; ok {
			parentInfo[pid] = p
		} else {
			fetched, err := c.IssueByID(pid)
			if err == nil {
				parentInfo[pid] = *fetched
			}
		}
	}

	records := make([]MyIssueRecord, 0, len(issues))
	for _, issue := range issues {
		r := MyIssueRecord{
			IssueID:      issue.ID,
			IssueKey:     issue.IssueKey,
			IssueSummary: issue.Summary,
			Description:  issue.Description,
			IssueURL:     fmt.Sprintf("https://%s/view/%s", c.Domain, issue.IssueKey),
			UpdatedAt:    issue.Updated,
			UpdatedAtJST: formatJST(issue.Updated),
		}
		if issue.Status != nil {
			r.IssueStatus = issue.Status.Name
		}
		if issue.Priority != nil {
			r.Priority = issue.Priority.Name
		}
		if issue.IssueType != nil {
			r.IssueType = issue.IssueType.Name
		}
		if issue.CreatedUser != nil {
			r.Creator = issue.CreatedUser.Name
		}
		if issue.DueDate != "" {
			r.DueDate = formatDate(issue.DueDate)
			r.Overdue = r.DueDate < today
		}
		if issue.ParentIssueId != nil && *issue.ParentIssueId > 0 {
			pid := *issue.ParentIssueId
			r.ParentIssueID = pid
			if p, ok := parentInfo[pid]; ok {
				r.ParentIssueKey = p.IssueKey
				r.ParentIssueSummary = p.Summary
				r.ParentIssueURL = fmt.Sprintf("https://%s/view/%s", c.Domain, p.IssueKey)
			}
		}
		records = append(records, r)
	}
	return records, nil
}

func formatCommentHistory(domain, issueKey string, comments []Comment, highlightID, ownUserID int) (title, body string) {
	limit := historyLimit
	if len(comments) < limit {
		limit = len(comments)
	}
	var b strings.Builder
	shown := 0
	for _, c := range comments {
		if shown >= limit {
			break
		}
		if strings.TrimSpace(c.Content) == "" {
			continue
		}
		shown++
		ts := formatJST(c.Created)
		author := ""
		isOwn := false
		if c.CreatedUser != nil {
			author = c.CreatedUser.Name
			isOwn = ownUserID != 0 && c.CreatedUser.ID == ownUserID
		}
		content := truncateComment(c.Content)
		marker := ""
		if c.ID == highlightID {
			marker = " 👈 メンション元"
		}
		ownPrefix := ""
		if isOwn {
			// renderHistory が拾って own-comment クラスを付与し、見出しから除去する sentinel
			ownPrefix = "[ME] "
		}
		url := fmt.Sprintf("https://%s/view/%s#comment-%d", domain, issueKey, c.ID)
		fmt.Fprintf(&b, "### %s[%s](%s) - %s%s\n\n", ownPrefix, ts, url, author, marker)
		if content == "" {
			b.WriteString("_(本文なし)_\n\n")
		} else {
			b.WriteString(content)
			b.WriteString("\n\n")
		}
	}
	title = fmt.Sprintf("コメント履歴 (最新%d件)", shown)
	return title, b.String()
}

type FetchOptions struct {
	Count       int
	IncludeRead bool
	// Pages は Notifications を何ページ取得するか（1 ページ = Count 件）。
	// 長期休暇明けの取りこぼし防止用。0 以下なら 1 として扱う。
	Pages int
	// CategoryID > 0 のとき、そのカテゴリの課題（完了除く）を My Backlog 用に取り込む。
	CategoryID int
	// PriorityIDs は priorities.json に保存済みの issue_id 群。取込対象に無いものを
	// IssueByID で現況確認し、完了でなければ警告付きで残す（stale 判定）。
	PriorityIDs []int
}

// basicIssueRecord は Issue から My Backlog 用の MyIssueRecord を組み立てる軽量版。
// 親子グルーピング情報は付けない（My Backlog はフラットな一列のため不要）。
func basicIssueRecord(domain string, issue Issue, today string) MyIssueRecord {
	r := MyIssueRecord{
		IssueID:      issue.ID,
		IssueKey:     issue.IssueKey,
		IssueSummary: issue.Summary,
		Description:  issue.Description,
		IssueURL:     fmt.Sprintf("https://%s/view/%s", domain, issue.IssueKey),
		UpdatedAt:    issue.Updated,
		UpdatedAtJST: formatJST(issue.Updated),
	}
	if issue.Status != nil {
		r.IssueStatus = issue.Status.Name
	}
	if issue.Priority != nil {
		r.Priority = issue.Priority.Name
	}
	if issue.IssueType != nil {
		r.IssueType = issue.IssueType.Name
	}
	if issue.CreatedUser != nil {
		r.Creator = issue.CreatedUser.Name
	}
	if issue.DueDate != "" {
		r.DueDate = formatDate(issue.DueDate)
		r.Overdue = r.DueDate < today
	}
	return r
}

func Fetch(c *Client, opts FetchOptions, prev *Snapshot) (*Snapshot, error) {
	if opts.Count <= 0 {
		opts.Count = 100
	}
	if opts.Pages <= 0 {
		opts.Pages = 1
	}

	apiCallsBefore := c.APICalls()

	// 前回 snapshot から以下を抽出（mention のコメント取得を最新性優先にする判定材料）:
	//   - prevUnhandledKeys: "未対応" mention だった課題 IssueKey 集合 → 常時取得
	//   - prevMentionedKeys: mention 一覧に出ていた課題 IssueKey 集合 → 新規 mention 判定に使う
	//   - prevCommentsCache: コメントキャッシュ
	prevUnhandledKeys := map[string]bool{}
	prevMentionedKeys := map[string]bool{}
	var prevCommentsCache map[int]CommentsCacheEntry
	if prev != nil {
		for _, r := range prev.Records {
			prevMentionedKeys[r.IssueKey] = true
			if r.Status == StatusUnhandled {
				prevUnhandledKeys[r.IssueKey] = true
			}
		}
		prevCommentsCache = prev.CommentsCache
	}

	me, err := c.Myself()
	if err != nil {
		return nil, fmt.Errorf("myself: %w", err)
	}

	// Notifications を最大 opts.Pages ページ取得（長期休暇明け等で 100 件超のメンションが
	// 溜まっていた場合の取りこぼし防止）。ページサイズ未満の応答 or 空応答が来たら早期終了。
	// 重複は seen で除去（Backlog の maxId 境界が実装上 inclusive/exclusive 混在しても安全に）。
	var notifs []Notification
	seen := map[int64]bool{}
	var nextMaxID int64
	pagesFetched := 0
	for page := 0; page < opts.Pages; page++ {
		pageItems, err := c.Notifications(opts.Count, nextMaxID)
		if err != nil {
			return nil, fmt.Errorf("notifications page %d: %w", page+1, err)
		}
		pagesFetched++
		if len(pageItems) == 0 {
			break
		}
		var minID int64
		for _, n := range pageItems {
			if seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			notifs = append(notifs, n)
			if minID == 0 || n.ID < minID {
				minID = n.ID
			}
		}
		if len(pageItems) < opts.Count {
			break // 取得件数がページサイズ未満 = これ以上の通知は無い
		}
		nextMaxID = minID - 1
	}
	slog.Info("notifications fetched", "total", len(notifs), "pages_fetched", pagesFetched, "pages_max", opts.Pages)

	var targets []Notification
	for _, n := range notifs {
		if n.Comment == nil || n.Issue == nil {
			continue
		}
		if !opts.IncludeRead && n.AlreadyRead && n.ResourceAlreadyRead {
			continue
		}
		if n.Sender != nil && n.Sender.ID == me.ID {
			continue
		}
		targets = append(targets, n)
	}

	byIssue := map[int][]Notification{}
	for _, n := range targets {
		byIssue[n.Issue.ID] = append(byIssue[n.Issue.ID], n)
	}

	myIssues, err := fetchMyIssues(c, me.ID)
	if err != nil {
		return nil, fmt.Errorf("my issues: %w", err)
	}

	// 活動取得を 2 系統に分ける（用途が異なる）:
	//   lastActs: 全アクティビティ種別から「課題ごとの自分の最終活動時刻」を抽出
	//   passedActs: type=2,3 限定で assigner 変更を抽出
	// どちらも失敗してもベストエフォートで先に進める。
	lastActs, err := c.UserActivities(me.ID, 100)
	if err != nil {
		slog.Warn("user activities (last) fetch failed", "user", me.ID, "error", err)
	}
	myIssues = applyLastUserActivity(myIssues, lastActs)

	statusOrder := fetchMyIssueStatusOrder(c, myIssues)

	// My Backlog 用の追加取込: 担当課題に Origin="assigned" を付け、
	// カテゴリ課題（完了除く・担当と重複しない分）と stale 課題を集める。
	today := time.Now().Format("2006-01-02")
	for i := range myIssues {
		myIssues[i].Origin = "assigned"
	}
	targetIDs := map[int]bool{}
	for _, m := range myIssues {
		targetIDs[m.IssueID] = true
	}

	var backlogExtra []MyIssueRecord
	if opts.CategoryID > 0 {
		params := url.Values{
			"categoryId[]": {fmt.Sprintf("%d", opts.CategoryID)},
			"count":        {"100"},
			"sort":         {"updated"},
			"order":        {"desc"},
		}
		catIssues, err := c.Issues(params)
		if err != nil {
			slog.Warn("category issues fetch failed", "category_id", opts.CategoryID, "error", err)
		}
		for _, iss := range catIssues {
			if iss.Status != nil && iss.Status.Name == "完了" {
				continue
			}
			if targetIDs[iss.ID] {
				continue // 担当課題と重複
			}
			targetIDs[iss.ID] = true
			r := basicIssueRecord(c.Domain, iss, today)
			r.Origin = "category"
			backlogExtra = append(backlogExtra, r)
		}
	}

	// stale: priorities に居るが取込対象に無い課題。完了なら自動削除、それ以外は警告付きで残す。
	var backlogStale []MyIssueRecord
	for _, pid := range opts.PriorityIDs {
		if pid <= 0 || targetIDs[pid] {
			continue
		}
		targetIDs[pid] = true
		iss, err := c.IssueByID(pid)
		if err != nil {
			slog.Warn("stale issue fetch failed", "issue_id", pid, "error", err)
			continue
		}
		if iss.Status != nil && iss.Status.Name == "完了" {
			continue // 完了は自動削除（snapshot に載せない → handler 側で order からも消える）
		}
		r := basicIssueRecord(c.Domain, *iss, today)
		r.Origin = "stale"
		r.Stale = true
		backlogStale = append(backlogStale, r)
	}

	passedActs, err := c.UserActivities(me.ID, 100, 2, 3)
	if err != nil {
		slog.Warn("user activities (passed) fetch failed", "user", me.ID, "error", err)
	}
	passedCandidates := extractPassedCandidates(c.Domain, me.Name, passedActs)
	passedIssues := filterPassedByCurrentState(c, passedCandidates, me.Name)

	// 共有コメントキャッシュ: mention ∪ my-issues ∪ filtered passed のユニーク ID 集合を
	// 取得対象とする。前回 snapshot に乗っており issue.updated が一致する課題はキャッシュを流用し、
	// IssueComments の呼び出しを省く。ただし下記の課題は常時取得する:
	//   - 前回 snapshot に "未対応 mention" として存在した課題（既読/返信/★ 検出が遅れないように）
	//   - 前回 snapshot に存在しなかった課題（新規 mention / 新規 my_issue / 新規 passed）
	commentNeeds := map[int]bool{}
	currentUpdated := map[int]string{}
	currentIssueKey := map[int]string{}
	for id, ns := range byIssue {
		commentNeeds[id] = true
		if len(ns) > 0 && ns[0].Issue != nil {
			currentUpdated[id] = ns[0].Issue.Updated
			currentIssueKey[id] = ns[0].Issue.IssueKey
		}
	}
	for _, m := range myIssues {
		commentNeeds[m.IssueID] = true
		if _, ok := currentUpdated[m.IssueID]; !ok {
			currentUpdated[m.IssueID] = m.UpdatedAt
		}
		if _, ok := currentIssueKey[m.IssueID]; !ok {
			currentIssueKey[m.IssueID] = m.IssueKey
		}
	}
	for _, p := range passedIssues {
		commentNeeds[p.IssueID] = true
		if _, ok := currentUpdated[p.IssueID]; !ok && p.IssueUpdated != "" {
			currentUpdated[p.IssueID] = p.IssueUpdated
		}
		if _, ok := currentIssueKey[p.IssueID]; !ok {
			currentIssueKey[p.IssueID] = p.IssueKey
		}
	}
	// カテゴリ取込・stale 課題もコメント履歴を付与する（オーバーレイ事前格納用）。
	// updated_at をキャッシュキーに使うことで、変化が無ければ次回 IssueComments を省ける。
	for _, m := range append(append([]MyIssueRecord{}, backlogExtra...), backlogStale...) {
		commentNeeds[m.IssueID] = true
		if _, ok := currentUpdated[m.IssueID]; !ok {
			currentUpdated[m.IssueID] = m.UpdatedAt
		}
		if _, ok := currentIssueKey[m.IssueID]; !ok {
			currentIssueKey[m.IssueID] = m.IssueKey
		}
	}

	commentsCache := map[int][]Comment{}
	commentsCacheReused := 0
	for id := range commentNeeds {
		// mention は最新性が重要なので、以下のいずれかなら常時取得（updated_at 一致でもスキップしない）:
		//   - 前回 "未対応" だった mention（既読/返信/★ の遅延検出を避ける）
		//   - 前回 mention に出ていなかった（= 新規 mention。updated_at がコメント編集等で動かない場合の保険）
		isMention := byIssue[id] != nil
		key := currentIssueKey[id]
		alwaysFetch := isMention && (prevUnhandledKeys[key] || !prevMentionedKeys[key])

		if !alwaysFetch {
			if entry, ok := prevCommentsCache[id]; ok {
				if cu := currentUpdated[id]; cu != "" && cu == entry.UpdatedAt {
					commentsCache[id] = entry.Comments
					commentsCacheReused++
					continue
				}
			}
		}
		cs, err := c.IssueComments(id, 100)
		if err != nil {
			slog.Warn("issue comments fetch failed", "issue_id", id, "error", err)
			continue
		}
		commentsCache[id] = cs
	}
	slog.Debug("comments cache reuse", "reused", commentsCacheReused, "fetched", len(commentNeeds)-commentsCacheReused)

	records := make([]Record, 0, len(targets))
	for issueID, notifs := range byIssue {
		comments := commentsCache[issueID]
		byID := map[int]*Comment{}
		for i := range comments {
			byID[comments[i].ID] = &comments[i]
		}
		for _, n := range notifs {
			cid := n.Comment.ID
			cm := byID[cid]
			if cm == nil {
				fetched, err := c.IssueComment(issueID, cid)
				if err != nil {
					return nil, fmt.Errorf("issue %d comment %d: %w", issueID, cid, err)
				}
				cm = fetched
			}
			replied := hasReplyAfter(comments, me.ID, n.Created)
			starred := isStarredBy(cm, me.ID)
			atMentioned := hasAtMention(cm.Content, me.Name)
			// 完了に遷移しただけ（本文なしの changeLog 通知）なら確認不要として 確認済 に格上げ。
			// クローズ時に本文付きコメントがあれば未対応のまま残し、ユーザーに本文を読ませる。
			silentClose := n.Issue.Status != nil && n.Issue.Status.Name == "完了" && cm.IsEvent()
			// CC 判定（受動的に来る通知。メンションタブでの反応は不要だが認知はしておきたい）:
			//   - 起票時通知（reason=3）: 担当課題タブで拾えるため反応不要
			//   - cc: パターンの受動メンション: 本人宛ではないため反応不要
			// 由来を CCReason として保持し、UI で見分けられるようにする。
			ccReason := ""
			switch {
			case n.Reason == reasonIssueCreated:
				ccReason = CCReasonIssueCreated
			case isCCMention(cm.Content, me.Name):
				ccReason = CCReasonCCMention
			}
			isCC := ccReason != ""
			status := determineStatus(replied, starred, silentClose, isCC)

			isEvent := cm.IsEvent()
			var eventFields []string
			if isEvent {
				eventFields = make([]string, 0, len(cm.ChangeLog))
				seen := map[string]bool{}
				for _, cl := range cm.ChangeLog {
					if cl.Field == "" || seen[cl.Field] {
						continue
					}
					seen[cl.Field] = true
					eventFields = append(eventFields, cl.Field)
				}
			}

			rec := Record{
				NotificationID: n.ID,
				NotifiedAt:     n.Created,
				NotifiedAtJST:  formatJST(n.Created),
				AlreadyRead:    n.AlreadyRead,
				Reason:         n.Reason,
				IssueID:        n.Issue.ID,
				IssueKey:       n.Issue.IssueKey,
				IssueSummary:   n.Issue.Summary,
				CommentID:      cid,
				IssueURL:       fmt.Sprintf("https://%s/view/%s", c.Domain, n.Issue.IssueKey),
				CommentURL:     fmt.Sprintf("https://%s/view/%s#comment-%d", c.Domain, n.Issue.IssueKey, cid),
				ContentExcerpt: truncateExcerpt(cm.Content),
				Starred:        starred,
				Replied:        replied,
				AtMentioned:    atMentioned,
				SilentClose:    silentClose,
				IsEvent:        isEvent,
				EventFields:    eventFields,
				Status:         status,
			}
			if status == StatusCC {
				rec.CCReason = ccReason
			}
			if n.Issue.Status != nil {
				rec.IssueStatus = n.Issue.Status.Name
			}
			if n.Sender != nil {
				rec.Sender = n.Sender.Name
			}
			if n.Issue.Assignee != nil {
				rec.Assignee = n.Issue.Assignee.Name
			}
			if n.Issue.CreatedUser != nil {
				rec.Creator = n.Issue.CreatedUser.Name
			}
			// 全ステータスでコメント履歴を付与（返信済・確認済でも自分の返信と相手の発言を読み返したい）。
			rec.CommentHistoryTitle, rec.CommentHistory = formatCommentHistory(c.Domain, n.Issue.IssueKey, comments, cid, me.ID)
			records = append(records, rec)
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].NotifiedAt > records[j].NotifiedAt
	})

	// 担当課題: モード非依存で全件にコメント履歴を付与する
	for i := range myIssues {
		comments, ok := commentsCache[myIssues[i].IssueID]
		if !ok {
			continue
		}
		myIssues[i].CommentHistoryTitle, myIssues[i].CommentHistory =
			formatCommentHistory(c.Domain, myIssues[i].IssueKey, comments, 0, me.ID)
	}

	// パス済みから「未対応 mention がある課題」を除外する。
	// メンションタブで未対応として扱う方が自分の TODO 性が高いため、両タブに重複表示しない。
	unhandledMentionIDs := map[int]bool{}
	for _, r := range records {
		if r.Status == StatusUnhandled {
			unhandledMentionIDs[r.IssueID] = true
		}
	}
	if len(unhandledMentionIDs) > 0 {
		filtered := make([]PassedIssueRecord, 0, len(passedIssues))
		for _, p := range passedIssues {
			if unhandledMentionIDs[p.IssueID] {
				continue
			}
			filtered = append(filtered, p)
		}
		passedIssues = filtered
	}

	// パス済み: コメント履歴を付与してソート
	for i := range passedIssues {
		comments, ok := commentsCache[passedIssues[i].IssueID]
		if !ok {
			continue
		}
		passedIssues[i].CommentHistoryTitle, passedIssues[i].CommentHistory =
			formatCommentHistory(c.Domain, passedIssues[i].IssueKey, comments, 0, me.ID)
	}
	sort.Slice(passedIssues, func(i, j int) bool {
		return passedIssues[i].PassedAt > passedIssues[j].PassedAt
	})

	// カテゴリ取込・stale 課題にもコメント履歴を付与する。
	for i := range backlogExtra {
		if comments, ok := commentsCache[backlogExtra[i].IssueID]; ok {
			backlogExtra[i].CommentHistoryTitle, backlogExtra[i].CommentHistory =
				formatCommentHistory(c.Domain, backlogExtra[i].IssueKey, comments, 0, me.ID)
		}
	}
	for i := range backlogStale {
		if comments, ok := commentsCache[backlogStale[i].IssueID]; ok {
			backlogStale[i].CommentHistoryTitle, backlogStale[i].CommentHistory =
				formatCommentHistory(c.Domain, backlogStale[i].IssueKey, comments, 0, me.ID)
		}
	}

	// 次回 Fetch 用にコメントキャッシュを構築。各 issue_id の現在の updated_at と紐づけて保存する。
	nextCommentsCache := make(map[int]CommentsCacheEntry, len(commentsCache))
	for id, cs := range commentsCache {
		nextCommentsCache[id] = CommentsCacheEntry{
			UpdatedAt: currentUpdated[id],
			Comments:  cs,
		}
	}

	return &Snapshot{
		FetchedAt:          time.Now().Format(time.RFC3339),
		OwnUserID:          me.ID,
		OwnUserName:        me.Name,
		Domain:             c.Domain,
		Records:            records,
		MyIssues:           myIssues,
		MyIssueStatusOrder: statusOrder,
		PassedIssues:       passedIssues,
		BacklogExtra:       backlogExtra,
		BacklogStale:       backlogStale,
		APICallCount:       int(c.APICalls() - apiCallsBefore),
		CommentsCache:      nextCommentsCache,
	}, nil
}

// extractPassedCandidates は事前取得済みの activities から「自分→他人」の assigner 変更を
// 抽出する。同一課題は最新の 1 件のみ採用する。API は呼ばない。
func extractPassedCandidates(domain, userName string, acts []Activity) []PassedIssueRecord {
	seen := map[int]bool{}
	out := []PassedIssueRecord{}
	for _, a := range acts {
		if a.Project == nil || a.Content.ID == 0 {
			continue
		}
		for _, ch := range a.Content.Changes {
			if ch.Field != "assigner" {
				continue
			}
			if ch.OldValue == "" || ch.OldValue != userName {
				continue
			}
			if ch.NewValue == "" || ch.NewValue == userName {
				continue
			}
			if seen[a.Content.ID] {
				break
			}
			seen[a.Content.ID] = true

			issueKey := fmt.Sprintf("%s-%d", a.Project.ProjectKey, a.Content.KeyID)
			out = append(out, PassedIssueRecord{
				IssueID:      a.Content.ID,
				IssueKey:     issueKey,
				IssueSummary: a.Content.Summary,
				PassedAt:     a.Created,
				PassedAtJST:  formatJST(a.Created),
				PassedTo:     ch.NewValue,
				IssueURL:     fmt.Sprintf("https://%s/view/%s", domain, issueKey),
			})
			break
		}
	}
	return out
}

// filterPassedByCurrentState は各候補の現状を IssueByID で取得し、以下を満たすものは除外する:
//   - 現在の担当者が自分（パス後に自分へ戻った）
//   - 現在のステータスが「完了」
//
// IssueByID 失敗時はベストエフォートで該当レコードを残し、警告ログを出す。
func filterPassedByCurrentState(c *Client, candidates []PassedIssueRecord, userName string) []PassedIssueRecord {
	out := make([]PassedIssueRecord, 0, len(candidates))
	for _, p := range candidates {
		issue, err := c.IssueByID(p.IssueID)
		if err != nil {
			slog.Warn("issue fetch failed for passed", "issue_id", p.IssueID, "error", err)
			out = append(out, p)
			continue
		}
		if issue.Assignee != nil && issue.Assignee.Name == userName {
			continue
		}
		if issue.Status != nil && issue.Status.Name == "完了" {
			continue
		}
		p.IssueUpdated = issue.Updated
		out = append(out, p)
	}
	return out
}

// applyLastUserActivity は事前取得済みの activities から各課題への最終活動時刻を抽出し、
// 対応する MyIssueRecord にセットして返す。activities ウィンドウに該当の活動が無い課題は
// LastUserActivityAt が空のままになる。acts が nil の場合は入力をそのまま返す。
func applyLastUserActivity(issues []MyIssueRecord, acts []Activity) []MyIssueRecord {
	if len(acts) == 0 {
		return issues
	}
	latest := map[int]string{}
	for _, a := range acts {
		id := a.Content.ID
		if id == 0 || a.Created == "" {
			continue
		}
		if cur, ok := latest[id]; !ok || a.Created > cur {
			latest[id] = a.Created
		}
	}
	out := make([]MyIssueRecord, len(issues))
	copy(out, issues)
	for i := range out {
		if ts, ok := latest[out[i].IssueID]; ok {
			out[i].LastUserActivityAt = ts
			out[i].LastUserActivityAtJST = formatJST(ts)
		}
	}
	return out
}

// 担当課題に含まれる各プロジェクトの statuses をマージし、Backlog 側の displayOrder を尊重した
// 横断的な並びを返す。同名ステータスは複数プロジェクトで最小の displayOrder を採用する。
// API 失敗時はそのプロジェクト分をスキップし、ベストエフォートで返す。
func fetchMyIssueStatusOrder(c *Client, issues []MyIssueRecord) []string {
	projectKeys := map[string]bool{}
	for _, r := range issues {
		if idx := strings.LastIndex(r.IssueKey, "-"); idx > 0 {
			projectKeys[r.IssueKey[:idx]] = true
		}
	}

	type orderedStatus struct {
		name  string
		order int
	}
	byName := map[string]int{}
	for key := range projectKeys {
		statuses, err := c.ProjectStatuses(key)
		if err != nil {
			slog.Warn("project statuses fetch failed", "project", key, "error", err)
			continue
		}
		for _, s := range statuses {
			if cur, ok := byName[s.Name]; !ok || s.DisplayOrder < cur {
				byName[s.Name] = s.DisplayOrder
			}
		}
	}

	merged := make([]orderedStatus, 0, len(byName))
	for name, order := range byName {
		merged = append(merged, orderedStatus{name, order})
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].order != merged[j].order {
			return merged[i].order < merged[j].order
		}
		return merged[i].name < merged[j].name
	})

	result := make([]string, 0, len(merged))
	for _, m := range merged {
		result = append(result, m.name)
	}
	return result
}
