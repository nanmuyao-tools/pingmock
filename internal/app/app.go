// Package app is the pingmock command: it parses the command line and runs
// pingmock either in the current network (Main with flags) or around a
// command in a private network (Main with "exec").
//
// The packages it puts together are, in the order a ping meets them:
//
//	tun        the virtual network device the pings arrive on
//	responder  reads each packet and answers it
//	icmp       parses echo requests and builds replies
//	engine     decides, per target, how to answer and keeps statistics
//	rule       what a rule is and how it is written
//	api        the HTTP API that changes rules while pingmock runs
//	config     the YAML configuration file
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

// Main runs the pingmock command with the arguments that follow the
// program name and returns its exit status.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "-version", "--version", "version":
			fmt.Println("pingmock", buildVersion())
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
  sudo pingmock [flags]                  run until interrupted
  pingmock exec [flags] -- CMD [ARGS]    run CMD in a private network with pingmock (no root needed)
  pingmock -version

Examples:
  sudo pingmock -rule 10.99.0.1=50ms -rule 10.99.0.2=delay=300ms,jitter=50ms -rule '10.99.0.3=20ms for 5 pings; timeout'
  sudo pingmock -rule fd99::1=30ms -rule 10.99.0.4=unreachable
  sudo pingmock -config pingmock.yaml
  pingmock exec -rule 8.8.8.8=120ms -- ./my-app

Flags:
`)
	fs.PrintDefaults()
}

// runServer runs pingmock in the current network until it is interrupted.
func runServer(args []string) int {
	s, rest, err := parseSettings("pingmock", args, serverUsage, os.Stderr)
	if isHelp(err) {
		return 0
	}
	if err != nil {
		log.Print(err)
		return 2
	}
	if len(rest) > 0 {
		log.Printf("unexpected arguments %q (to run a command, use: pingmock exec [flags] -- %s)", rest, rest[0])
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
