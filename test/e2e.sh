#!/usr/bin/env bash
# End-to-end check: run as root on Linux with iproute2, ping and curl.
#   make e2e    (or: go build -o pingmock . && sudo test/e2e.sh)
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
received() { ping -c "$2" -i 0.05 -W 1 "$1" 2>/dev/null | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true; }
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

echo "all checks passed"
