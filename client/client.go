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

// State is a snapshot of the seed, the rules, the paths and the
// per-target statistics.
type State struct {
	Seed    uint64                 `json:"seed"`
	Default string                 `json:"default"`
	Rules   []Rule                 `json:"rules"`
	Paths   []Path                 `json:"paths"`
	Stats   map[string]TargetStats `json:"stats"`
	// TCP rules, and the statistics of each address and port that got a
	// connection attempt, keyed as "10.99.0.1:5432" or "[fd99::1]:443".
	TCP      []Rule              `json:"tcp"`
	TCPStats map[string]TCPStats `json:"tcp_stats"`
	// DNS rules, under pingmock exec, and the statistics of each name and
	// query type that got a query, keyed as "api.example.com/AAAA".
	DNS      []Rule              `json:"dns"`
	DNSStats map[string]DNSStats `json:"dns_stats"`
	// UDP rules, and the statistics of each address and port that got a
	// datagram, keyed as TCP's are.
	UDP      []Rule              `json:"udp"`
	UDPStats map[string]UDPStats `json:"udp_stats"`
	// Routes are the targets routed to pingmock besides its own subnets,
	// such as "8.8.8.8/32": those the rules and paths need, which stop
	// being routed once no rule needs them.
	Routes []string `json:"routes"`
	// Outside is the host's network that pingmock exec -outside gives the
	// command; nil without it.
	Outside *Outside `json:"outside,omitempty"`
}

// Outside describes the outside access of pingmock exec -outside: the
// command's address and gateway in it, and the host loopback ports the
// command reaches, such as "5432/tcp".
type Outside struct {
	Address string   `json:"address"`
	Gateway string   `json:"gateway"`
	Ports   []string `json:"ports"`
}

// UDPStats counts the datagrams sent to one address and port.
type UDPStats struct {
	Requests    uint64 `json:"requests"`
	Refused     uint64 `json:"refused"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
	Rule        string `json:"rule"`  // e.g. "udp 10.99.0.1/32:514"
	Phase       int    `json:"phase"` // current phase, 1-based
}

// DNSStats counts the queries for one name and type. Every query counts,
// including those a resolver sends again after SERVFAIL or a timeout.
type DNSStats struct {
	Requests uint64 `json:"requests"`
	Answered uint64 `json:"answered"`
	NXDomain uint64 `json:"nxdomain"`
	ServFail uint64 `json:"servfail"`
	Dropped  uint64 `json:"dropped"`
	Rule     string `json:"rule"`  // e.g. "dns api.example.com"
	Phase    int    `json:"phase"` // current phase, 1-based
}

// TCPStats counts the connection attempts to one address and port. A SYN
// the client's kernel sends again for the same attempt is not counted.
type TCPStats struct {
	Requests    uint64 `json:"requests"`
	Accepted    uint64 `json:"accepted"`
	Refused     uint64 `json:"refused"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
	// Accepted connections that pingmock then closed or reset (after=).
	Closed uint64 `json:"closed"`
	Reset  uint64 `json:"reset"`
	Rule   string `json:"rule"`  // e.g. "tcp 10.99.0.1/32:5432"
	Phase  int    `json:"phase"` // current phase, 1-based
}

// Rule is an installed rule.
type Rule struct {
	Target string `json:"target"` // prefix, e.g. "10.99.0.1/32"
	Rule   string `json:"rule"`   // canonical text form
}

// Path is the route to a target, as traceroute and mtr see it.
type Path struct {
	Target string `json:"target"` // address, e.g. "8.8.8.8"
	Path   string `json:"path"`   // canonical text form, hops separated by " > "
}

