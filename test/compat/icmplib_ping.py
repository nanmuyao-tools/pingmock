"""Pings the given hosts with icmplib, a common Python ping library, once
with a raw socket and once with an unprivileged ping socket, and prints one
line per host and mode for test/e2e-compat.sh to check:

    icmplib raw 10.99.0.1 sent=4 recv=4 avg=50.5
"""

import sys

from icmplib import ping

for mode in ("raw", "dgram"):
    for host in sys.argv[1:]:
        h = ping(host, count=4, interval=0.2, timeout=1, privileged=(mode == "raw"))
        print(f"icmplib {mode} {host} sent={h.packets_sent} recv={h.packets_received} avg={h.avg_rtt:.1f}")
