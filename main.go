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
	"math/rand/v2"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/nanmuyao/pingmock/internal/api"
	"github.com/nanmuyao/pingmock/internal/config"
	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/responder"
	"github.com/nanmuyao/pingmock/internal/rule"
	"github.com/nanmuyao/pingmock/internal/tun"
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

// parseRuleFlag splits "TARGET=RULE" at the first '=' that follows the
// target, since rules themselves contain '=' (delay=50ms).
func parseRuleFlag(s string) (netip.Prefix, rule.Scenario, error) {
	target, text, ok := strings.Cut(s, "=")
	if !ok {
		return netip.Prefix{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: want TARGET=RULE", s)
	}
	p, err := rule.ParsePrefix(target)
	if err != nil {
		return netip.Prefix{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: %w", s, err)
	}
	sc, err := rule.ParseScenario(text)
	if err != nil {
		return netip.Prefix{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: %w", s, err)
	}
	return p, sc, nil
}

func main() {
	var (
		devName = flag.String("dev", "pingmock0", "TUN device name")
		addr    = flag.String("addr", "10.99.0.254/24", "address/prefix of the TUN device; other addresses in this subnet are mocked hosts")
		defRule = flag.String("default", "0ms", "rule for targets without a matching -rule")
		apiAddr = flag.String("api", "127.0.0.1:9100", "listen address of the HTTP control API (empty disables it)")
		seed    = flag.Uint64("seed", 0, "seed for loss and jitter; the same seed reproduces the same sequence per target (default: random, printed at startup)")
		verbose = flag.Bool("v", false, "log every request")
		showVer = flag.Bool("version", false, "print the version and exit")
		cfgPath = flag.String("config", "", "JSON configuration file; flags given on the command line override it")
		histLen = flag.Int("history", 1000, "number of recent requests kept for GET /history (0 disables)")
		rules   ruleFlags
	)
	flag.Var(&rules, "rule", "TARGET=RULE, repeatable. TARGET is an IPv4 address or CIDR. RULE is e.g. 120ms, delay=120ms,jitter=10ms,loss=5%, pattern=..x, timeout, or phases such as \"50ms for 3 pings; timeout\"")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sudo pingmock [flags]\n\nExamples:\n  sudo pingmock -rule 10.99.0.1=50ms -rule 10.99.0.2=delay=300ms,jitter=50ms -rule '10.99.0.3=20ms for 5 pings; timeout'\n  sudo pingmock -config pingmock.json\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("pingmock", buildVersion())
		return
	}
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// Configuration file first; explicitly set flags override it.
	var cfgDefault *rule.Scenario
	var cfgSeed *uint64
	ruleIndex := map[netip.Prefix]int{}
	type pending struct {
		p  netip.Prefix
		sc rule.Scenario
	}
	var initial []pending
	addRule := func(p netip.Prefix, sc rule.Scenario) {
		if i, ok := ruleIndex[p]; ok {
			initial[i].sc = sc
			return
		}
		ruleIndex[p] = len(initial)
		initial = append(initial, pending{p, sc})
	}
	if *cfgPath != "" {
		cfg, cfgRules, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		if cfg.Dev != "" && !set["dev"] {
			*devName = cfg.Dev
		}
		if cfg.Addr != "" && !set["addr"] {
			*addr = cfg.Addr
		}
		if cfg.API != nil && !set["api"] {
			*apiAddr = *cfg.API
		}
		if cfg.History != nil && !set["history"] {
			*histLen = *cfg.History
		}
		cfgDefault, cfgSeed = cfg.Default, cfg.Seed
		for _, r := range cfgRules {
			addRule(r.Prefix, r.Scenario)
		}
	}

	if *histLen < 0 {
		log.Fatalf("invalid -history %d: must not be negative", *histLen)
	}
	devPrefix, err := netip.ParsePrefix(*addr)
	if err != nil || !devPrefix.Addr().Is4() {
		log.Fatalf("invalid -addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", *addr)
	}
	var def rule.Scenario
	if cfgDefault != nil && !set["default"] {
		def = *cfgDefault
	} else if def, err = rule.ParseScenario(*defRule); err != nil {
		log.Fatalf("invalid -default: %v", err)
	}
	for _, r := range rules {
		p, sc, err := parseRuleFlag(r)
		if err != nil {
			log.Fatal(err)
		}
		addRule(p, sc)
	}
	switch {
	case set["seed"]:
	case cfgSeed != nil:
		*seed = *cfgSeed
	default:
		*seed = rand.Uint64()
	}

	dev, ifName, err := tun.Open(*devName)
	if err != nil {
		log.Fatal(err)
	}
	defer dev.Close()
	if err := tun.IP("addr", "add", devPrefix.String(), "dev", ifName); err != nil {
		log.Fatal(err)
	}
	if err := tun.IP("link", "set", ifName, "up"); err != nil {
		log.Fatal(err)
	}
	router := tun.NewRouter(ifName, devPrefix)
	rs := engine.NewRuleSet(def, *seed)
	hist := engine.NewHistory(*histLen)
	for _, r := range initial {
		if err := router.Ensure(r.p); err != nil {
			log.Fatal(err)
		}
		rs.Set(r.p, r.sc)
	}
	log.Printf("pingmock %s: device %s up with %s; ping any other address in %s (not %s itself, that one is local)",
		buildVersion(), ifName, devPrefix, devPrefix.Masked(), devPrefix.Addr())
	log.Printf("seed %d (pass -seed %d to reproduce this run)", *seed, *seed)
	log.Printf("default: %s", def)
	for _, r := range initial {
		log.Printf("rule %s: %s", r.p, r.sc)
	}

	var srv *http.Server
	if *apiAddr != "" {
		srv = &http.Server{Addr: *apiAddr, Handler: api.NewHandler(rs, hist, router.Ensure), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("API: %v", err)
			}
		}()
		log.Printf("control API on http://%s (GET /rules, PUT /rules/{target}, PUT /default, GET /history)", *apiAddr)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("shutting down, removing %s", ifName)
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			srv.Shutdown(ctx)
			cancel()
		}
		dev.Close()
	}()

	r := &responder.Responder{Rules: rs, History: hist, Verbose: *verbose}
	if err := r.Serve(dev); err != nil {
		log.Fatalf("read: %v", err)
	}
}
