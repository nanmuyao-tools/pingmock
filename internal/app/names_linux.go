package app

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/nanmuyao/pingmock/internal/config"
	"github.com/nanmuyao/pingmock/internal/dns"
	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// dnsRule returns the DNS server's Rule: the decision of rs, recorded in
// hist and, if verbose, in the log.
// A query's event has the query's ID and no addresses: they would all be
// the command's own DNS server, 127.0.0.1, and the history filters events
// by address.
func dnsRule(rs *engine.RuleSet, hist *engine.History, verbose bool) func(uint16, string, uint16) (rule.Outcome, time.Duration, bool) {
	return func(id uint16, name string, qtype uint16) (rule.Outcome, time.Duration, bool) {
		d, ok := rs.DecideDNS(name, qtype)
		if !ok {
			return 0, 0, false
		}
		ev := engine.Event{
			Time: time.Now(), ID: id, Result: d.Outcome.String(),
			Rule: d.Rule, Phase: d.Phase, Probe: "dns", Name: dns.Canonical(name), QType: engine.QType(qtype),
		}
		if d.Outcome == rule.Reply {
			ev.Result = "answer"
		}
		if d.Outcome != rule.Drop {
			ev.Delay = d.Delay.String()
		}
		hist.Add(ev)
		if verbose {
			what := ev.Result
			if ev.Delay != "" {
				what += " in " + ev.Delay
			}
			log.Printf("dns %s %s: %s [rule %s, phase %d]", ev.Name, ev.QType, what, d.Rule, d.Phase)
		}
		return d.Outcome, d.Delay, true
	}
}

// dnsAddr is where the command's DNS server listens, inside its network.
const dnsAddr = "127.0.0.1:53"

// relayFD is the file descriptor on which the child reaches the parent's
// DNS relay: the first extra file the parent passes.
const relayFD = 3

// setupNames gives the command the files that make the names resolve:
// its own /etc/hosts, a resolv.conf pointing at pingmock's DNS server,
// and, if needed, an nsswitch.conf that does not bypass it; nscd, which
// would bypass both, is hidden. It runs in the child's mount namespace.
func setupNames(hosts []config.Host, dnsNames []string) error {
	if err := privateMounts(); err != nil {
		return err
	}
	if err := mountHosts(hosts, dnsNames); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(dnsAddr)
	if err := mountResolvConf(host); err != nil {
		return err
	}
	if err := mountNsswitch(); err != nil {
		return err
	}
	return hideNscd()
}

// serveDNS starts the command's DNS server: it answers for the mocked
// names and addresses and forwards everything else to the parent, which
// asks the host's resolver, as the DNS rules in rs say; queries for names
// with a DNS rule are recorded in hist. It returns once the server
// listens.
func serveDNS(hosts []config.Host, rs *engine.RuleSet, hist *engine.History, verbose bool) error {
	table := dns.NewTable()
	for _, h := range hosts {
		table.Add(h.Name, h.Addr)
	}
	relay := os.NewFile(relayFD, "dns-relay")
	if relay == nil {
		return fmt.Errorf("DNS: the relay to the host's resolver is missing")
	}
	conn, err := net.FileConn(relay)
	relay.Close()
	if err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	pc, err := net.ListenPacket("udp", dnsAddr)
	if err != nil {
		return fmt.Errorf("DNS: listening on %s: %w", dnsAddr, err)
	}
	l, err := net.Listen("tcp", dnsAddr)
	if err != nil {
		return fmt.Errorf("DNS: listening on %s over TCP: %w", dnsAddr, err)
	}
	srv := &dns.Server{Table: table, Forward: dns.StreamForwarder(conn), Rule: dnsRule(rs, hist, verbose)}
	go srv.Serve(pc)
	go srv.ServeTCP(l)
	log.Printf("DNS on %s for the command: %d mocked names, other names go to the host's resolver", dnsAddr, len(table.Names()))
	return nil
}
