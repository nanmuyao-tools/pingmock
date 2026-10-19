#!/usr/bin/env bash
# End-to-end check: run as root on Linux with iproute2, ping and curl.
#   make e2e    (or: go build -o pingmock . && sudo test/e2e.sh)
# The IPv6 checks are skipped when the kernel has no IPv6, unless
# REQUIRE_IPV6=1 is set (as in CI), which makes that a failure.
set -euo pipefail
cd "$(dirname "$0")/.."

PM=""
start() { # args passed to pingmock
  ./pingmock "$@" >/tmp/pingmock-e2e.log 2>&1 &
  PM=$!
  for _ in $(seq 50); do curl -sf localhost:9100/rules >/dev/null && return; sleep 0.1; done
  echo "FAIL pingmock did not start:"; cat /tmp/pingmock-e2e.log; exit 1
}
stop() { kill "$PM" 2>/dev/null || true; wait "$PM" 2>/dev/null || true; PM=""; }
trap stop EXIT

avg() { ping -c "$2" -i 0.2 -W 2 "$1" | awk -F'/' '/^rtt/ {print $5}'; }
# Sequence numbers of the echo replies (not of error messages) ping received.
received() { ping -c "$2" -i 0.05 -W 1 "$1" 2>/dev/null | grep 'bytes from' | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true; }
pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; exit 1; }
near() { # name, actual avg ms, expected ms, tolerance ms
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }

start -seed 1 -rule 10.99.0.1=50ms -rule 10.99.0.2=timeout \
  -rule '10.99.0.3=delay=1ms,pattern=..x' -rule '10.99.0.4=10ms for 2 pings; 120ms'

near "static 50ms" "$(avg 10.99.0.1 3)" 50 5
curl -sf -X PUT localhost:9100/rules/10.99.0.1 -d 250ms >/dev/null
near "changed at runtime to 250ms" "$(avg 10.99.0.1 3)" 250 10

if ping -c 2 -W 1 10.99.0.2 >/dev/null 2>&1; then fail "timeout target answered"; fi
pass "timeout target gives no reply"

same "pattern ..x drops every third request" "$(received 10.99.0.3 9)" "1 2 4 5 7 8 "

near "count phase 1 (10ms for 2 pings)" "$(avg 10.99.0.4 2)" 10 3
near "count phase 2 (120ms afterwards)" "$(avg 10.99.0.4 2)" 120 5

events=$(curl -sf "localhost:9100/history?target=10.99.0.3" | grep -c '"seq":' || true)
same "history records every request" "$events" 9

# ttl= sets the TTL of the replies; without it, 64.
curl -sf -X PUT localhost:9100/rules/10.99.0.5 -d 'delay=1ms,ttl=128' >/dev/null
ttl() { ping -c 1 -W 2 "$1" | grep -o 'ttl=[0-9]*' | head -1; }
same "ttl=128 shows in ping" "$(ttl 10.99.0.5)" "ttl=128"
same "the default TTL is 64" "$(ttl 10.99.0.1)" "ttl=64"
stop

# -host is for exec only: the server mode refuses it instead of ignoring it.
status=0
./pingmock -host test.example=10.99.0.1 >/dev/null 2>&1 || status=$?
same "-host is refused outside pingmock exec" "$status" 2

# Destination unreachable: ping must report each code, and fail fast.
start -rule 10.99.0.8=unreachable -rule 10.99.0.9=unreachable=net \
  -rule 10.99.0.10=unreachable=admin -rule '10.99.0.11=delay=5ms,pattern=..u'
errors() { ping -c "$2" -i 0.2 -W 2 "$1" 2>/dev/null | grep -c "$3" || true; }
same "unreachable reports host unreachable" "$(errors 10.99.0.8 2 'Destination Host Unreachable')" 2
same "unreachable=net reports net unreachable" "$(errors 10.99.0.9 2 'Destination Net Unreachable')" 2
same "unreachable=admin reports packet filtered" "$(errors 10.99.0.10 2 'Packet filtered')" 2
same "pattern ..u answers every third with unreachable" "$(received 10.99.0.11 6)" "1 2 4 5 "
start_ns=$(date +%s%N)
ping -c 1 -W 3 10.99.0.8 >/dev/null 2>&1 || true
elapsed_ms=$(( ($(date +%s%N) - start_ns) / 1000000 ))
if [ "$elapsed_ms" -lt 1000 ]; then pass "unreachable fails fast (${elapsed_ms}ms, timeout 3s)"; else fail "unreachable took ${elapsed_ms}ms"; fi
stop

# The same seed must reproduce the same random losses across runs.
start -seed 1234 -rule 10.99.0.5=loss=50%
first=$(received 10.99.0.5 20)
stop
start -seed 1234 -rule 10.99.0.5=loss=50%
second=$(received 10.99.0.5 20)
stop
same "same seed reproduces random loss ($first)" "$second" "$first"

