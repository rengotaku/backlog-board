package handler

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rengotaku/backlog-board/internal/backlog"
	"github.com/rengotaku/backlog-board/internal/store"
)

// Options は handler が受け持つセキュリティ系の許可リストをまとめる。
// AllowedHosts: Host ヘッダの allowlist (DNS rebinding 対策)。例: "127.0.0.1:8082", "localhost:8082"。
// AllowedOrigins: POST 等の state-changing リクエストで許可する Origin/Referer プレフィックス。
//   例: "http://127.0.0.1:8082", "http://localhost:8082"。
// LinkAllowPrefixes: コメント本文中の [text](url) リンクを <a href> として有効化する URL prefix。
//   nil または空 (デフォルト) では http/https/mailse 全許可。非空にすると prefix allowlist で厳格化する。
// PostCommentStar: スター付与 callback。nil の場合は /api/star エンドポイントを公開しない。
type Options struct {
	AllowedHosts      []string
	AllowedOrigins    []string
	LinkAllowPrefixes []string
	PostCommentStar   func(commentID int) error
}

type Handler struct {
	cache             *store.Cache
	templates         map[string]*template.Template
	staticFS          fs.FS
	refresh           func() error
	postStar          func(commentID int) error
	opts              Options
	linkAllowPrefixes []string
}

func New(cache *store.Cache, templates map[string]*template.Template, staticFS fs.FS, refresh func() error, opts Options) *Handler {
	return &Handler{
		cache:             cache,
		templates:         templates,
		staticFS:          staticFS,
		refresh:           refresh,
		postStar:          opts.PostCommentStar,
		opts:              opts,
		linkAllowPrefixes: opts.LinkAllowPrefixes,
	}
}

func (h *Handler) Routes() http.Handler {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(requireAllowedHost(h.opts.AllowedHosts))
	r.Use(requireSameOrigin(h.opts.AllowedOrigins))
	r.StaticFS("/static", http.FS(h.staticFS))
	r.GET("/", h.handleIndex)
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/api/state", h.handleState)
	if h.refresh != nil {
		r.POST("/api/refresh", h.handleRefresh)
	}
	if h.postStar != nil {
		r.POST("/api/star", h.handleStar)
	}
	return r
}

// requireAllowedHost は Host ヘッダが allowlist に含まれない場合 403 を返す。
// DNS rebinding 攻撃で attacker.example が 127.0.0.1 に解決されるシナリオを遮断する。
// allowed が空の場合は素通し（テスト等）。
func requireAllowedHost(allowed []string) gin.HandlerFunc {
	if len(allowed) == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	set := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		set[h] = true
	}
	return func(c *gin.Context) {
		if !set[c.Request.Host] {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// requireSameOrigin は state-changing メソッド (POST/PUT/DELETE/PATCH) で
// Origin / Referer ヘッダが allowlist に prefix 一致しない場合 403 を返す。
// allowed が空の場合は素通し（テスト等）。
func requireSameOrigin(allowed []string) gin.HandlerFunc {
	if len(allowed) == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = c.GetHeader("Referer")
		}
		if origin == "" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		for _, prefix := range allowed {
			if strings.HasPrefix(origin, prefix) {
				c.Next()
				return
			}
		}
		c.AbortWithStatus(http.StatusForbidden)
	}
}

// handleState は開いたページがポーリングで参照する軽量エンドポイント。
// 現行 snapshot の fetched_at と、前世代との差分カウント（未対応 / CC / 対応済）を返す。
func (h *Handler) handleState(c *gin.Context) {
	snap, err := h.cache.Load()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"fetched_at": "",
			"diff":       emptyDiff(),
		})
		return
	}
	prev, _ := h.cache.LoadPrevious() // 無くても nil 扱いで進める
	c.JSON(http.StatusOK, gin.H{
		"fetched_at": snap.FetchedAt,
		"diff":       computeMentionDiff(snap, prev),
	})
}

type diffCounts struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
}

func emptyDiff() diffCounts {
	return diffCounts{
		Total: 0,
		ByStatus: map[string]int{
			backlog.StatusUnhandled: 0,
			backlog.StatusCC:        0,
			backlog.StatusHandled:   0,
		},
	}
}

