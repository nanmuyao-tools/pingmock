package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/nanmuyao/pingmock/internal/api"
	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/responder"
	"github.com/nanmuyao/pingmock/internal/rule"
	"github.com/nanmuyao/pingmock/internal/tun"
)

// instance is a running pingmock: the TUN device, the responder answering
// on it and the control API.
type instance struct {
	dev    *os.File
	ifName string
	srv    *http.Server
	apiURL string // empty when the API is disabled
	served chan error

	rules   *engine.RuleSet
	history *engine.History
}

// start sets up the device, installs the rules and starts answering. When it
// returns without error, pings are answered and the API accepts requests.
func start(s *settings) (*instance, error) {
	dev, ifName, err := tun.Open(s.dev)
	if err != nil {
		return nil, err
	}
	in := &instance{dev: dev, ifName: ifName, served: make(chan error, 1)}
	addr6, err := configureDevice(ifName, s)
	if err != nil {
		dev.Close()
		return nil, err
	}
	subnets := []netip.Prefix{s.addr}
	if addr6.IsValid() {
		subnets = append(subnets, addr6)
	}
	router := tun.NewRouter(ifName, subnets...)

	rs := engine.NewRuleSet(s.def, s.seed)
	for _, r := range s.rules {
		if err := router.Ensure(r.prefix); err != nil {
			dev.Close()
			return nil, err
		}
		rs.Set(r.prefix, r.sc)
	}
	for _, p := range s.paths {
		// The target and every hop need a route to the device: hops
		// answer from their own addresses, which the kernel accepts only
		// if they are reachable through the device the answer came in on.
		if err := router.Ensure(netip.PrefixFrom(p.addr, p.addr.BitLen())); err != nil {
			dev.Close()
			return nil, err
		}
		for _, h := range p.path.Addrs() {
			if err := router.Ensure(netip.PrefixFrom(h, h.BitLen())); err != nil {
				dev.Close()
				return nil, err
			}
		}
		rs.SetPath(p.addr, p.path)
	}
	for _, r := range s.tcp {
		if err := router.Ensure(r.prefix); err != nil {
			dev.Close()
			return nil, err
		}
		rs.SetTCP(r.prefix, r.port, r.sc)
	}
	for _, r := range s.dns {
		rs.SetDNS(r.Name, r.Scenario)
	}
	rs.SaveBaseline() // what POST /reset goes back to
	hist := engine.NewHistory(s.history)
	in.rules, in.history = rs, hist

	if s.api != "" {
		h := api.NewHandler(rs, hist, router.Ensure, buildVersion(), s.dnsTarget)
		if err := in.startAPI(s.api, h); err != nil {
			dev.Close()
			return nil, err
		}
	}
	logStartup(s, ifName, addr6, in.apiURL)

	r := &responder.Responder{Rules: rs, History: hist, Verbose: s.verbose}
	go func() { in.served <- r.Serve(dev) }()
	return in, nil
}

// configureDevice gives the device its addresses and brings it up. It
// returns the IPv6 prefix in use, which is invalid if IPv6 is off or, when
// it was not asked for explicitly, unavailable.
func configureDevice(ifName string, s *settings) (netip.Prefix, error) {
	if err := tun.IP("addr", "add", s.addr.String(), "dev", ifName); err != nil {
		return netip.Prefix{}, err
	}
	if err := tun.IP("link", "set", ifName, "up"); err != nil {
		return netip.Prefix{}, err
	}
	if !s.addr6.IsValid() {
		return netip.Prefix{}, nil
	}
	// nodad: the address is usable at once instead of after duplicate
	// address detection, which is pointless on a private TUN device.
	if err := tun.IP("-6", "addr", "add", s.addr6.String(), "dev", ifName, "nodad"); err != nil {
		if s.addr6Explicit {
			return netip.Prefix{}, fmt.Errorf("setting up IPv6: %w", err)
		}
		log.Printf("IPv6 is unavailable, continuing with IPv4 only (%v)", err)
		return netip.Prefix{}, nil
	}
	return s.addr6, nil
}

// startAPI serves the control API on addr. It listens before returning, so
// the API is usable as soon as start is.
func (in *instance) startAPI(addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("API: %w", err)
	}
	in.srv = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	in.apiURL = "http://" + ln.Addr().String()
	go func() {
		if err := in.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("API: %v", err)
		}
	}()
	return nil
}

func logStartup(s *settings, ifName string, addr6 netip.Prefix, apiURL string) {
	log.Printf("pingmock %s: device %s up with %s; ping any other address in %s (not %s itself, that one is local)",
		buildVersion(), ifName, s.addr, s.addr.Masked(), s.addr.Addr())
	if addr6.IsValid() {
		log.Printf("IPv6: %s; ping any other address in %s", addr6, addr6.Masked())
	}
	log.Printf("seed %d (pass -seed %d to reproduce this run)", s.seed, s.seed)
	log.Printf("default: %s", s.def)
	for _, r := range s.rules {
		if r.name != "" {
			log.Printf("rule %s (%s): %s", r.name, r.prefix.Addr(), r.sc)
		} else {
			log.Printf("rule %s: %s", r.prefix, r.sc)
		}
	}
	for _, p := range s.paths {
		if p.name != "" {
			log.Printf("path to %s (%s): %s", p.name, p.addr, p.path)
		} else {
			log.Printf("path to %s: %s", p.addr, p.path)
		}
	}
	for _, r := range s.tcp {
		t := rule.TCPTarget{Prefix: r.prefix, Port: r.port}
		if r.name != "" {
			log.Printf("tcp %s (%s): %s", rule.TCPTarget{Name: r.name, Port: r.port}, t, rule.TCPScenario{Scenario: r.sc})
		} else {
			log.Printf("tcp %s: %s", t, rule.TCPScenario{Scenario: r.sc})
		}
	}
	for _, h := range s.hosts {
		log.Printf("name %s resolves to %s (for the command only)", h.Name, h.Addr)
	}
	for _, r := range s.dns {
		log.Printf("dns %s: %s", r.Name, rule.DNSScenario{Scenario: r.Scenario})
	}
	if apiURL != "" {
		dnsAPI := ""
		if s.exec && s.names() {
			dnsAPI = ", PUT /dns/{name}"
		}
		log.Printf("control API on %s (GET /rules, PUT /rules/{target}, PUT /tcp/{target}%s, PUT /default, GET /history, POST /reset)", apiURL, dnsAPI)
	}
}

// Done is closed with an error if answering stops on its own.
func (in *instance) Done() <-chan error { return in.served }

// Close stops the API and removes the device with its addresses and routes.
func (in *instance) Close() {
	if in.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		in.srv.Shutdown(ctx)
		cancel()
	}
	in.dev.Close()
}