// TargetStats counts the requests to one target address.
type TargetStats struct {
	Requests    uint64 `json:"requests"`
	Replied     uint64 `json:"replied"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
	Hops        uint64 `json:"hops"`  // probes answered by a hop of the target's path
	Rule        string `json:"rule"`  // matching prefix, or "default"
	Phase       int    `json:"phase"` // current phase, 1-based
}

// SetPath gives target, an address, a route: traceroute and mtr see its
// hops, and UDP and TCP probes to it get answered. path is written as for
// the -path flag: hops separated by ">", each "ADDR@SETTINGS" (delay,
// jitter, loss, cut, ttl) or "*" for a hop that never answers, such as
// "192.168.1.1@2ms > * > 72.14.0.1@25ms".
func (c *Client) SetPath(ctx context.Context, target, path string) error {
	return c.do(ctx, http.MethodPut, "/paths/"+target, path, nil)
}

// DeletePath removes the route to target. It fails with an error for
// which IsNotFound is true if there is none.
func (c *Client) DeletePath(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodDelete, "/paths/"+target, "", nil)
}

// SetTCP sets how TCP connections to target are answered, replacing any
// TCP rule for the same target. target is ADDR[:PORT] or PREFIX[:PORT],
// IPv6 in brackets ("[fd99::1]:443"); without a port the rule covers every
// port. rule is written as for SetRule, plus "refuse" (answer with a
// reset) and 'r' in a pattern, such as "timeout",
// "refuse for 3 requests; 20ms" or "delay=50ms,pattern=..r".
func (c *Client) SetTCP(ctx context.Context, target, rule string) error {
	return c.do(ctx, http.MethodPut, "/tcp/"+escapeTarget(target), rule, nil)
}

// DeleteTCP removes the TCP rule for target, so that connections to it are
// ignored again. It fails with an error for which IsNotFound is true if
// there is none.
func (c *Client) DeleteTCP(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodDelete, "/tcp/"+escapeTarget(target), "", nil)
}

// TCPStats returns the statistics of one address and port, written as
// "10.99.0.1:5432" or "[fd99::1]:443"; all counters are zero if it has
// had no connection attempt.
func (c *Client) TCPStats(ctx context.Context, addrPort string) (TCPStats, error) {
	s, err := c.State(ctx)
	if err != nil {
		return TCPStats{}, err
	}
	return s.TCPStats[addrPort], nil
}

// SetUDP sets how UDP datagrams to target fail, replacing any UDP rule
// for the same target, written as for SetTCP. rule says how they fail:
// "refuse" (ICMP port unreachable), "unreachable", "timeout" or a
// pattern of 'x', 'u' and 'r', such as "timeout for 2 requests; refuse";
// pingmock sends no UDP answers.
func (c *Client) SetUDP(ctx context.Context, target, rule string) error {
	return c.do(ctx, http.MethodPut, "/udp/"+escapeTarget(target), rule, nil)
}

// DeleteUDP removes the UDP rule for target. It fails with an error for
// which IsNotFound is true if there is none.
func (c *Client) DeleteUDP(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodDelete, "/udp/"+escapeTarget(target), "", nil)
}

// UDPStats returns the statistics of one address and port, written as
// "10.99.0.1:514"; all counters are zero if it has had no datagram.
func (c *Client) UDPStats(ctx context.Context, addrPort string) (UDPStats, error) {
	s, err := c.State(ctx)
	if err != nil {
		return UDPStats{}, err
	}
	return s.UDPStats[addrPort], nil
}

// SetDNS sets how the command's DNS queries for name are answered, under
// pingmock exec, replacing any DNS rule for the name. rule is written as
// for SetRule, without unreachable and ttl, plus "nxdomain", "servfail"
// and 'n' and 's' in a pattern, such as "nxdomain",
// "servfail for 2 requests; 10ms" or "delay=2s". A name pingmock exec
// put in the command's /etc/hosts needs a DNS rule from the start
// (-dns NAME=0ms) for this to take effect, and the request fails if not.
func (c *Client) SetDNS(ctx context.Context, name, rule string) error {
	return c.do(ctx, http.MethodPut, "/dns/"+url.PathEscape(name), rule, nil)
}

// DeleteDNS removes the DNS rule for name, so that its queries are
// answered as without one. It fails with an error for which IsNotFound is
// true if there is none.
func (c *Client) DeleteDNS(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/dns/"+url.PathEscape(name), "", nil)
}

// DNSStats returns the statistics of the queries for name of type qtype
// ("A" or "AAAA"); all counters are zero if there has been none.
func (c *Client) DNSStats(ctx context.Context, name, qtype string) (DNSStats, error) {
	s, err := c.State(ctx)
	if err != nil {
		return DNSStats{}, err
	}
	return s.DNSStats[strings.ToLower(strings.TrimSuffix(name, "."))+"/"+qtype], nil
}

// escapeTarget escapes the brackets of an IPv6 target for the URL path.
func escapeTarget(target string) string {
	return strings.NewReplacer("[", "%5B", "]", "%5D").Replace(target)
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

// Event describes how one request was answered: an echo request, a probe
// of traceroute, a TCP connection attempt or segment, a UDP datagram or a
// DNS query.
type Event struct {
	Time   time.Time `json:"time"`
	Src    string    `json:"src"`
	Dst    string    `json:"dst"`
	ID     uint16    `json:"id"`
	Seq    uint16    `json:"seq"`
	Result string    `json:"result"` // "reply", "drop", "unreachable"; with a path also "time-exceeded", "port-unreachable", "syn-ack"; for TCP rules "syn-ack", "refused", "drop", "unreachable", "closed", "reset"; for DNS rules "answer", "nxdomain", "servfail", "drop"
	Code   string    `json:"code"`   // for "unreachable": "host", "net" or "admin"
	Reply  bool      `json:"reply"`
	Delay  string    `json:"delay"` // empty for drops
	Rule   string    `json:"rule"`
	Phase  int       `json:"phase"` // 1-based
	// For targets with a path, and TCP probes.
	Probe string `json:"probe"` // "udp", "tcp" or "dns"; empty for echo requests
	TTL   uint8  `json:"ttl"`   // the probe's TTL
	Hop   int    `json:"hop"`   // the hop that answered or lost it, from 1; 0 for the destination
	From  string `json:"from"`  // the address the answer came from
	// For TCP and UDP probes.
	Port   uint16 `json:"port"`   // the destination port
	Repeat bool   `json:"repeat"` // a SYN sent again for an attempt already answered, not counted again
	// For DNS queries for names with a DNS rule (Probe "dns").
	Name  string `json:"name"`  // the name asked for
	QType string `json:"qtype"` // "A", "AAAA", ...
}

// History returns recent requests, oldest first. target, if not empty, is
// an address or prefix to filter on, or a name for the DNS queries for it;
// limit, if positive, keeps only the most recent requests.
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

// Reset puts pingmock back as it was at startup: the rules and the default
// from its command line and configuration file, no statistics and no
// history. Targets restart their scenarios and their random sequences, so a
// test that calls Reset first starts from the same state on every run.
func (c *Client) Reset(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/reset", "", nil)
}

// Version returns the version of the running pingmock, as "pingmock
// -version" prints it.
func (c *Client) Version(ctx context.Context) (string, error) {
	var resp struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", "", &resp); err != nil {
		return "", err
	}
	return resp.Version, nil
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