// computeMentionDiff は current / previous のメンション差分を、最新ステータス別に集計する。
// 「差分」とは: 前世代に存在しなかった NotificationID（新規メンション）、
// または前世代から Status が変化した NotificationID。
func computeMentionDiff(current, previous *backlog.Snapshot) diffCounts {
	d := emptyDiff()
	if current == nil {
		return d
	}
	prev := map[int64]string{}
	if previous != nil {
		for _, r := range previous.Records {
			prev[r.NotificationID] = r.Status
		}
	}
	for _, r := range current.Records {
		prevStatus, existed := prev[r.NotificationID]
		if !existed || prevStatus != r.Status {
			d.ByStatus[displayStatus(r.Status)]++
			d.Total++
		}
	}
	return d
}

func (h *Handler) handleRefresh(c *gin.Context) {
	if err := h.refresh(); err != nil {
		c.String(http.StatusInternalServerError, "fetch error: "+err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/")
}

// handleStar は指定 comment_id にスター付与する。
// 流れ: snapshot からスター済か確認 → 未済なら Backlog API POST → refresh で再フェッチ。
// レスポンスは JSON で {"ok": true, "already_starred": bool}。
func (h *Handler) handleStar(c *gin.Context) {
	cidStr := c.PostForm("comment_id")
	if cidStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "comment_id required"})
		return
	}
	cid, err := strconv.Atoi(cidStr)
	if err != nil || cid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid comment_id"})
		return
	}

	// snapshot から該当 comment を特定し、既に Starred なら no-op。
	// 同一 commentID が複数 record にまたがるケース（reason 違いの通知）も「自分のスター」状態は同じなので
	// 1件でも Starred=true があれば早期 return する。
	if snap, err := h.cache.Load(); err == nil {
		known := false
		for _, r := range snap.Records {
			if r.CommentID != cid {
				continue
			}
			known = true
			if r.Starred {
				c.JSON(http.StatusOK, gin.H{"ok": true, "already_starred": true})
				return
			}
		}
		if !known {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown comment_id"})
			return
		}
	}

	if err := h.postStar(cid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// refresh は失敗してもスター付与自体は成功しているのでログのみ。
	// 次回 15 分の定期 fetch でも拾える。
	if h.refresh != nil {
		if err := h.refresh(); err != nil {
			slog.Warn("refresh after star failed", "error", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type viewData struct {
	Title          string
	FetchedAt      string
	FetchedAtJST   string
	FetchedSince   string
	CacheStale     bool
	StaleLabel     string
	Counts         map[string]int
	StatusOrder    []string
	Buckets        []mentionBucket
	Total          int
	Error          string
	Domain         string
	OwnUserName    string
	UnhandledFirst bool
	MyIssueGroups  []myIssueGroup
	MyIssueActive  []myIssueBucket
	MyIssuePassed  []passedIssueView
	MyIssueKanban  []myIssueKanbanColumn
	MyIssueTotal   int
	CanRefresh     bool
	APICallCount   int
}

// myIssueKanbanColumn は kanban モード用の 1 ステータス列。
// Issues は LatestSince (= UpdatedAt) 降順で並ぶ。Count は 0 でも列自体は描画する
// （プロセス全体を一望するため）。表示/非表示は LocalStorage で制御する。
type myIssueKanbanColumn struct {
	Status string
	Issues []myIssueView
	Count  int
}

type passedIssueView struct {
	IssueKey            string
	IssueSummary        string
	PassedAtJST         string
	PassedSince         string
	PassedTo            string
	IssueURL            string
	CommentHistoryTitle string
	CommentHistory      template.HTML
}

type mentionBucket struct {
	Label   string
	Slug    string
	Records []recordView
	Count   int
}

type myIssueGroup struct {
	HasParent      bool
	ParentKey      string
	ParentSummary  string
	ParentURL      string
	Issues         []myIssueView
}

type myIssueBucket struct {
	Label  string
	Slug   string
	Issues []myIssueView
	Count  int
}

type myIssueView struct {
	IssueKey              string
	IssueSummary          string
	IssueStatus           string
	Priority              string
	IssueType             string
	Creator               string
	DueDate               string
	UpdatedAtJST          string
	IssueURL              string
	Overdue               bool
	LastUserActivityAtJST string
	// 状態 badge の右に表示する経過時間ラベル（例: "20時間前"）。
	// group/kanban モードでは UpdatedAt 起点、active モードでは max(活動, 更新) 起点。
	LatestSince         string
	LatestSinceJST      string
	CommentHistoryTitle string
	CommentHistory      template.HTML
}

type recordView struct {
	NotificationID int64
	NotifiedAtJST  string
	NotifiedSince  string
	IssueKey       string
	IssueSummary   string
	IssueStatus    string
	Status         string
	// CCReason は Status=="CC" のときの発火由来（issue_created / cc_mention）。
	// テンプレートで小バッジを出し分けるために使う。非 CC では空。
	CCReason       string
	Sender         string
	Assignee       string
	Creator        string
	ContentExcerpt template.HTML
	IssueURL       string
	CommentURL     string
	CommentID      int
	CommentHistoryTitle string
	CommentHistory      template.HTML
	IsAssignee     bool
	IsCreator      bool
	Starred        bool
	Replied        bool
	AtMentioned    bool
	SilentClose    bool
	// IsEvent は本文なし changeLog 通知の場合に true。EventFieldsLabel は
	// テンプレートに表示する和訳済みラベル（例: "担当者変更, ステータス変更"）。
	IsEvent          bool
	EventFieldsLabel string
}

func (h *Handler) handleIndex(c *gin.Context) {
	data := viewData{Title: "Backlog board", UnhandledFirst: true, CanRefresh: h.refresh != nil}

	snap, err := h.cache.Load()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data.Error = "キャッシュがまだ作成されていません。fetch を実行してください。"
		} else {
			data.Error = fmt.Sprintf("キャッシュ読み込みエラー: %v", err)
		}
		h.render(c, "index.html", http.StatusOK, data)
		return
	}

	data.Domain = snap.Domain
	data.OwnUserName = snap.OwnUserName
	data.FetchedAt = snap.FetchedAt
	data.FetchedAtJST = formatFetchedJST(snap.FetchedAt)
	data.FetchedSince = humanizeSince(time.Now(), snap.FetchedAt)
	data.CacheStale = isStale(snap.FetchedAt, 30*time.Minute)
	if data.CacheStale {
		data.StaleLabel = staleLabel(time.Now(), snap.FetchedAt)
	}
	data.Total = len(snap.Records)
	data.Counts = countByStatus(snap.Records)
	data.StatusOrder = statusOrder
	data.Buckets = h.buildMentionBuckets(snap, time.Now())
	now := time.Now()
	data.MyIssueGroups = h.buildMyIssueGroups(snap, now)
	data.MyIssueActive = h.buildMyIssueActive(snap, now)
	data.MyIssuePassed = h.buildMyIssuePassed(snap, now)
	data.MyIssueKanban = h.buildMyIssueKanban(snap, now)
	data.MyIssueTotal = len(snap.MyIssues)
	data.APICallCount = snap.APICallCount

	h.render(c, "index.html", http.StatusOK, data)
}

func (h *Handler) render(c *gin.Context, name string, status int, data any) {
	t, ok := h.templates[name]
	if !ok {
		c.String(http.StatusInternalServerError, "Template error")
		return
	}
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(c.Writer, "base", data); err != nil {
		c.String(http.StatusInternalServerError, "Template error")
	}
}

var statusOrder = []string{
	backlog.StatusUnhandled,
	backlog.StatusHandled,
	backlog.StatusCC,
}

// displayStatus は内部ステータス（Checked / Replied）を表示用の集約ステータス（対応済）に畳む。
// 個別カードの ↩ ★ badge で内訳は引き続き判別可能。
func displayStatus(s string) string {
	switch s {
	case backlog.StatusChecked, backlog.StatusReplied:
		return backlog.StatusHandled
	}
	return s
}

func countByStatus(records []backlog.Record) map[string]int {
	m := map[string]int{}
	for _, s := range statusOrder {
		m[s] = 0
	}
	for _, r := range records {
		m[displayStatus(r.Status)]++
	}
	return m
}

func (h *Handler) toRecordView(snap *backlog.Snapshot, now time.Time, r backlog.Record) recordView {
	return recordView{
		NotificationID:      r.NotificationID,
		NotifiedAtJST:       fallbackJST(r),
		NotifiedSince:       humanizeSince(now, r.NotifiedAt),
		IssueKey:            r.IssueKey,
		IssueSummary:        r.IssueSummary,
		IssueStatus:         r.IssueStatus,
		Status:              displayStatus(r.Status),
		CCReason:            r.CCReason,
		Sender:              r.Sender,
		Assignee:            r.Assignee,
		Creator:             r.Creator,
		IsAssignee:          snap.OwnUserName != "" && r.Assignee == snap.OwnUserName,
		IsCreator:           snap.OwnUserName != "" && r.Creator == snap.OwnUserName,
		ContentExcerpt:      template.HTML(h.autolinkAndEscape(r.ContentExcerpt)),
		IssueURL:            r.IssueURL,
		CommentURL:          r.CommentURL,
		CommentID:           r.CommentID,
		CommentHistoryTitle: r.CommentHistoryTitle,
		CommentHistory:      template.HTML(h.renderHistory(r.CommentHistory)),
		Starred:             r.Starred,
		Replied:             r.Replied,
		AtMentioned:         r.AtMentioned,
		SilentClose:         r.SilentClose,
		IsEvent:             r.IsEvent,
		EventFieldsLabel:    eventFieldsLabel(r.EventFields),
	}
}

// eventFieldLabels は Backlog API の changeLog.field を和訳した表示ラベル。
// 一覧は Backlog API ドキュメント (https://developer.nulab.com/docs/backlog/api/2/get-comment-list/) より抜粋。
// 未知のフィールドはそのまま文字列を返す（新規追加 field の検出にもなる）。
var eventFieldLabels = map[string]string{
	"assigner":         "担当者変更",
	"status":           "ステータス変更",
	"resolution":       "完了理由変更",
	"summary":          "件名変更",
	"description":      "詳細変更",
	"priority":         "優先度変更",
	"limitDate":        "期限日変更",
	"startDate":        "開始日変更",
	"estimatedHours":   "予定時間変更",
	"actualHours":      "実績時間変更",
	"issueType":        "種別変更",
	"category":         "カテゴリー変更",
	"version":          "発生バージョン変更",
	"milestone":        "マイルストーン変更",
	"component":        "コンポーネント変更",
	"parentIssue":      "親課題変更",
	"attachment":       "添付ファイル変更",
	"notification":     "お知らせ変更",
	"commit":           "コミット連携",
	"pullRequest":      "プルリクエスト連携",
	"pullRequestComment": "プルリク コメント連携",
	"externalFile":     "外部ファイル変更",
}

func eventFieldsLabel(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	labels := make([]string, 0, len(fields))
	for _, f := range fields {
		if l, ok := eventFieldLabels[f]; ok {
			labels = append(labels, l)
		} else {
			labels = append(labels, f)
		}
	}
	return strings.Join(labels, ", ")
}

// メンションを「課題単位」に集約し、最新通知時刻で時間バケット (1h/6h/24h/1w/それ以前)
// に振り分ける。空バケットは結果から除外する。
type mentionBucketDef struct {
	Label string
	Slug  string
	Max   time.Duration // 0 = catch-all (それ以前)
}

var mentionBucketDefs = []mentionBucketDef{
	{"1時間以内", "1h", 1 * time.Hour},
	{"6時間以内", "6h", 6 * time.Hour},
	{"24時間以内", "24h", 24 * time.Hour},
	{"1週間以内", "1w", 7 * 24 * time.Hour},
	{"1ヶ月以内", "1m", 30 * 24 * time.Hour},
	{"1ヶ月より前", "older", 0},
}

func parseNotifiedAt(iso string) (time.Time, bool) {
	if iso == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.Replace(iso, "Z", "+00:00", 1))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func bucketSlugFor(now time.Time, isoUTC string) string {
	t, ok := parseNotifiedAt(isoUTC)
	if !ok {
		return "older"
	}
	elapsed := now.Sub(t)
	for _, def := range mentionBucketDefs {
		if def.Max == 0 {
			continue
		}
		if elapsed <= def.Max {
			return def.Slug
		}
	}
	return "older"
}

func humanizeSince(now time.Time, isoUTC string) string {
	t, ok := parseNotifiedAt(isoUTC)
	if !ok {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d分前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d時間前", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d日前", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d週間前", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dヶ月前", int(d.Hours()/(24*30)))
	}
}

// 各メンションを個別に時間バケットへ振り分ける（IssueKey 集約はしない）。
// 同じ課題への複数メンションは独立した行として時系列に並ぶ。
func (h *Handler) buildMentionBuckets(snap *backlog.Snapshot, now time.Time) []mentionBucket {
	sorted := make([]backlog.Record, len(snap.Records))
	copy(sorted, snap.Records)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NotifiedAt > sorted[j].NotifiedAt })

	bucketRecords := map[string][]recordView{}
	for _, r := range sorted {
		slug := bucketSlugFor(now, r.NotifiedAt)
		bucketRecords[slug] = append(bucketRecords[slug], h.toRecordView(snap, now, r))
	}

	result := make([]mentionBucket, 0, len(mentionBucketDefs))
	for _, def := range mentionBucketDefs {
		recs := bucketRecords[def.Slug]
		if len(recs) == 0 {
			continue
		}
		result = append(result, mentionBucket{
			Label:   def.Label,
			Slug:    def.Slug,
			Records: recs,
			Count:   len(recs),
		})
	}
	return result
}