# A configuration file, with a comment, a bare number of milliseconds and
# a flag overriding one of its rules.
cfg=$(mktemp --suffix=.yaml)
printf 'seed: 3\nrules:\n  10.99.0.6: 30   # milliseconds\n  10.99.0.7: timeout\n  10.99.0.8: unreachable\n' >"$cfg"
start -config "$cfg" -rule 10.99.0.7=60ms
near "rule from the config file" "$(avg 10.99.0.6 2)" 30 4
near "flag overrides the config file" "$(avg 10.99.0.7 2)" 60 5
same "unreachable rule from the config file" "$(ping -c 1 -W 2 10.99.0.8 2>/dev/null | grep -c 'Destination Host Unreachable' || true)" 1
stop
rm -f "$cfg"

# IPv6.
if [ ! -e /proc/net/if_inet6 ]; then
  if [ "${REQUIRE_IPV6:-0}" = 1 ]; then fail "IPv6 is unavailable but REQUIRE_IPV6=1"; fi
  echo "SKIP IPv6 checks: the kernel has no IPv6"
else
  start -addr6 fd99::fe/64 -rule fd99::1=40ms -rule fd99::2=unreachable \
    -rule fd99::3=unreachable=net -rule fd99::4=unreachable=admin \
    -rule 'fd99::5=delay=5ms,pattern=..x' -rule 2001:db8::8888=25ms
  avg6() { ping -6 -c "$2" -i 0.2 -W 2 "$1" | awk -F'/' '/^rtt/ {print $5}'; }
  near "IPv6 delay 40ms" "$(avg6 fd99::1 3)" 40 5
  near "IPv6 real address mocked through a route" "$(avg6 2001:db8::8888 2)" 25 5
  errors6() { ping -6 -c 2 -i 0.2 -W 2 "$1" 2>/dev/null | grep -c "$2" || true; }
  same "IPv6 unreachable reports address unreachable" "$(errors6 fd99::2 'Destination unreachable: Address unreachable')" 2
  same "IPv6 unreachable=net reports no route" "$(errors6 fd99::3 'Destination unreachable: No route')" 2
  same "IPv6 unreachable=admin reports administratively prohibited" "$(errors6 fd99::4 'Destination unreachable: Administratively prohibited')" 2
  got6=$(ping -6 -c 6 -i 0.05 -W 1 fd99::5 2>/dev/null | grep 'bytes from' | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true)
  same "IPv6 pattern ..x" "$got6" "1 2 4 5 "
  events=$(curl -sf "localhost:9100/history?target=fd99::/64" | grep -c '"dst": "fd99::' || true)
  [ "$events" -gt 0 ] && pass "history filters IPv6 prefixes ($events events)" || fail "no IPv6 events in history"
  stop
fi

# A network the host is on: pingmock must take its traffic without replacing
# the host's route for it, which would cut the host off from that network,
# even after pingmock exits. The device pmhost0 stands for such a network.
ip tuntap add dev pmhost0 mode tun
trap 'stop; ip link del pmhost0 2>/dev/null || true' EXIT
ip addr add 192.168.77.5/24 dev pmhost0
ip link set pmhost0 up
host_route() { ip -o "$1" route show exact "$2" table main | grep -c 'dev pmhost0' || true; }
start -rule 192.168.77.0/24=60ms
near "a network the host is on is mocked" "$(avg 192.168.77.9 2)" 60 5
same "the host's route for it stays while pingmock runs" "$(host_route -4 192.168.77.0/24)" 1
grep -q "leaving the host's route for 192.168.77.0/24 (dev pmhost0) in place" /tmp/pingmock-e2e.log &&
  pass "pingmock logs that it left the route in place" || fail "no log line about the route left in place"
stop
same "the host's route for it stays after pingmock exits" "$(host_route -4 192.168.77.0/24)" 1

# A single address with a route of its own cannot be split around: pingmock
# refuses to start rather than replace that route.
ip route add 192.168.77.99/32 dev pmhost0
if ./pingmock -addr6= -api= -rule 192.168.77.99=5ms >/tmp/pingmock-e2e.log 2>&1; then
  fail "pingmock started although 192.168.77.99 has a route of its own"
fi
grep -q "use pingmock exec" /tmp/pingmock-e2e.log && pass "an address with a route of its own is refused" ||
  { cat /tmp/pingmock-e2e.log; fail "the refusal does not suggest pingmock exec"; }
same "and its route stays" "$(host_route -4 192.168.77.99/32)" 1

if [ -e /proc/net/if_inet6 ]; then
  ip -6 addr add fd77::5/64 dev pmhost0 nodad
  start -rule fd77::/64=60ms
  near "IPv6: a network the host is on is mocked" "$(avg6 fd77::9 2)" 60 5
  same "IPv6: the host's route for it stays while pingmock runs" "$(host_route -6 fd77::/64)" 1
  stop
  same "IPv6: the host's route for it stays after pingmock exits" "$(host_route -6 fd77::/64)" 1
fi
ip link del pmhost0

echo "all checks passed"
