// Package client talks to the control API of a running pingmock, to change
// rules and read statistics and history from Go code, typically tests.
//
// The simplest way to test Go code against pingmock is to run the whole
// test binary inside "pingmock exec", which needs no root:
//
//	go test -exec 'pingmock exec -rule 10.99.0.1=50ms --' ./...
//
// and to connect to it from the tests with FromEnv:
//
//	func TestFailover(t *testing.T) {
//		pm, err := client.FromEnv()
//		if err != nil {
//			t.Skip(err) // not running under pingmock exec
//		}
//		ctx := context.Background()
//		if err := pm.SetRule(ctx, "10.99.0.1", "500ms"); err != nil {
//			t.Fatal(err)
//		}
//		// ... exercise the code under test, which pings 10.99.0.1 ...
//		events, err := pm.History(ctx, "10.99.0.1", 0)
//		// ...
//	}
//
// Rules use the same text form as pingmock's -rule flag, such as "50ms",
// "delay=200ms,jitter=30ms,loss=5%", "unreachable" or
// "20ms for 5 pings; timeout".
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvAPI is the environment variable in which pingmock exec passes the
// API address to the command it runs.
const EnvAPI = "PINGMOCK_API"

// DefaultURL is where pingmock listens unless told otherwise with -api.
const DefaultURL = "http://127.0.0.1:9100"

// Client calls the pingmock control API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for the API at baseURL, such as
// "http://127.0.0.1:9100". If baseURL is empty, the address in
// $PINGMOCK_API is used, and DefaultURL if that is empty too.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv(EnvAPI)
	}
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 10 * time.Second}}
}

// ErrNotUnderExec is returned by FromEnv outside of pingmock exec.
var ErrNotUnderExec = errors.New("$" + EnvAPI + " is not set: not running under pingmock exec " +
	"(run the tests with: go test -exec 'pingmock exec [flags] --' ./...)")

// FromEnv returns a client for the pingmock that runs this process through
// pingmock exec, or ErrNotUnderExec if there is none. Tests can skip
// themselves on that error.
func FromEnv() (*Client, error) {
	u := os.Getenv(EnvAPI)
	if u == "" {
		return nil, ErrNotUnderExec
	}
	return New(u), nil
}

// URL returns the base URL of the API.
func (c *Client) URL() string { return c.baseURL }

// Error is an error response from the API.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pingmock API: %s (HTTP %d)", e.Message, e.StatusCode)
}

// IsNotFound reports whether err is an API response saying that the rule
// or resource does not exist.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// SetRule installs rule for target, an IP address or CIDR prefix, replacing
// any rule for the same target. Scenarios restart from their first phase.
func (c *Client) SetRule(ctx context.Context, target, rule string) error {
	return c.do(ctx, http.MethodPut, "/rules/"+target, rule, nil)
}

// DeleteRule removes the rule for target, so that the default applies again.
// It fails with an error for which IsNotFound is true if there is none.
func (c *Client) DeleteRule(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodDelete, "/rules/"+target, "", nil)
}

// SetDefault sets the rule for targets that no rule matches.
func (c *Client) SetDefault(ctx context.Context, rule string) error {
	return c.do(ctx, http.MethodPut, "/default", rule, nil)
}

// State is a snapshot of the seed, the rules and the per-target statistics.
type State struct {
	Seed    uint64                 `json:"seed"`
	Default string                 `json:"default"`
	Rules   []Rule                 `json:"rules"`
	Stats   map[string]TargetStats `json:"stats"`
}

// Rule is an installed rule.
type Rule struct {
	Target string `json:"target"` // prefix, e.g. "10.99.0.1/32"
	Rule   string `json:"rule"`   // canonical text form
}

// TargetStats counts the requests to one target address.
type TargetStats struct {
	Requests    uint64 `json:"requests"`
	Replied     uint64 `json:"replied"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
	Rule        string `json:"rule"`  // matching prefix, or "default"
	Phase       int    `json:"phase"` // current phase, 1-based
}

// State returns the current rules and statistics.
func (c *Client) State(ctx context.Context) (*State, error) {
	var s State
	if err := c.do(ctx, http.MethodGet, "/rules", "", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Stats returns the statistics of one target address; all counters are
// zero if it has not been pinged.
func (c *Client) Stats(ctx context.Context, addr string) (TargetStats, error) {
	s, err := c.State(ctx)
	if err != nil {
		return TargetStats{}, err
	}
	return s.Stats[addr], nil
}

// ResetStats zeroes all counters.
func (c *Client) ResetStats(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/stats", "", nil)
}

// Event describes how one echo request was answered.
type Event struct {
	Time   time.Time `json:"time"`
	Src    string    `json:"src"`
	Dst    string    `json:"dst"`
	ID     uint16    `json:"id"`
	Seq    uint16    `json:"seq"`
	Result string    `json:"result"` // "reply", "drop" or "unreachable"
	Code   string    `json:"code"`   // for "unreachable": "host", "net" or "admin"
	Reply  bool      `json:"reply"`
	Delay  string    `json:"delay"` // empty for drops
	Rule   string    `json:"rule"`
	Phase  int       `json:"phase"` // 1-based
}

// History returns recent requests, oldest first. target, if not empty, is
// an address or prefix to filter on; limit, if positive, keeps only the
// most recent requests.
func (c *Client) History(ctx context.Context, target string, limit int) ([]Event, error) {
	q := url.Values{}
	if target != "" {
		q.Set("target", target)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/history"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var resp struct {
		Events []Event `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, path, "", &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// ClearHistory forgets all recorded requests.
func (c *Client) ClearHistory(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/history", "", nil)
}

func (c *Client) do(ctx context.Context, method, path, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pingmock API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("pingmock API: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &Error{StatusCode: resp.StatusCode, Message: msg}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("pingmock API: decoding %s %s: %w", method, path, err)
		}
	}
	return nil
}