func (h *Handler) toMyIssueView(r backlog.MyIssueRecord, now time.Time) myIssueView {
	return myIssueView{
		IssueKey:              r.IssueKey,
		IssueSummary:          r.IssueSummary,
		IssueStatus:           r.IssueStatus,
		Priority:              r.Priority,
		IssueType:             r.IssueType,
		Creator:               r.Creator,
		DueDate:               r.DueDate,
		UpdatedAtJST:          r.UpdatedAtJST,
		IssueURL:              r.IssueURL,
		Overdue:               r.Overdue,
		LastUserActivityAtJST: r.LastUserActivityAtJST,
		LatestSince:           humanizeSince(now, r.UpdatedAt),
		LatestSinceJST:        r.UpdatedAtJST,
		CommentHistoryTitle:   r.CommentHistoryTitle,
		CommentHistory:        template.HTML(h.renderHistory(r.CommentHistory)),
	}
}

// アクティブ順: max(自分の最終活動時刻, チケット更新時刻) を effective time とみなし、
// メンションと同じ時間バケット (1h / 6h / 24h / 1w / 1m / older) に振り分けて返す。
// 各バケット内は effective time 降順、空バケットは除外する。
// 並びキーは Backlog の RFC3339 UTC 文字列をそのまま辞書順比較で使う（時刻順と一致する）。
func (h *Handler) buildMyIssueActive(snap *backlog.Snapshot, now time.Time) []myIssueBucket {
	recs := make([]backlog.MyIssueRecord, len(snap.MyIssues))
	copy(recs, snap.MyIssues)
	keyOf := func(r backlog.MyIssueRecord) string {
		if r.LastUserActivityAt > r.UpdatedAt {
			return r.LastUserActivityAt
		}
		return r.UpdatedAt
	}
	sort.SliceStable(recs, func(i, j int) bool {
		return keyOf(recs[i]) > keyOf(recs[j])
	})

	bySlug := map[string][]myIssueView{}
	for _, r := range recs {
		eff := keyOf(r)
		v := h.toMyIssueView(r, now)
		v.LatestSince = humanizeSince(now, eff)
		// active モードでは活動が更新より新しい場合のみ tooltip も活動時刻に揃える
		if r.LastUserActivityAt > r.UpdatedAt && r.LastUserActivityAtJST != "" {
			v.LatestSinceJST = r.LastUserActivityAtJST
		}
		slug := bucketSlugFor(now, eff)
		bySlug[slug] = append(bySlug[slug], v)
	}

	out := make([]myIssueBucket, 0, len(mentionBucketDefs))
	for _, def := range mentionBucketDefs {
		issues := bySlug[def.Slug]
		if len(issues) == 0 {
			continue
		}
		out = append(out, myIssueBucket{
			Label:  def.Label,
			Slug:   def.Slug,
			Issues: issues,
			Count:  len(issues),
		})
	}
	return out
}

