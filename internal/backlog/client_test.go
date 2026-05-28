package backlog

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactErr(t *testing.T) {
	c := &Client{Domain: "example.backlog.com", APIKey: "SECRETKEY123"}

	t.Run("nil なら nil", func(t *testing.T) {
		if got := c.redactErr(nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("APIKey 空なら何もしない", func(t *testing.T) {
		empty := &Client{}
		raw := errors.New("plain error")
		if got := empty.redactErr(raw); got != raw {
			t.Errorf("expected pass-through, got %v", got)
		}
	})

	t.Run("url.Error の URL からキーが伏字化される", func(t *testing.T) {
		ue := &url.Error{
			Op:  "Get",
			URL: "https://example.backlog.com/api/v2/users/myself?apiKey=SECRETKEY123",
			Err: errors.New("dial tcp: no such host"),
		}
		got := c.redactErr(ue)
		if strings.Contains(got.Error(), "SECRETKEY123") {
			t.Errorf("error still contains API key: %v", got)
		}
		if !strings.Contains(got.Error(), "<REDACTED>") {
			t.Errorf("expected REDACTED marker, got: %v", got)
		}
	})

	t.Run("単純な error 文字列でも安全網が効く", func(t *testing.T) {
		raw := errors.New("oops: apiKey=SECRETKEY123 leaked")
		got := c.redactErr(raw)
		if strings.Contains(got.Error(), "SECRETKEY123") {
			t.Errorf("error still contains API key: %v", got)
		}
		if !strings.Contains(got.Error(), "<REDACTED>") {
			t.Errorf("expected REDACTED marker, got: %v", got)
		}
	})

	t.Run("キーを含まないエラーはそのまま", func(t *testing.T) {
		raw := errors.New("decode failure")
		got := c.redactErr(raw)
		if got.Error() != "decode failure" {
			t.Errorf("expected unchanged, got: %v", got)
		}
	})
}
