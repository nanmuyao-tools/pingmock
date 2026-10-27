// Command pingmock mocks network answers for tests: it answers ping (ICMP
// and ICMPv6 echo requests) and TCP connection attempts with a
// configurable delay, jitter, loss, unreachable error or refusal, and,
// under pingmock exec, makes DNS lookups fail or come late, so that
// software that decides things from the network can be tested
// deterministically.
//
//	sudo pingmock [flags]               mock pings for this machine
//	pingmock exec [flags] -- CMD ...    run CMD in a private network, no root
//
// The code lives in internal/app; see docs/architecture.md for a tour.
package main

import (
	"os"

	"github.com/nanmuyao-tools/pingmock/internal/app"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version string

func main() {
	app.Version = version
	os.Exit(app.Main(os.Args[1:]))
}