// kanban: MyIssueStatusOrder の displayOrder に従って全ステータスを列にし、各 issue を IssueStatus で
// 分配する。同一列内は UpdatedAt 降順。0 件のステータス列もそのまま描画する（プロセス全体を一望する
// 用途）。MyIssueStatusOrder に乗っていない未知ステータスは末尾の追加列としてマージする。
func (h *Handler) buildMyIssueKanban(snap *backlog.Snapshot, now time.Time) []myIssueKanbanColumn {
	statuses := make([]string, 0, len(snap.MyIssueStatusOrder))
	seen := map[string]bool{}
	for _, s := range snap.MyIssueStatusOrder {
		if seen[s] {
			continue
		}
		seen[s] = true
		statuses = append(statuses, s)
	}
	// 未知ステータスを末尾に追加（status 一覧取得が失敗したプロジェクトの分など）
	for _, r := range snap.MyIssues {
		if r.IssueStatus != "" && !seen[r.IssueStatus] {
			seen[r.IssueStatus] = true
			statuses = append(statuses, r.IssueStatus)
		}
	}

	byStatus := map[string][]myIssueView{}
	recs := make([]backlog.MyIssueRecord, len(snap.MyIssues))
	copy(recs, snap.MyIssues)
	sort.SliceStable(recs, func(i, j int) bool {
		return recs[i].UpdatedAt > recs[j].UpdatedAt
	})
	for _, r := range recs {
		byStatus[r.IssueStatus] = append(byStatus[r.IssueStatus], h.toMyIssueView(r, now))
	}

	out := make([]myIssueKanbanColumn, 0, len(statuses))
	for _, s := range statuses {
		issues := byStatus[s]
		out = append(out, myIssueKanbanColumn{
			Status: s,
			Issues: issues,
			Count:  len(issues),
		})
	}
	return out
}

