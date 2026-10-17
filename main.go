// Command pingmock answers ICMP echo requests ("ping") with a configurable
// delay, jitter and loss, so software that measures latency with ping can be
// tested deterministically.
//
// It creates a TUN device, routes a test subnet (and optionally real
// addresses such as 8.8.8.8) to it, and replies to every echo request it
// receives according to the matching rule.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
// When empty, the module version recorded by "go install" is used.
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

type ruleFlags []string

func (f *ruleFlags) String() string     { return strings.Join(*f, " ") }
func (f *ruleFlags) Set(v string) error { *f = append(*f, v); return nil }

// Router adds routes for targets outside the device subnet, so that real
// addresses can be mocked too. Routes vanish with the device on exit.
type Router struct {
	mu     sync.Mutex
	dev    string
	subnet netip.Prefix
	routed map[netip.Prefix]bool
}

func (r *Router) Ensure(p netip.Prefix) error {
	if r.subnet.Contains(p.Addr()) && p.Bits() >= r.subnet.Bits() {
		return nil // already routed through the device subnet
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routed[p] {
		return nil
	}
	if err := ipCmd("route", "replace", p.String(), "dev", r.dev); err != nil {
		return err
	}
	r.routed[p] = true
	log.Printf("routing %s through %s (hijacked: real traffic to it is mocked until exit)", p, r.dev)
	return nil
}

func main() {
	var (
		devName = flag.String("dev", "pingmock0", "TUN device name")
		addr    = flag.String("addr", "10.99.0.254/24", "address/prefix of the TUN device; other addresses in this subnet are mocked hosts")
		defSpec = flag.String("default", "0ms", "spec for targets without a rule")
		apiAddr = flag.String("api", "127.0.0.1:9100", "listen address of the HTTP control API (empty disables it)")
		verbose = flag.Bool("v", false, "log every request")
		showVer = flag.Bool("version", false, "print the version and exit")
		rules   ruleFlags
	)
	flag.Var(&rules, "rule", "TARGET=SPEC, repeatable. TARGET is an IPv4 address or CIDR; SPEC is e.g. 120ms or delay=120ms,jitter=10ms,loss=5% or timeout")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sudo pingmock [flags]\n\nExample:\n  sudo pingmock -rule 10.99.0.1=50ms -rule 10.99.0.2=delay=300ms,jitter=50ms -rule 8.8.8.8=timeout\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("pingmock", buildVersion())
		return
	}

	devPrefix, err := netip.ParsePrefix(*addr)
	if err != nil || !devPrefix.Addr().Is4() {
		log.Fatalf("invalid -addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", *addr)
	}
	def, err := ParseSpec(*defSpec)
	if err != nil {
		log.Fatalf("invalid -default: %v", err)
	}
	rs := NewRuleSet(def)

	type pending struct {
		p  netip.Prefix
		sp Spec
	}
	var initial []pending
	for _, r := range rules {
		target, spec, ok := strings.Cut(r, "=")
		if !ok {
			log.Fatalf("invalid -rule %q: want TARGET=SPEC", r)
		}
		p, err := ParsePrefix(target)
		if err != nil {
			log.Fatalf("invalid -rule %q: %v", r, err)
		}
		sp, err := ParseSpec(spec)
		if err != nil {
			log.Fatalf("invalid -rule %q: %v", r, err)
		}
		initial = append(initial, pending{p, sp})
	}

	tun, dev, err := openTUN(*devName)
	if err != nil {
		log.Fatal(err)
	}
	defer tun.Close()
	if err := ipCmd("addr", "add", devPrefix.String(), "dev", dev); err != nil {
		log.Fatal(err)
	}
	if err := ipCmd("link", "set", dev, "up"); err != nil {
		log.Fatal(err)
	}
	router := &Router{dev: dev, subnet: devPrefix.Masked(), routed: map[netip.Prefix]bool{}}
	for _, r := range initial {
		if err := router.Ensure(r.p); err != nil {
			log.Fatal(err)
		}
		rs.Set(r.p, r.sp)
	}
	log.Printf("device %s up with %s; ping any other address in %s (not %s itself, that one is local)",
		dev, devPrefix, devPrefix.Masked(), devPrefix.Addr())
	log.Printf("default: %s", def)
	for _, r := range initial {
		log.Printf("rule %s: %s", r.p, r.sp)
	}

	var srv *http.Server
	if *apiAddr != "" {
		srv = &http.Server{Addr: *apiAddr, Handler: newAPI(rs, router), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("API: %v", err)
			}
		}()
		log.Printf("control API on http://%s (GET /rules, PUT /rules/{target}, PUT /default)", *apiAddr)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("shutting down, removing %s", dev)
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			srv.Shutdown(ctx)
			cancel()
		}
		tun.Close()
	}()

	serve(tun, rs, *verbose)
}

// serve reads packets from the device and schedules replies.
func serve(tun *os.File, rs *RuleSet, verbose bool) {
	var wmu sync.Mutex
	buf := make([]byte, 65536)
	for {
		n, err := tun.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				log.Printf("read: %v", err)
			}
			return
		}
		pkt := buf[:n]
		req, ok := parseEchoRequest(pkt)
		if !ok {
			continue
		}
		delay, reply := rs.Lookup(req.dst).sample()
		rs.record(req.dst, reply)
		if !reply {
			if verbose {
				log.Printf("%s -> %s id=%d seq=%d: dropped", req.src, req.dst, req.id, req.seq)
			}
			continue
		}
		out := buildEchoReply(pkt)
		if verbose {
			log.Printf("%s -> %s id=%d seq=%d: reply in %s", req.src, req.dst, req.id, req.seq, delay)
		}
		time.AfterFunc(delay, func() {
			wmu.Lock()
			defer wmu.Unlock()
			if _, err := tun.Write(out); err != nil && !errors.Is(err, os.ErrClosed) {
				log.Printf("write: %v", err)
			}
		})
	}
}
