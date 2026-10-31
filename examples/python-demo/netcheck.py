"""Tell whether the internet connection works, from pings to a few
well-known hosts.

This is the code under test in this example. It knows nothing about
ghostwire and runs the system's ping command, as many tools do;
test_netcheck.py tests it against ghostwire.

    python3 netcheck.py 8.8.8.8 1.1.1.1

reach() tells, in the same way, why a service cannot be reached: its name
does not resolve, or the connection is refused or times out.
"""

import re
import socket
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field

ONLINE, DEGRADED, OFFLINE = "online", "degraded", "offline"

_REPLY = re.compile(r"bytes from .*time=([0-9.]+) ms")
_ERROR = re.compile(r"unreachable|prohibited|filtered", re.IGNORECASE)


@dataclass
class Result:
    """The outcome of pinging one host."""

    host: str
    sent: int
    replies: list = field(default_factory=list)  # round trips, in milliseconds
    errors: int = 0  # ICMP errors such as "Destination Host Unreachable"

    @property
    def received(self):
        return len(self.replies)

    @property
    def loss(self):
        return 1 - self.received / self.sent if self.sent else 1.0

    @property
    def avg_ms(self):
        return sum(self.replies) / len(self.replies) if self.replies else None


def measure(host, count=3, timeout=1):
    """Ping host count times with the system's ping command."""
    out = subprocess.run(
        ["ping", "-n", "-c", str(count), "-i", "0.2", "-W", str(timeout), host],
        capture_output=True,
        text=True,
    ).stdout
    result = Result(host, count)
    for line in out.splitlines():
        if m := _REPLY.search(line):
            result.replies.append(float(m[1]))
        elif _ERROR.search(line):
            result.errors += 1
    return result


@dataclass
class Status:
    state: str  # ONLINE, DEGRADED or OFFLINE
    reason: str
    results: list


def check(hosts, count=3, slow_ms=200, max_loss=0.2):
    """Ping all hosts at once and tell the state of the connection.

    It is ONLINE if at least one host answers within slow_ms and loses at
    most max_loss of the pings, DEGRADED if hosts answer but only slowly or
    with more loss, and OFFLINE if none answers.
    """
    with ThreadPoolExecutor(len(hosts)) as pool:
        results = list(pool.map(lambda h: measure(h, count), hosts))
    answering = sorted((r for r in results if r.received), key=lambda r: r.avg_ms)
    good = [r for r in answering if r.loss <= max_loss and r.avg_ms <= slow_ms]
    if good:
        best = good[0]
        return Status(ONLINE, f"{best.host} answers in {best.avg_ms:.0f}ms", results)
    if answering:
        best = answering[0]
        return Status(DEGRADED, f"best is {best.host}: {best.avg_ms:.0f}ms, {best.loss:.0%} lost", results)
    errors = sum(r.errors for r in results)
    return Status(OFFLINE, f"no host answers ({errors} unreachable errors)", results)


REACHABLE, NO_SUCH_NAME, REFUSED, TIMED_OUT = "reachable", "no such name", "refused", "timed out"


def reach(name, port, timeout=1.0):
    """Resolve name and connect to port, and tell how that went: REACHABLE,
    NO_SUCH_NAME, REFUSED or TIMED_OUT."""
    try:
        addr = socket.getaddrinfo(name, port, type=socket.SOCK_STREAM)[0][4]
    except socket.gaierror:
        return NO_SUCH_NAME
    try:
        socket.create_connection(addr[:2], timeout=timeout).close()
    except ConnectionRefusedError:
        return REFUSED
    except TimeoutError:
        return TIMED_OUT
    return REACHABLE


def main(argv):
    status = check(argv or ["8.8.8.8", "1.1.1.1"])
    for r in status.results:
        if r.received:
            print(f"{r.host:<15} {r.avg_ms:6.1f}ms  loss {r.loss:4.0%}")
        else:
            print(f"{r.host:<15} no reply ({r.errors} errors)")
    print(f"{status.state}: {status.reason}")
    return {ONLINE: 0, DEGRADED: 1, OFFLINE: 2}[status.state]


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
