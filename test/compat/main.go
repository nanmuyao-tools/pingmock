// Command probing pings the given hosts with pro-bing, the most used Go ping
// library, once with a raw socket and once with an unprivileged ping socket,
// and prints one line per host and mode for test/e2e-compat.sh to check:
//
//	pro-bing raw 10.99.0.1 sent=4 recv=4 avg=50.5
//
// It is its own module so that pro-bing does not become a dependency of
// ghostwire.
package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

func main() {
	hosts := os.Args[1:]
	for _, mode := range []string{"raw", "dgram"} {
		// The hosts are pinged at the same time, each with its own
		// pinger, so that the lost replies are waited for once.
		lines := make([]string, len(hosts))
		errs := make([]error, len(hosts))
		var wg sync.WaitGroup
		for i, host := range hosts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				lines[i], errs[i] = ping(mode, host)
			}()
		}
		wg.Wait()
		for i := range hosts {
			if errs[i] != nil {
				fmt.Fprintf(os.Stderr, "pro-bing %s %s: %v\n", mode, hosts[i], errs[i])
				os.Exit(1)
			}
			fmt.Println(lines[i])
		}
	}
}

func ping(mode, host string) (string, error) {
	p, err := probing.NewPinger(host)
	if err != nil {
		return "", err
	}
	p.SetPrivileged(mode == "raw")
	p.Count, p.Interval, p.Timeout = 4, 200*time.Millisecond, 1500*time.Millisecond
	if err := p.Run(); err != nil {
		return "", err
	}
	s := p.Statistics()
	return fmt.Sprintf("pro-bing %s %s sent=%d recv=%d avg=%.1f",
		mode, host, s.PacketsSent, s.PacketsRecv, float64(s.AvgRtt.Microseconds())/1000), nil
}
