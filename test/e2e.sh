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

events=$(curl -sf "localhost:9100/history?target=10.99.0.3" | grep -o '"seq"' | wc -l)
same "history records every request" "$events" 9
stop

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

# A configuration file, with a flag overriding one of its rules.
cfg=$(mktemp)
echo '{"seed": 3, "rules": {"10.99.0.6": "30ms", "10.99.0.7": "timeout"}}' >"$cfg"
start -config "$cfg" -rule 10.99.0.7=60ms
near "rule from config file" "$(avg 10.99.0.6 2)" 30 4
near "flag overrides config file" "$(avg 10.99.0.7 2)" 60 5
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
  same "IPv6 unreachable reports address unreachable" "$(errors6 fd99::2 'Address unreachable')" 2
  same "IPv6 unreachable=net reports no route" "$(errors6 fd99::3 'No route')" 2
  same "IPv6 unreachable=admin reports administratively prohibited" "$(errors6 fd99::4 'Administratively prohibited')" 2
  got6=$(ping -6 -c 6 -i 0.05 -W 1 fd99::5 2>/dev/null | grep 'bytes from' | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true)
  same "IPv6 pattern ..x" "$got6" "1 2 4 5 "
  events=$(curl -sf "localhost:9100/history?target=fd99::/64" | grep -o '"dst":"fd99::' | wc -l)
  [ "$events" -gt 0 ] && pass "history filters IPv6 prefixes ($events events)" || fail "no IPv6 events in history"
  stop
fi

echo "all checks passed"
