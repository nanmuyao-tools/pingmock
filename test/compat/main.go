// Command probing pings the given hosts with pro-bing, the most used Go ping
// library, once with a raw socket and once with an unprivileged ping socket,
// and prints one line per host and mode for test/e2e-compat.sh to check:
//
//	pro-bing raw 10.99.0.1 sent=4 recv=4 avg=50.5
//
// It is its own module so that pro-bing does not become a dependency of
// pingmock.
package main

import (
	"fmt"
	"os"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

func main() {
	for _, mode := range []string{"raw", "dgram"} {
		for _, host := range os.Args[1:] {
			p, err := probing.NewPinger(host)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			p.SetPrivileged(mode == "raw")
			p.Count, p.Interval, p.Timeout = 4, 200*time.Millisecond, 3*time.Second
			if err := p.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "pro-bing %s %s: %v\n", mode, host, err)
				os.Exit(1)
			}
			s := p.Statistics()
			fmt.Printf("pro-bing %s %s sent=%d recv=%d avg=%.1f\n",
				mode, host, s.PacketsSent, s.PacketsRecv, float64(s.AvgRtt.Microseconds())/1000)
		}
	}
}
