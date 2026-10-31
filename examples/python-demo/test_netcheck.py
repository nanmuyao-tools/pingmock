"""Tests for netcheck, run inside ghostwire exec:

    ghostwire exec -config ghostwire.yaml -- python3 -m pytest

ghostwire.yaml makes both hosts answer quickly; each test changes that as it
needs through ghostwire's control API, and the fixture resets it before the
next one.
Inside ghostwire exec, mocking 8.8.8.8 affects only these tests.
"""

import time

import netcheck

HOSTS = ["8.8.8.8", "1.1.1.1"]


def test_online_when_hosts_answer_quickly(ghostwire):
    status = netcheck.check(HOSTS)
    assert status.state == netcheck.ONLINE
    by_host = {r.host: r for r in status.results}
    assert 13 <= by_host["8.8.8.8"].avg_ms <= 20  # ghostwire.yaml: 15ms
    assert status.reason.startswith("8.8.8.8 answers")  # the faster of the two


def test_online_while_one_host_is_down(ghostwire):
    ghostwire.set_rule("8.8.8.8", "timeout")
    status = netcheck.check(HOSTS)
    assert status.state == netcheck.ONLINE
    assert status.reason.startswith("1.1.1.1 answers")


def test_degraded_when_every_host_is_slow(ghostwire):
    ghostwire.set_rule("8.8.8.8", "delay=300ms,jitter=20ms")
    ghostwire.set_rule("1.1.1.1", "400ms")
    assert netcheck.check(HOSTS).state == netcheck.DEGRADED


def test_degraded_when_every_host_loses_pings(ghostwire):
    # pattern=..x drops every third ping: exactly one of the three, every
    # run, unlike loss=33%, which is random (though reproducible with a seed).
    ghostwire.set_rule("8.8.8.8", "delay=15ms,pattern=..x")
    ghostwire.set_rule("1.1.1.1", "delay=20ms,pattern=..x")
    status = netcheck.check(HOSTS)
    assert status.state == netcheck.DEGRADED
    assert all(r.received == 2 for r in status.results)


def test_offline_when_unreachable_is_reported_quickly(ghostwire):
    # An unreachable error comes back at once, unlike a timeout.
    ghostwire.set_rule("8.8.8.8", "unreachable")
    ghostwire.set_rule("1.1.1.1", "unreachable=admin")
    start = time.monotonic()
    status = netcheck.check(HOSTS)
    assert status.state == netcheck.OFFLINE
    assert all(r.errors == 3 for r in status.results)
    assert time.monotonic() - start < 1.0


def test_offline_when_nothing_answers(ghostwire):
    ghostwire.set_rule("8.8.8.8", "timeout")
    ghostwire.set_rule("1.1.1.1", "timeout")
    status = netcheck.check(HOSTS)
    assert status.state == netcheck.OFFLINE
    assert all(r.errors == 0 for r in status.results)


def test_notices_when_the_connection_drops(ghostwire):
    # A scenario: the hosts answer the first 3 pings, then nothing. No API
    # call is needed between the two checks.
    ghostwire.set_rule("8.8.8.8", "15ms for 3 pings; timeout")
    ghostwire.set_rule("1.1.1.1", "20ms for 3 pings; timeout")
    assert netcheck.check(HOSTS).state == netcheck.ONLINE
    assert netcheck.check(HOSTS).state == netcheck.OFFLINE


def test_pings_each_host_the_given_number_of_times(ghostwire):
    ghostwire.clear_history()
    netcheck.check(HOSTS, count=4)
    # ghostwire's history shows what the code under test really did.
    for host in HOSTS:
        events = ghostwire.history(host)
        assert [e["seq"] for e in events] == [1, 2, 3, 4]
        assert all(e["result"] == "reply" for e in events)


def test_reaches_the_api(ghostwire):
    assert netcheck.reach("api.example.com", 443) == netcheck.REACHABLE


def test_tells_a_missing_name(ghostwire):
    ghostwire.set_dns("api.example.com", "nxdomain")
    assert netcheck.reach("api.example.com", 443) == netcheck.NO_SUCH_NAME


def test_tells_a_refused_connection(ghostwire):
    ghostwire.set_tcp("10.99.0.10:443", "refuse")  # api.example.com (ghostwire.yaml)
    start = time.monotonic()
    assert netcheck.reach("api.example.com", 443) == netcheck.REFUSED
    assert time.monotonic() - start < 0.5  # a reset comes back at once


def test_tells_a_connection_that_hangs(ghostwire):
    ghostwire.set_tcp("10.99.0.10:443", "timeout")
    assert netcheck.reach("api.example.com", 443, timeout=0.3) == netcheck.TIMED_OUT
    # Each attempt is in the history, however often the kernel sent its SYN.
    attempts = [e for e in ghostwire.history("10.99.0.10") if e.get("probe") == "tcp" and not e.get("repeat")]
    assert [(e["port"], e["result"]) for e in attempts] == [(443, "drop")]
