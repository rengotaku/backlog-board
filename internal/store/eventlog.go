package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rengotaku/backlog-board/internal/backlog"
)

// EventLog は cold 層（イベント履歴 + 完了/パス課題アーカイブ）への append-only writer。
// snapshot.json と同じデータディレクトリ配下の history/ に置く:
//   - events-YYYY-MM.jsonl : 月次ローテーションするイベントログ（遡りたい月だけ読む）
//   - archive.jsonl        : 追跡対象から外れた課題の最終既知状態
//
// snapshot.json と同様、Backlog コメント本文を含み得るため dir 0o700 / file 0o600 を強制する。
type EventLog struct {
	Dir string
}

// NewEventLog は snapshot.json と同じディレクトリの history/ を使う EventLog を返す。
func NewEventLog(snapshotPath string) *EventLog {
	return &EventLog{Dir: filepath.Join(filepath.Dir(snapshotPath), "history")}
}

// AppendEvents はイベントを月別ファイルへ追記する。イベントの TS（RFC3339）先頭 7 文字
// "YYYY-MM" でローテーションする。空スライスは no-op。
func (l *EventLog) AppendEvents(events []backlog.Event) error {
	if len(events) == 0 {
		return nil
	}
	byMonth := map[string][]backlog.Event{}
	for _, e := range events {
		m := monthBucket(e.TS)
		byMonth[m] = append(byMonth[m], e)
	}
	// 月キーを安定順で処理（部分失敗時も決定的に）。
	months := make([]string, 0, len(byMonth))
	for m := range byMonth {
		months = append(months, m)
	}
	sort.Strings(months)
	for _, m := range months {
		lines := make([][]byte, 0, len(byMonth[m]))
		for _, e := range byMonth[m] {
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			lines = append(lines, b)
		}
		if err := l.appendLines(filepath.Join(l.Dir, "events-"+m+".jsonl"), lines); err != nil {
			return err
		}
	}
	return nil
}

// AppendArchive はアーカイブ対象を archive.jsonl へ追記する。空スライスは no-op。
func (l *EventLog) AppendArchive(entries []backlog.ArchiveEntry) error {
	if len(entries) == 0 {
		return nil
	}
	lines := make([][]byte, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		lines = append(lines, b)
	}
	return l.appendLines(filepath.Join(l.Dir, "archive.jsonl"), lines)
}

// appendLines は各バイト列を 1 行（末尾改行付き）として O_APPEND で追記する。
// 1 回の Write にまとめることでローカル FS 上の追記アトミック性を最大化する。
func (l *EventLog) appendLines(path string, lines [][]byte) error {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, b := range lines {
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// O_CREATE の perm は umask の影響を受けるため、コメント本文を含む既存ファイルが
	// 緩い権限で残らないよう明示的に 0o600 を保証する（cache.go と同じ best-effort 方針）。
	_ = f.Chmod(0o600)
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

// ReadEvents は events-<month>.jsonl を読み、壊れた行（途中書き込み等）はスキップして返す。
// ファイル未作成時は空スライス。Phase 2 の遡上読取・テスト用。
func (l *EventLog) ReadEvents(month string) ([]backlog.Event, error) {
	out := []backlog.Event{}
	err := readJSONL(filepath.Join(l.Dir, "events-"+month+".jsonl"), func(line []byte) {
		var e backlog.Event
		if json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	})
	return out, err
}

// ReadArchive は archive.jsonl を読み、壊れた行はスキップして返す。未作成時は空スライス。
func (l *EventLog) ReadArchive() ([]backlog.ArchiveEntry, error) {
	out := []backlog.ArchiveEntry{}
	err := readJSONL(filepath.Join(l.Dir, "archive.jsonl"), func(line []byte) {
		var e backlog.ArchiveEntry
		if json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	})
	return out, err
}

// ListEventMonths は events-YYYY-MM.jsonl の "YYYY-MM" 部分を昇順で返す。
// history ディレクトリ未作成時は空スライス。
func (l *EventLog) ListEventMonths() ([]string, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	months := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "events-") && strings.HasSuffix(n, ".jsonl") {
			months = append(months, strings.TrimSuffix(strings.TrimPrefix(n, "events-"), ".jsonl"))
		}
	}
	sort.Strings(months)
	return months, nil
}

// ReadEventsForIssue は全月ファイルを走査し、指定 issue_id のイベントだけを
// 月昇順・各月内は追記（時系列）順で返す。並べ替えは呼び出し側に委ねる。
func (l *EventLog) ReadEventsForIssue(issueID int) ([]backlog.Event, error) {
	months, err := l.ListEventMonths()
	if err != nil {
		return nil, err
	}
	out := []backlog.Event{}
	for _, m := range months {
		evs, err := l.ReadEvents(m)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.IssueID == issueID {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// readJSONL は path を 1 行ずつ読み、空行を飛ばし、各行を onLine に渡す。
// ファイル未作成は nil（エラーにしない）。壊れた行の扱いは onLine 側に委ねる
// （Unmarshal 失敗を黙ってスキップする想定）。
// 返り値の error はスキャナー I/O エラー（ファイル故障等）であり、行レベルの
// パース失敗とは別物。渡される line は scanner の内部バッファ参照なので、
// onLine は同期的に消費する（保持・goroutine 送出する場合は自前でコピーすること）。
func readJSONL(path string, onLine func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		onLine(line)
	}
	return sc.Err()
}

// monthBucket は RFC3339 タイムスタンプから "YYYY-MM" を取り出す。
// 不正・短すぎる場合は "unknown" バケットに退避し、ログ行を失わない。
func monthBucket(ts string) string {
	if len(ts) >= 7 {
		return ts[:7]
	}
	return "unknown"
}