// パス済み: 自分から他人に担当を振り直した直近の課題を時系列降順で並べる。
// snapshot.PassedIssues は既に降順ソート済みなので、ここでは表示用整形のみを行う。
func (h *Handler) buildMyIssuePassed(snap *backlog.Snapshot, now time.Time) []passedIssueView {
	out := make([]passedIssueView, 0, len(snap.PassedIssues))
	for _, r := range snap.PassedIssues {
		out = append(out, passedIssueView{
			IssueKey:            r.IssueKey,
			IssueSummary:        r.IssueSummary,
			PassedAtJST:         r.PassedAtJST,
			PassedSince:         humanizeSince(now, r.PassedAt),
			PassedTo:            r.PassedTo,
			IssueURL:            r.IssueURL,
			CommentHistoryTitle: r.CommentHistoryTitle,
			CommentHistory:      template.HTML(h.renderHistory(r.CommentHistory)),
		})
	}
	return out
}

func (h *Handler) buildMyIssueGroups(snap *backlog.Snapshot, now time.Time) []myIssueGroup {
	// Collect which issue IDs appear as a parent of another issue in the list
	parentIDs := map[int]bool{}
	for _, r := range snap.MyIssues {
		if r.ParentIssueID > 0 {
			parentIDs[r.ParentIssueID] = true
		}
	}

	// Group children by parentIssueID; collect parent meta
	type parentMeta struct {
		Key, Summary, URL string
	}
	grouped := map[int][]myIssueView{}
	meta := map[int]parentMeta{}
	var standalone []myIssueView

	for _, r := range snap.MyIssues {
		if r.ParentIssueID > 0 {
			grouped[r.ParentIssueID] = append(grouped[r.ParentIssueID], h.toMyIssueView(r, now))
			if _, ok := meta[r.ParentIssueID]; !ok {
				meta[r.ParentIssueID] = parentMeta{r.ParentIssueKey, r.ParentIssueSummary, r.ParentIssueURL}
			}
		} else if !parentIDs[r.IssueID] {
			// No parent and not itself a parent of others → standalone
			standalone = append(standalone, h.toMyIssueView(r, now))
		}
		// If this issue IS a parent of others in the list, skip it from standalone;
		// it will appear only as a group header.
	}

	// Stable sort of groups by parent key
	type entry struct {
		pid int
		m   parentMeta
	}
	entries := make([]entry, 0, len(grouped))
	for pid := range grouped {
		entries = append(entries, entry{pid, meta[pid]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].m.Key < entries[j].m.Key })

	var groups []myIssueGroup
	if len(standalone) > 0 {
		groups = append(groups, myIssueGroup{HasParent: false, Issues: standalone})
	}
	for _, e := range entries {
		groups = append(groups, myIssueGroup{
			HasParent:     true,
			ParentKey:     e.m.Key,
			ParentSummary: e.m.Summary,
			ParentURL:     e.m.URL,
			Issues:        grouped[e.pid],
		})
	}
	return groups
}

