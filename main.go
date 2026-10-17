// Command pingmock answers ping (ICMP and ICMPv6 echo requests) with a
// configurable delay, jitter, loss or unreachable error, so that software
// which measures latency with ping can be tested deterministically.
//
//	sudo pingmock [flags]               mock pings for this machine
//	pingmock exec [flags] -- CMD ...    run CMD in a private network, no root
//
// The code lives in internal/app; see docs/architecture.md for a tour.
package main

import (
	"os"

	"github.com/nanmuyao/pingmock/internal/app"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version string

func main() {
	app.Version = version
	os.Exit(app.Main(os.Args[1:]))
}
