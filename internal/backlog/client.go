package backlog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type Client struct {
	Domain   string
	APIKey   string
	HTTP     *http.Client
	apiCalls atomic.Int64
}

// APICalls は累計の API 呼び出し回数を返す。Fetch ごとの差分を取るのに使う。
func (c *Client) APICalls() int64 {
	return c.apiCalls.Load()
}

func NewClient(domain, apiKey string) *Client {
	return &Client{
		Domain: domain,
		APIKey: apiKey,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) get(path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("apiKey", c.APIKey)
	u := fmt.Sprintf("https://%s/api/v2/%s?%s", c.Domain, path, params.Encode())

	c.apiCalls.Add(1)
	resp, err := c.HTTP.Get(u)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Priority struct {
	Name string `json:"name"`
}

type IssueType struct {
	Name string `json:"name"`
}

type Issue struct {
	ID            int    `json:"id"`
	IssueKey      string `json:"issueKey"`
	Summary       string `json:"summary"`
	Status        *struct {
		Name string `json:"name"`
	} `json:"status"`
	Priority      *Priority  `json:"priority"`
	IssueType     *IssueType `json:"issueType"`
	Assignee      *User      `json:"assignee"`
	CreatedUser   *User      `json:"createdUser"`
	DueDate       string     `json:"dueDate"`
	Created       string     `json:"created"`
	Updated       string     `json:"updated"`
	ParentIssueId *int       `json:"parentIssueId"`
}

type Star struct {
	Presenter *User `json:"presenter"`
}

type ChangeLog struct {
	Field string `json:"field"`
}

type Comment struct {
	ID          int         `json:"id"`
	Content     string      `json:"content"`
	Created     string      `json:"created"`
	CreatedUser *User       `json:"createdUser"`
	Stars       []Star      `json:"stars"`
	ChangeLog   []ChangeLog `json:"changeLog"`
}

func (c *Comment) IsEvent() bool {
	return strings.TrimSpace(c.Content) == "" && len(c.ChangeLog) > 0
}

type Notification struct {
	ID                  int64    `json:"id"`
	Created             string   `json:"created"`
	AlreadyRead         bool     `json:"alreadyRead"`
	ResourceAlreadyRead bool     `json:"resourceAlreadyRead"`
	Reason              int      `json:"reason"`
	Sender              *User    `json:"sender"`
	Issue               *Issue   `json:"issue"`
	Comment             *Comment `json:"comment"`
}

func (c *Client) Myself() (*User, error) {
	var u User
	if err := c.get("users/myself", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) Notifications(count int) ([]Notification, error) {
	var ns []Notification
	params := url.Values{
		"count": {fmt.Sprintf("%d", count)},
		"order": {"desc"},
	}
	if err := c.get("notifications", params, &ns); err != nil {
		return nil, err
	}
	return ns, nil
}

func (c *Client) IssueComments(issueID, count int) ([]Comment, error) {
	var cs []Comment
	params := url.Values{
		"count": {fmt.Sprintf("%d", count)},
		"order": {"desc"},
	}
	if err := c.get(fmt.Sprintf("issues/%d/comments", issueID), params, &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func (c *Client) IssueComment(issueID, commentID int) (*Comment, error) {
	var cm Comment
	if err := c.get(fmt.Sprintf("issues/%d/comments/%d", issueID, commentID), nil, &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func (c *Client) Issues(params url.Values) ([]Issue, error) {
	var issues []Issue
	if err := c.get("issues", params, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func (c *Client) IssueByID(id int) (*Issue, error) {
	var issue Issue
	if err := c.get(fmt.Sprintf("issues/%d", id), nil, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

type Status struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	DisplayOrder int    `json:"displayOrder"`
}

func (c *Client) ProjectStatuses(projectKey string) ([]Status, error) {
	var ss []Status
	if err := c.get(fmt.Sprintf("projects/%s/statuses", projectKey), nil, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

// ActivityProject は Activity に紐づくプロジェクト情報のうち、画面表示で必要な部分のみを保持する。
type ActivityProject struct {
	ID         int    `json:"id"`
	ProjectKey string `json:"projectKey"`
}

// ActivityChange は Activity の content.changes の各エントリ。
// assigner / status / priority 等のフィールド変更を表し、値は表示名（User.Name 等）の文字列。
type ActivityChange struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

// Activity は /users/:userId/activities のレスポンスを表す。
// 課題関連のアクティビティでは content.id に課題 ID が入る。
type Activity struct {
	ID          int64            `json:"id"`
	Type        int              `json:"type"`
	Created     string           `json:"created"`
	CreatedUser *User            `json:"createdUser"`
	Project     *ActivityProject `json:"project"`
	Content     struct {
		ID      int              `json:"id"`
		KeyID   int              `json:"key_id"`
		Summary string           `json:"summary"`
		Changes []ActivityChange `json:"changes"`
	} `json:"content"`
}

// UserActivities は対象ユーザーの最近のアクティビティを取得する。
// typeIDs を指定すると activityTypeId[] フィルタが付与される（空なら全種別）。
func (c *Client) UserActivities(userID, count int, typeIDs ...int) ([]Activity, error) {
	var as []Activity
	params := url.Values{
		"count": {fmt.Sprintf("%d", count)},
		"order": {"desc"},
	}
	for _, t := range typeIDs {
		params.Add("activityTypeId[]", fmt.Sprintf("%d", t))
	}
	if err := c.get(fmt.Sprintf("users/%d/activities", userID), params, &as); err != nil {
		return nil, err
	}
	return as, nil
}