func fallbackJST(r backlog.Record) string {
	if r.NotifiedAtJST != "" {
		return r.NotifiedAtJST
	}
	return r.NotifiedAt
}

func formatFetchedJST(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		jst = time.FixedZone("JST", 9*60*60)
	}
	return t.In(jst).Format("2006-01-02 15:04")
}

func isStale(iso string, threshold time.Duration) bool {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return true
	}
	return time.Since(t) > threshold
}

// staleLabel は CacheStale 時にツールチップへ出す文言を組み立てる。
// 「⚠ stale」だけだと OS sleep 由来か恒常障害か判別しづらいため、
// 前回 fetch からの経過時間を併記して深刻度を一目で分かるようにする。
func staleLabel(now time.Time, isoUTC string) string {
	t, err := time.Parse(time.RFC3339, isoUTC)
	if err != nil {
		return "⚠ stale"
	}
	d := now.Sub(t)
	var dur string
	switch {
	case d < time.Hour:
		dur = fmt.Sprintf("%d分", int(d.Minutes()))
	case d < 24*time.Hour:
		dur = fmt.Sprintf("%d時間", int(d.Hours()))
	default:
		dur = fmt.Sprintf("%d日", int(d.Hours()/24))
	}
	return fmt.Sprintf("⚠ stale (%s fetch 停止)", dur)
}

