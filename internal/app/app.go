// Package app is the ghostwire command: it parses the command line and runs
// ghostwire either in the current network (Main with flags) or around a
// command in a private network (Main with "exec").
//
// The packages it puts together are, in the order a ping meets them:
//
//	tun        the virtual network device the pings and connections arrive on
//	responder  reads each packet and answers it
//	icmp       parses pings, UDP and TCP probes and builds the answers
//	engine     decides, per target, how to answer and keeps statistics
//	rule       what a rule is and how it is written
//	api        the HTTP API that changes rules while ghostwire runs
//	config     the YAML configuration file
//	dns        the DNS server of ghostwire exec, which applies DNS rules
package app

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// Version is the version reported by -version. The main package sets it
// from its build-time version; when empty, the module version recorded by
// "go install" is used.
var Version string

func buildVersion() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

// Main runs the ghostwire command with the arguments that follow the
// program name and returns its exit status.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "-version", "--version", "version":
			fmt.Println("ghostwire", buildVersion())
			return 0
		case "exec":
			return runExec(args[1:])
		case execChildArg:
			return runExecChild(args[1:])
		}
	}
	return runServer(args)
}

func serverUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, `Usage:
  sudo ghostwire [flags]                  run until interrupted
  ghostwire exec [flags] -- CMD [ARGS]    run CMD in a private network with ghostwire (no root needed)
  ghostwire -version

Examples:
  sudo ghostwire -rule 10.99.0.1=50ms -rule 10.99.0.2=delay=300ms,jitter=50ms -rule '10.99.0.3=20ms for 5 pings; timeout'
  sudo ghostwire -rule fd99::1=30ms -rule 10.99.0.4=unreachable
  sudo ghostwire -rule 8.8.8.8=30ms -path '8.8.8.8=192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms'
  sudo ghostwire -tcp 10.99.0.1:5432=timeout -tcp '10.99.0.2:5432=refuse for 3 requests; 5ms'
  sudo ghostwire -udp 10.99.0.1:514=refuse -udp '10.99.0.2:123=timeout for 2 requests; refuse'
  sudo ghostwire -config ghostwire.yaml
  ghostwire exec -rule 8.8.8.8=120ms -- ./my-app

Flags:
`)
	fs.PrintDefaults()
}

// runServer runs ghostwire in the current network until it is interrupted.
func runServer(args []string) int {
	s, rest, err := parseSettings("ghostwire", args, serverUsage, os.Stderr)
	if isHelp(err) {
		return 0
	}
	if err != nil {
		log.Print(err)
		return 2
	}
	if len(rest) > 0 {
		log.Printf("unexpected arguments %q (to run a command, use: ghostwire exec [flags] -- %s)", rest, rest[0])
		return 2
	}
	if s.outside {
		log.Printf("-outside, -outside-port and outside: in the configuration file only work with ghostwire exec, whose\n" +
			"command has a network of its own. ghostwire on its own already runs in the host's network.")
		return 2
	}
	if len(s.dns) > 0 {
		log.Printf("-dns and dns: in the configuration file only work with ghostwire exec, which gives the command its\n" +
			"own DNS server. For the whole machine, ghostwire cannot get between programs and the machine's resolver.")
		return 2
	}
	if s.names() {
		log.Printf("-host, rules for names, and hosts: in the configuration file only work with ghostwire exec, which\n" +
			"gives the command its own /etc/hosts and DNS server. To resolve a name to a mocked address for the\n" +
			"whole machine, add it to /etc/hosts and write the rule for the address.")
		return 2
	}
	in, err := start(s)
	if err != nil {
		log.Print(err)
		return 1
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigs:
		log.Printf("shutting down, removing %s", in.ifName)
		in.Close()
		return 0
	case err := <-in.Done():
		in.Close()
		if err != nil {
			log.Printf("read: %v", err)
			return 1
		}
		return 0
	}
}
