package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/netip"
	"sort"
	"strings"

	"github.com/nanmuyao/pingmock/internal/config"
	"github.com/nanmuyao/pingmock/internal/rule"
)

type ruleFlags []string

func (f *ruleFlags) String() string     { return strings.Join(*f, " ") }
func (f *ruleFlags) Set(v string) error { *f = append(*f, v); return nil }

// targetRule is a rule to install at startup. A rule given for a name gets
// its address allocated when the settings are resolved.
type targetRule struct {
	prefix netip.Prefix
	name   string // set when the rule was given for a name
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
	hosts         []config.Host // names the command resolves; exec only
	paths         []targetPath
	tcp           []targetTCP
	dns           []config.DNSRule // exec only
	exec          bool             // running under pingmock exec
}

// targetTCP is a TCP rule to install at startup; a rule given for a name
// gets the name's address.
type targetTCP struct {
	prefix netip.Prefix
	name   string
	port   uint16
	sc     rule.Scenario
}

// targetPath is a path to install at startup, for one target address; a
// path given for a name gets its address with the name's rule.
type targetPath struct {
	addr netip.Addr
	name string
	path rule.Path
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
	hosts                                  ruleFlags
	paths                                  ruleFlags
	tcp                                    ruleFlags
	dns                                    ruleFlags

	given map[string]bool // flags given on the command line

	// From the configuration file.
	addr6FromConfig bool
	cfgDefault      *rule.Scenario
	cfgSeed         *uint64
	cfgRules        []config.Rule
	cfgHosts        []config.Host
	cfgPaths        []config.PathRule
	cfgTCP          []config.TCPRule
	cfgDNS          []config.DNSRule
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
	fs.Var(&f.rules, "rule", "TARGET=RULE, repeatable. TARGET is an IPv4 or IPv6 address or CIDR, or under pingmock exec a name. RULE is e.g. 120ms, delay=120ms,jitter=10ms,loss=5%, pattern=..x, unreachable, timeout, ttl=128, or phases such as \"50ms for 3 pings; timeout\"")
	fs.Var(&f.hosts, "host", "NAME=ADDR, repeatable, pingmock exec only: CMD resolves NAME to ADDR through a private /etc/hosts, e.g. api.example.com=10.99.0.1")
	fs.Var(&f.paths, "path", "TARGET=PATH, repeatable: the route traceroute and mtr see to TARGET, hops separated by \">\", each ADDR@SETTINGS (delay, jitter, loss, cut, ttl) or * for one that never answers, e.g. '8.8.8.8=192.168.1.1@2ms > * > 72.14.0.1@25ms'")
	fs.Var(&f.tcp, "tcp", "TARGET=RULE, repeatable: how TCP connections to TARGET are answered. TARGET is ADDR[:PORT] or PREFIX[:PORT], IPv6 in brackets ([fd99::1]:443), or under pingmock exec NAME[:PORT]; no port means every port. RULE is as for -rule, plus refuse (answer with a reset), r in a pattern and after=close|reset|hang (what happens once a connection is accepted), e.g. 10.99.0.1:5432=timeout or '10.99.0.2:443=refuse for 3 requests; 20ms'")
	fs.Var(&f.dns, "dns", "NAME=RULE, repeatable, pingmock exec only: how CMD's DNS queries for NAME are answered. RULE is as for -rule, without unreachable and ttl, plus nxdomain (the name does not exist), servfail (the server failed) and n and s in a pattern; other settings apply to the usual answer, e.g. api.example.com=nxdomain or 'db.example.com=servfail for 2 requests; 10ms'")
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
	if f.cfgHosts, err = cfg.SortedHosts(path); err != nil {
		return err
	}
	if f.cfgPaths, err = cfg.SortedPaths(path); err != nil {
		return err
	}
	if f.cfgTCP, err = cfg.SortedTCP(path); err != nil {
		return err
	}
	f.cfgDNS, err = cfg.SortedDNS(path)
	return err
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
		s.addRule(r.Target, r.Scenario)
	}
	for _, text := range f.rules {
		t, sc, err := parseRuleFlag(text)
		if err != nil {
			return nil, err
		}
		s.addRule(t, sc)
	}

	for _, h := range f.cfgHosts {
		s.addHost(h)
	}
	for _, text := range f.hosts {
		name, addr, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("invalid -host %q: want NAME=ADDR", text)
		}
		h, err := config.ParseHost(name, addr)
		if err != nil {
			return nil, fmt.Errorf("invalid -host %q: %w", text, err)
		}
		s.addHost(h)
	}
	for _, p := range f.cfgPaths {
		s.addPath(p.Target, p.Path)
	}
	for _, text := range f.paths {
		target, pathText, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("invalid -path %q: want TARGET=PATH", text)
		}
		t, err := rule.ParseTarget(target)
		if err != nil || (t.Name == "" && !t.Prefix.IsSingleIP()) {
			return nil, fmt.Errorf("invalid -path %q: want a single address or a name as the target", text)
		}
		p, err := rule.ParsePath(pathText)
		if err != nil {
			return nil, fmt.Errorf("invalid -path %q: %w", text, err)
		}
		s.addPath(t, p)
	}
	for _, r := range f.cfgTCP {
		s.addTCP(r.Target, r.Scenario)
	}
	for _, text := range f.tcp {
		target, ruleText, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("invalid -tcp %q: want TARGET=RULE", text)
		}
		t, err := rule.ParseTCPTarget(target)
		if err != nil {
			return nil, fmt.Errorf("invalid -tcp %q: %w", text, err)
		}
		sc, err := rule.ParseTCPScenario(ruleText)
		if err != nil {
			return nil, fmt.Errorf("invalid -tcp %q: %w", text, err)
		}
		s.addTCP(t, sc.Scenario)
	}
	for _, r := range f.cfgDNS {
		s.addDNS(r)
	}
	for _, text := range f.dns {
		name, ruleText, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("invalid -dns %q: want NAME=RULE", text)
		}
		n, err := config.ParseDNSName(name)
		if err != nil {
			return nil, fmt.Errorf("invalid -dns %q: %w", text, err)
		}
		sc, err := rule.ParseDNSScenario(ruleText)
		if err != nil {
			return nil, fmt.Errorf("invalid -dns %q: %w", text, err)
		}
		s.addDNS(config.DNSRule{Name: n, Scenario: sc.Scenario})
	}
	if err := s.allocateNames(); err != nil {
		return nil, err
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

// addHost adds a host, or replaces the address of the same name.
func (s *settings) addHost(h config.Host) {
	for i := range s.hosts {
		if s.hosts[i].Name == h.Name {
			s.hosts[i].Addr = h.Addr
			return
		}
	}
	s.hosts = append(s.hosts, h)
}

// addRule adds a rule, or replaces the one for the same target.
func (s *settings) addRule(t rule.Target, sc rule.Scenario) {
	for i := range s.rules {
		if s.rules[i].name == t.Name && (t.Name != "" || s.rules[i].prefix == t.Prefix) {
			s.rules[i].sc = sc
			return
		}
	}
	s.rules = append(s.rules, targetRule{prefix: t.Prefix, name: t.Name, sc: sc})
}

// allocateNames gives every rule written for a name an address: one given
// with -host for that name if there is one, else a free address from the
// top of the IPv4 subnet down, in name order so that it is the same on
// every run. The command resolves the name to it.
func (s *settings) allocateNames() error {
	byName := map[string]netip.Addr{}
	for _, h := range s.hosts {
		if _, taken := byName[h.Name]; !taken || h.Addr.Is4() {
			byName[h.Name] = h.Addr // prefer the IPv4 address, which every rule can use
		}
	}
	taken := map[netip.Addr]bool{}
	for _, h := range s.hosts {
		taken[h.Addr] = true
	}
	for _, r := range s.rules {
		if r.name == "" && r.prefix.IsSingleIP() {
			taken[r.prefix.Addr()] = true
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, r := range s.rules {
		if r.name != "" && !seen[r.name] {
			seen[r.name] = true
			names = append(names, r.name)
		}
	}
	for _, p := range s.paths {
		if p.name != "" && !seen[p.name] {
			seen[p.name] = true
			names = append(names, p.name)
		}
	}
	for _, r := range s.tcp {
		if r.name != "" && !seen[r.name] {
			seen[r.name] = true
			names = append(names, r.name)
		}
	}
	sort.Strings(names)
	next := s.addr.Masked().Addr() // the subnet's first address; counted from the top below
	top := lastAddr(s.addr)
	for _, name := range names {
		a, ok := byName[name]
		if !ok {
			a = top
			for taken[a] || a == s.addr.Addr() || a == s.addr.Masked().Addr() || a == lastAddr(s.addr) {
				a = a.Prev()
				if !a.IsValid() || a.Less(next) {
					return fmt.Errorf("no free address left in %s for the rule %s", s.addr.Masked(), name)
				}
			}
			byName[name] = a
			s.hosts = append(s.hosts, config.Host{Name: name, Addr: a})
		}
		taken[a] = true
		for i := range s.rules {
			if s.rules[i].name == name {
				s.rules[i].prefix = netip.PrefixFrom(a, a.BitLen())
			}
		}
		for i := range s.paths {
			if s.paths[i].name == name {
				s.paths[i].addr = a
			}
		}
		for i := range s.tcp {
			if s.tcp[i].name == name {
				s.tcp[i].prefix = netip.PrefixFrom(a, a.BitLen())
			}
		}
	}
	return nil
}

// addPath adds a path, or replaces the one for the same target.
func (s *settings) addPath(t rule.Target, p rule.Path) {
	addr := t.Prefix.Addr()
	for i := range s.paths {
		if s.paths[i].name == t.Name && (t.Name != "" || s.paths[i].addr == addr) {
			s.paths[i].path = p
			return
		}
	}
	s.paths = append(s.paths, targetPath{addr: addr, name: t.Name, path: p})
}

// addTCP adds a TCP rule, or replaces the one for the same target and port.
func (s *settings) addTCP(t rule.TCPTarget, sc rule.Scenario) {
	for i := range s.tcp {
		r := &s.tcp[i]
		if r.name == t.Name && r.port == t.Port && (t.Name != "" || r.prefix == t.Prefix) {
			r.sc = sc
			return
		}
	}
	s.tcp = append(s.tcp, targetTCP{prefix: t.Prefix, name: t.Name, port: t.Port, sc: sc})
}

// addDNS adds a DNS rule, or replaces the one for the same name.
func (s *settings) addDNS(r config.DNSRule) {
	for i := range s.dns {
		if s.dns[i].Name == r.Name {
			s.dns[i].Scenario = r.Scenario
			return
		}
	}
	s.dns = append(s.dns, r)
}

// hostsFile returns the names that go in the command's /etc/hosts: every
// mocked name, except those with a DNS rule, which the C library and Go
// would otherwise find there without asking DNS.
func (s *settings) hostsFile() []config.Host {
	var out []config.Host
	for _, h := range s.hosts {
		if !s.hasDNS(h.Name) {
			out = append(out, h)
		}
	}
	return out
}

// dnsTarget checks that a DNS rule for name, set through the API, can take
// effect, and returns the name as rules are keyed.
func (s *settings) dnsTarget(name string) (string, error) {
	n, err := config.ParseDNSName(name)
	switch {
	case err != nil:
		return "", err
	case !s.exec:
		return "", fmt.Errorf("DNS rules are for pingmock exec only: for the whole machine, pingmock cannot get between programs and the machine's resolver")
	case !s.names():
		return "", fmt.Errorf("this pingmock exec runs no DNS server for its command: start it with a -dns rule, a -host or a rule for a name (a rule that changes nothing will do: -dns %s=0ms)", n)
	}
	for _, h := range s.hostsFile() {
		if h.Name == n {
			return "", fmt.Errorf("%s is in the command's /etc/hosts, where programs find it without asking DNS: give it a DNS rule at startup (-dns %s=0ms) to change its answers later", n, n)
		}
	}
	return n, nil
}

// dnsNames returns the names with a DNS rule.
func (s *settings) dnsNames() []string {
	out := make([]string, len(s.dns))
	for i, r := range s.dns {
		out[i] = r.Name
	}
	return out
}

func (s *settings) hasDNS(name string) bool {
	for _, r := range s.dns {
		if r.Name == name {
			return true
		}
	}
	return false
}

// lastAddr is the last address of p, the broadcast address of an IPv4 subnet.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	bits := p.Bits()
	for i := bits; i < 32; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	return netip.AddrFrom4(b)
}

// parseRuleFlag splits "TARGET=RULE" at the first '=' that follows the
// target, since rules themselves contain '=' (delay=50ms).
func parseRuleFlag(s string) (rule.Target, rule.Scenario, error) {
	target, text, ok := strings.Cut(s, "=")
	if !ok {
		return rule.Target{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: want TARGET=RULE", s)
	}
	t, err := rule.ParseTarget(target)
	if err != nil {
		return rule.Target{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: %w", s, err)
	}
	sc, err := rule.ParseScenario(text)
	if err != nil {
		return rule.Target{}, rule.Scenario{}, fmt.Errorf("invalid -rule %q: %w", s, err)
	}
	return t, sc, nil
}

// names reports whether the command needs name resolution: hosts given,
// rules written for names, or DNS rules.
func (s *settings) names() bool { return len(s.hosts) > 0 || len(s.dns) > 0 }

func isHelp(err error) bool { return errors.Is(err, errHelp) }
