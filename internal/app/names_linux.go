package app

import (
	"fmt"
	"log"
	"net"
	"os"

	"github.com/nanmuyao/pingmock/internal/config"
	"github.com/nanmuyao/pingmock/internal/dns"
)

// dnsAddr is where the command's DNS server listens, inside its network.
const dnsAddr = "127.0.0.1:53"

// relayFD is the file descriptor on which the child reaches the parent's
// DNS relay: the first extra file the parent passes.
const relayFD = 3

// setupNames gives the command the files that make the names resolve:
// its own /etc/hosts, a resolv.conf pointing at pingmock's DNS server,
// and, if needed, an nsswitch.conf that does not bypass it; nscd, which
// would bypass both, is hidden. It runs in the child's mount namespace.
func setupNames(hosts []config.Host) error {
	if err := privateMounts(); err != nil {
		return err
	}
	if err := mountHosts(hosts); err != nil {
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
// asks the host's resolver. It returns once the server listens.
func serveDNS(hosts []config.Host) error {
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
	srv := &dns.Server{Table: table, Forward: dns.StreamForwarder(conn)}
	go srv.Serve(pc)
	go srv.ServeTCP(l)
	log.Printf("DNS on %s for the command: %d mocked names, other names go to the host's resolver", dnsAddr, len(table.Names()))
	return nil
}
