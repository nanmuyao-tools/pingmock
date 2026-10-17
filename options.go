package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/netip"
	"strings"

	"github.com/nanmuyao/pingmock/internal/config"
	"github.com/nanmuyao/pingmock/internal/rule"
)

type ruleFlags []string

func (f *ruleFlags) String() string     { return strings.Join(*f, " ") }
func (f *ruleFlags) Set(v string) error { *f = append(*f, v); return nil }

// targetRule is a rule to install at startup.
type targetRule struct {
	prefix netip.Prefix
	sc     rule.Scenario
}

// settings is everything needed to start an instance, after flags and the
// configuration file have been merged.
type settings struct {
	dev           string
	addr          netip.Prefix
	addr6         netip.Prefix // invalid when IPv6 is disabled
	addr6Explicit bool         // asked for explicitly: failing to set it up is an error
	api           string       // empty disables the API
	seed          uint64
	history       int
	verbose       bool
	def           rule.Scenario
	rules         []targetRule
}

// errHelp is returned when -h or -help was given; usage has been printed.
var errHelp = flag.ErrHelp

// parseSettings parses the flags shared by the server and exec modes. It
// returns the arguments left after the flags (the command, for exec).
func parseSettings(name string, args []string, usage func(io.Writer, *flag.FlagSet), out io.Writer) (*settings, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		devName = fs.String("dev", "pingmock0", "TUN device name")
		addr    = fs.String("addr", "10.99.0.254/24", "IPv4 address/prefix of the TUN device; other addresses in this subnet are mocked hosts")
		addr6   = fs.String("addr6", "fd99::fe/64", "IPv6 address/prefix of the TUN device (empty disables IPv6); if IPv6 is unavailable and this flag is not given, pingmock continues with IPv4 only")
		defRule = fs.String("default", "0ms", "rule for targets without a matching -rule")
		apiAddr = fs.String("api", "127.0.0.1:9100", "listen address of the HTTP control API (empty disables it)")
		seed    = fs.Uint64("seed", 0, "seed for loss and jitter; the same seed reproduces the same sequence per target (default: random, printed at startup)")
		verbose = fs.Bool("v", false, "log every request")
		cfgPath = fs.String("config", "", "YAML configuration file; flags given on the command line override it")
		histLen = fs.Int("history", 1000, "number of recent requests kept for GET /history (0 disables)")
		rules   ruleFlags
	)
	fs.Var(&rules, "rule", "TARGET=RULE, repeatable. TARGET is an IPv4 or IPv6 address or CIDR. RULE is e.g. 120ms, delay=120ms,jitter=10ms,loss=5%, pattern=..x, unreachable, timeout, or phases such as \"50ms for 3 pings; timeout\"")
	fs.Usage = func() { usage(out, fs) }
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	s := &settings{dev: *devName, api: *apiAddr, history: *histLen, verbose: *verbose, addr6Explicit: set["addr6"]}
	ruleIndex := map[netip.Prefix]int{}
	addRule := func(p netip.Prefix, sc rule.Scenario) {
		if i, ok := ruleIndex[p]; ok {
			s.rules[i].sc = sc
			return
		}
		ruleIndex[p] = len(s.rules)
		s.rules = append(s.rules, targetRule{p, sc})
	}

	// Configuration file first; explicitly set flags override it.
	var cfgDefault *rule.Scenario
	var cfgSeed *uint64
	if *cfgPath != "" {
		cfg, cfgRules, err := config.Load(*cfgPath)
		if err != nil {
			return nil, nil, err
		}
		if cfg.Dev != "" && !set["dev"] {
			s.dev = cfg.Dev
		}
		if cfg.Addr != "" && !set["addr"] {
			*addr = cfg.Addr
		}
		if cfg.Addr6 != nil && !set["addr6"] {
			*addr6 = *cfg.Addr6
			s.addr6Explicit = true
		}
		if cfg.API != nil && !set["api"] {
			s.api = *cfg.API
		}
		if cfg.History != nil && !set["history"] {
			s.history = *cfg.History
		}
		cfgDefault, cfgSeed = cfg.Default, cfg.Seed
		for _, r := range cfgRules {
			addRule(r.Prefix, r.Scenario)
		}
	}

	if s.history < 0 {
		return nil, nil, fmt.Errorf("invalid -history %d: must not be negative", s.history)
	}
	var err error
	s.addr, err = netip.ParsePrefix(*addr)
	if err != nil || !s.addr.Addr().Is4() {
		return nil, nil, fmt.Errorf("invalid -addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", *addr)
	}
	if *addr6 != "" {
		s.addr6, err = netip.ParsePrefix(*addr6)
		if err != nil || !s.addr6.Addr().Is6() || s.addr6.Addr().Is4In6() {
			return nil, nil, fmt.Errorf("invalid -addr6 %q: need an IPv6 address with prefix, e.g. fd99::fe/64", *addr6)
		}
	}
	if cfgDefault != nil && !set["default"] {
		s.def = *cfgDefault
	} else if s.def, err = rule.ParseScenario(*defRule); err != nil {
		return nil, nil, fmt.Errorf("invalid -default: %w", err)
	}
	for _, r := range rules {
		p, sc, err := parseRuleFlag(r)
		if err != nil {
			return nil, nil, err
		}
		addRule(p, sc)
	}
	switch {
	case set["seed"]:
		s.seed = *seed
	case cfgSeed != nil:
		s.seed = *cfgSeed
	default:
		s.seed = rand.Uint64()
	}
	return s, fs.Args(), nil
}

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

func isHelp(err error) bool { return errors.Is(err, errHelp) }