// renderHistory turns the markdown comment history into a minimal HTML
// fragment. Each ### block becomes a div.comment-block; the mention source
// gets an additional "mention-source" class for background highlighting.
func (h *Handler) renderHistory(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="history">`)
	inBlock := false
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, " \r")
		switch {
		case strings.HasPrefix(line, "### "):
			if inBlock {
				b.WriteString("</div>")
			}
			body := line[4:]
			classes := "comment-block"
			if strings.HasPrefix(body, "[ME] ") {
				classes += " own-comment"
				body = body[len("[ME] "):]
			}
			if strings.Contains(body, "👈") {
				classes += " mention-source"
			}
			fmt.Fprintf(&b, `<div class="%s">`, classes)
			inBlock = true
			fmt.Fprintf(&b, "<h5>%s</h5>", h.renderInline(body))
		case line == "":
			// skip
		default:
			fmt.Fprintf(&b, "<p>%s</p>", h.renderInline(line))
		}
	}
	if inBlock {
		b.WriteString("</div>")
	}
	b.WriteString("</div>")
	return b.String()
}

// safeURL は scheme allowlist + 任意の URL prefix allowlist を満たすときだけ raw を返し、
// それ以外は "#" を返す。
//   - scheme: http / https / mailto のみ常時 OK。javascript: / data: / vbscript: 等は常時拒否。
//   - prefix: h.linkAllowPrefixes が空なら scheme チェックのみで通す（デフォルト動作）。
//     非空ならいずれかの prefix と前方一致しない URL は "#" に置換する。
//     これにより「自テナント + 明示許可した GitHub 等のみリンク化」という運用が可能。
func (h *Handler) safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "#"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
	default:
		return "#"
	}
	if len(h.linkAllowPrefixes) == 0 {
		return raw
	}
	for _, p := range h.linkAllowPrefixes {
		if strings.HasPrefix(raw, p) {
			return raw
		}
	}
	return "#"
}

// renderInline は markdown 記法 [text](url) のリンクを処理する。
// [text](url) ブロックの外側に裸の http(s):// URL があれば autolinkAndEscape で
// クリッカブル化する（allowlist 設定に従って素テキスト or <a> が選ばれる）。
func (h *Handler) renderInline(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "[")
		if i < 0 {
			b.WriteString(h.autolinkAndEscape(s))
			break
		}
		b.WriteString(h.autolinkAndEscape(s[:i]))
		rest := s[i+1:]
		end := strings.Index(rest, "](")
		if end < 0 {
			b.WriteString(template.HTMLEscapeString(s[i:]))
			break
		}
		text := rest[:end]
		rest = rest[end+2:]
		close := strings.Index(rest, ")")
		if close < 0 {
			b.WriteString(template.HTMLEscapeString(s[i:]))
			break
		}
		rawURL := rest[:close]
		fmt.Fprintf(&b, `<a href="%s" target="_blank" rel="noopener">%s</a>`,
			template.HTMLEscapeString(h.safeURL(rawURL)),
			template.HTMLEscapeString(text),
		)
		s = rest[close+1:]
	}
	return b.String()
}

// bareURLPattern は autolink で拾う裸 URL の正規表現。
// 終端を貪欲にしすぎないよう、空白・引用符・基本的な記号で停止する。
// URL の末尾に張り付いた句読点 (".", ",", ";", ":", "!", "?", ")", "]" 等) は
// 後段 trimURLTrailingPunct で URL から外して別途エスケープ出力する。
var bareURLPattern = regexp.MustCompile(`https?://[^\s<>"'\x60]+`)

// autolinkAndEscape は s 内の裸 http(s):// URL を <a> でラップし、
// それ以外の部分は HTML エスケープして連結する。
// safeURL が "#" を返す URL（allowlist 外 / scheme 不正）はリンク化せず素テキストとして残す。
func (h *Handler) autolinkAndEscape(s string) string {
	idxs := bareURLPattern.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return template.HTMLEscapeString(s)
	}
	var b strings.Builder
	pos := 0
	for _, idx := range idxs {
		start, end := idx[0], idx[1]
		b.WriteString(template.HTMLEscapeString(s[pos:start]))
		raw := s[start:end]
		core, trail := trimURLTrailingPunct(raw)
		href := h.safeURL(core)
		if href == "#" {
			// allowlist 外 / 不正 scheme: クリッカブルにせず素テキストとして出す
			b.WriteString(template.HTMLEscapeString(raw))
		} else {
			fmt.Fprintf(&b, `<a href="%s" target="_blank" rel="noopener">%s</a>%s`,
				template.HTMLEscapeString(href),
				template.HTMLEscapeString(core),
				template.HTMLEscapeString(trail),
			)
		}
		pos = end
	}
	b.WriteString(template.HTMLEscapeString(s[pos:]))
	return b.String()
}

// trimURLTrailingPunct は URL 末尾の句読点を URL 本体から切り離す。
// 例: "https://example.com/path." → ("https://example.com/path", ".")
// 末尾が ")" の場合は URL 内の "(" と対応が取れているなら維持する
// （Wikipedia 等の URL 末尾 ")" を切らないため）。
func trimURLTrailingPunct(u string) (core, trail string) {
	end := len(u)
	for end > 0 {
		r := u[end-1]
		switch r {
		case '.', ',', ';', ':', '!', '?', ']', '}', '\'', '"':
			end--
			continue
		case ')':
			// 対応する "(" が URL 内にあれば末尾 ")" を URL の一部とみなす
			if strings.Count(u[:end], "(") > strings.Count(u[:end-1], ")") {
				return u[:end], u[end:]
			}
			end--
			continue
		}
		break
	}
	return u[:end], u[end:]
}
