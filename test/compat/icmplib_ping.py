"""Pings the given hosts with icmplib, a common Python ping library, once
with a raw socket and once with an unprivileged ping socket, and prints one
line per host and mode for test/e2e-compat.sh to check:

    icmplib raw 10.99.0.1 sent=4 recv=4 avg=50.5
"""

import sys

from icmplib import multiping

# multiping pings the hosts at the same time, so that the lost replies are
# waited for once; it returns one result per host, in order.
for mode in ("raw", "dgram"):
    hosts = multiping(sys.argv[1:], count=4, interval=0.2, timeout=0.5, privileged=(mode == "raw"))
    for h in hosts:
        print(f"icmplib {mode} {h.address} sent={h.packets_sent} recv={h.packets_received} avg={h.avg_rtt:.1f}")
