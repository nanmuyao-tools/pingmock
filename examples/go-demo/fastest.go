// Package fastest picks the server with the lowest ping latency and moves
// to another one when it slows down or stops answering.
//
// It is the code under test in this example: it knows nothing about
// ghostwire and measures latency with the system's ping command, as many
// programs do. fastest_test.go tests it against ghostwire.
package fastest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result is the outcome of pinging one server.
type Result struct {
	Addr     string
	Sent     int
	Received int           // echo replies
	Avg      time.Duration // average round trip of the replies
	Errors   int           // ICMP errors such as "Destination Host Unreachable"
}

// Loss is the fraction of pings that got no reply.
func (r Result) Loss() float64 {
	if r.Sent == 0 {
		return 1
	}
	return 1 - float64(r.Received)/float64(r.Sent)
}

// Options control how servers are measured and chosen.
type Options struct {
	Count   int           // pings per server; default 3
	Timeout time.Duration // how long to wait for each reply; default 1s
	MaxLoss float64       // servers losing more than this are skipped; default 0.25
}

func (o Options) withDefaults() Options {
	if o.Count <= 0 {
		o.Count = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Second
	}
	if o.MaxLoss <= 0 {
		o.MaxLoss = 0.25
	}
	return o
}

var (
	replyLine = regexp.MustCompile(`bytes from .*time=([0-9.]+) ms`)
	errorLine = regexp.MustCompile(`(?i)unreachable|prohibited|filtered`)
)

// Measure pings addr opts.Count times with the system's ping command.
func Measure(ctx context.Context, addr string, opts Options) Result {
	opts = opts.withDefaults()
	wait := strconv.Itoa(int((opts.Timeout + time.Second - 1) / time.Second))
	out, _ := exec.CommandContext(ctx, "ping", "-n", "-c", strconv.Itoa(opts.Count), "-i", "0.2", "-W", wait, addr).CombinedOutput()

	r := Result{Addr: addr, Sent: opts.Count}
	var total time.Duration
	for _, line := range strings.Split(string(out), "\n") {
		if m := replyLine.FindStringSubmatch(line); m != nil {
			ms, _ := strconv.ParseFloat(m[1], 64)
			total += time.Duration(ms * float64(time.Millisecond))
			r.Received++
		} else if errorLine.MatchString(line) {
			r.Errors++
		}
	}
	if r.Received > 0 {
		r.Avg = total / time.Duration(r.Received)
	}
	return r
}

// ErrNoServer is returned when no server answers well enough.
var ErrNoServer = errors.New("no server answers")

// Pick measures all servers at once and returns the fastest usable one: it
// must answer, and lose no more than opts.MaxLoss of the pings. Results come
// back for every server, fastest first.
func Pick(ctx context.Context, servers []string, opts Options) (string, []Result, error) {
	opts = opts.withDefaults()
	results := make([]Result, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Measure(ctx, s, opts)
		}()
	}
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool {
		ui, uj := usable(results[i], opts), usable(results[j], opts)
		if ui != uj {
			return ui
		}
		return results[i].Avg < results[j].Avg
	})
	if len(results) == 0 || !usable(results[0], opts) {
		return "", results, ErrNoServer
	}
	return results[0].Addr, results, nil
}

func usable(r Result, opts Options) bool {
	return r.Received > 0 && r.Loss() <= opts.MaxLoss
}

// Selector keeps using one server while it stays good, and picks another
// when it gets slower than MaxLatency or stops being usable.
type Selector struct {
	Servers    []string
	MaxLatency time.Duration
	Options    Options

	current string
}

// Current returns the server in use, or "" before the first Check.
func (s *Selector) Current() string { return s.current }

// Check measures the current server, picking a new one if needed. It reports
// whether the server changed.
func (s *Selector) Check(ctx context.Context) (changed bool, err error) {
	if s.current != "" {
		r := Measure(ctx, s.current, s.Options)
		if usable(r, s.Options.withDefaults()) && (s.MaxLatency == 0 || r.Avg <= s.MaxLatency) {
			return false, nil
		}
	}
	best, _, err := Pick(ctx, s.Servers, s.Options)
	if err != nil {
		return false, err
	}
	changed = best != s.current
	s.current = best
	return changed, nil
}

// Connect opens a TCP connection to port on the first of servers that
// accepts one, trying them in order, as from Pick, and waiting at most
// timeout for each. It returns the connection, the server, and why each
// server before it failed.
func Connect(ctx context.Context, servers []string, port string, timeout time.Duration) (net.Conn, string, []error, error) {
	var failed []error
	for _, s := range servers {
		d := net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s, port))
		if err == nil {
			return conn, s, failed, nil
		}
		failed = append(failed, err)
	}
	return nil, "", failed, ErrNoServer
}

func (r Result) String() string {
	if r.Received == 0 {
		return fmt.Sprintf("%-15s no reply (%d errors)", r.Addr, r.Errors)
	}
	return fmt.Sprintf("%-15s %6.1fms  loss %3.0f%%", r.Addr, float64(r.Avg)/float64(time.Millisecond), r.Loss()*100)
}
