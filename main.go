// Command pingmock answers ICMP and ICMPv6 echo requests ("ping") with a
// configurable delay, jitter, loss or unreachable error, so software that
// measures latency with ping can be tested deterministically.
//
// It creates a TUN device, routes test subnets (and optionally real
// addresses such as 8.8.8.8) to it, and answers every echo request it
// receives according to the matching rule.
//
//	pingmock [flags]                   run until interrupted (needs root)
//	pingmock exec [flags] -- CMD ...   run CMD in a private network with
//	                                   pingmock, without root
package main

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

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "-version", "--version", "version":
			fmt.Println("pingmock", buildVersion())
			return
		case "exec":
			os.Exit(runExec(args[1:]))
		case execChildArg:
			os.Exit(runExecChild(args[1:]))
		}
	}
	os.Exit(runServer(args))
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
