package app

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

// parseSettings parses the flags shared by the server and exec modes, and
// the configuration file they may name. It returns the arguments left after
// the flags (the command, for exec).
//
// Precedence: a flag given on the command line, then the configuration
// file, then the flag's default.
func parseSettings(name string, args []string, usage func(io.Writer, *flag.FlagSet), out io.Writer) (*settings, []string, error) {
	f, fs := defineFlags(name, usage, out)
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	fs.Visit(func(fl *flag.Flag) { f.given[fl.Name] = true })
	if *f.config != "" {
		if err := f.applyConfig(*f.config); err != nil {
			return nil, nil, err
		}
	}
	s, err := f.resolve()
	if err != nil {
		return nil, nil, err
	}
	return s, fs.Args(), nil
}

// flagValues are the raw values of the flags, before validation. Values
// from the configuration file are written into them, except where the flag
// was given on the command line.
type flagValues struct {
	dev, addr, addr6, defRule, api, config *string
	seed                                   *uint64
	history                                *int
	verbose                                *bool
	rules                                  ruleFlags

	given map[string]bool // flags given on the command line

	// From the configuration file.
	addr6FromConfig bool
	cfgDefault      *rule.Scenario
	cfgSeed         *uint64
	cfgRules        []config.Rule
}

func defineFlags(name string, usage func(io.Writer, *flag.FlagSet), out io.Writer) (*flagValues, *flag.FlagSet) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	f := &flagValues{given: map[string]bool{}}
	f.dev = fs.String("dev", "pingmock0", "TUN device name")
	f.addr = fs.String("addr", "10.99.0.254/24", "IPv4 address/prefix of the TUN device; other addresses in this subnet are mocked hosts")
	f.addr6 = fs.String("addr6", "fd99::fe/64", "IPv6 address/prefix of the TUN device (empty disables IPv6); if IPv6 is unavailable and this flag is not given, pingmock continues with IPv4 only")
	f.defRule = fs.String("default", "0ms", "rule for targets without a matching -rule")
	f.api = fs.String("api", "127.0.0.1:9100", "listen address of the HTTP control API (empty disables it)")
	f.seed = fs.Uint64("seed", 0, "seed for loss and jitter; the same seed reproduces the same sequence per target (default: random, printed at startup)")
	f.verbose = fs.Bool("v", false, "log every request")
	f.config = fs.String("config", "", "YAML configuration file; flags given on the command line override it")
	f.history = fs.Int("history", 1000, "number of recent requests kept for GET /history (0 disables)")
	fs.Var(&f.rules, "rule", "TARGET=RULE, repeatable. TARGET is an IPv4 or IPv6 address or CIDR. RULE is e.g. 120ms, delay=120ms,jitter=10ms,loss=5%, pattern=..x, unreachable, timeout, or phases such as \"50ms for 3 pings; timeout\"")
	fs.Usage = func() { usage(out, fs) }
	return f, fs
}

// applyConfig loads the configuration file into the values of the flags
// that were not given on the command line.
func (f *flagValues) applyConfig(path string) error {
	cfg, rules, err := config.Load(path)
	if err != nil {
		return err
	}
	if cfg.Dev != "" && !f.given["dev"] {
		*f.dev = cfg.Dev
	}
	if cfg.Addr != "" && !f.given["addr"] {
		*f.addr = cfg.Addr
	}
	if cfg.Addr6 != nil && !f.given["addr6"] {
		*f.addr6 = *cfg.Addr6
		f.addr6FromConfig = true
	}
	if cfg.API != nil && !f.given["api"] {
		*f.api = *cfg.API
	}
	if cfg.History != nil && !f.given["history"] {
		*f.history = *cfg.History
	}
	f.cfgDefault, f.cfgSeed, f.cfgRules = cfg.Default, cfg.Seed, rules
	return nil
}

// resolve validates the values and turns them into settings. Rules from the
// configuration file come first; a -rule for the same target replaces one.
func (f *flagValues) resolve() (*settings, error) {
	s := &settings{
		dev:           *f.dev,
		api:           *f.api,
		history:       *f.history,
		verbose:       *f.verbose,
		addr6Explicit: f.given["addr6"] || f.addr6FromConfig,
	}
	if s.history < 0 {
		return nil, fmt.Errorf("invalid -history %d: must not be negative", s.history)
	}
	var err error
	s.addr, err = netip.ParsePrefix(*f.addr)
	if err != nil || !s.addr.Addr().Is4() {
		return nil, fmt.Errorf("invalid -addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", *f.addr)
	}
	if *f.addr6 != "" {
		s.addr6, err = netip.ParsePrefix(*f.addr6)
		if err != nil || !s.addr6.Addr().Is6() || s.addr6.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid -addr6 %q: need an IPv6 address with prefix, e.g. fd99::fe/64", *f.addr6)
		}
	}

	if f.cfgDefault != nil && !f.given["default"] {
		s.def = *f.cfgDefault
	} else if s.def, err = rule.ParseScenario(*f.defRule); err != nil {
		return nil, fmt.Errorf("invalid -default: %w", err)
	}

	for _, r := range f.cfgRules {
		s.addRule(r.Prefix, r.Scenario)
	}
	for _, text := range f.rules {
		p, sc, err := parseRuleFlag(text)
		if err != nil {
			return nil, err
		}
		s.addRule(p, sc)
	}

	switch {
	case f.given["seed"]:
		s.seed = *f.seed
	case f.cfgSeed != nil:
		s.seed = *f.cfgSeed
	default:
		s.seed = rand.Uint64()
	}
	return s, nil
}

// addRule adds a rule, or replaces the one for the same target.
func (s *settings) addRule(p netip.Prefix, sc rule.Scenario) {
	for i := range s.rules {
		if s.rules[i].prefix == p {
			s.rules[i].sc = sc
			return
		}
	}
	s.rules = append(s.rules, targetRule{p, sc})
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
