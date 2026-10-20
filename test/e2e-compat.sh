#!/usr/bin/env bash
# Compatibility check: common ping tools and libraries, run against pingmock,
# get the delays, losses and unreachable errors its rules say. Run as root on
# Linux with fping, Go and Python, with icmplib installed:
#   make e2e-compat
#   (or: go build -o pingmock . && pip install -r test/compat/requirements.txt &&
#        sudo env "PATH=$PATH" test/e2e-compat.sh)
# The libraries are tried both with a raw socket and with an unprivileged
# ping socket, which the kernel delivers replies to differently.
set -euo pipefail
cd "$(dirname "$0")/.."

PM=""
GROUPS_BEFORE=$(cat /proc/sys/net/ipv4/ping_group_range)
GROUPS_CHANGED=""
cleanup() {
  [ -n "$PM" ] && { kill "$PM" 2>/dev/null || true; wait "$PM" 2>/dev/null || true; }
  [ -n "$GROUPS_CHANGED" ] && echo "$GROUPS_BEFORE" >/proc/sys/net/ipv4/ping_group_range
  rm -rf "$WORK"
}
WORK=$(mktemp -d)
trap cleanup EXIT

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; exit 1; }

# The libraries' unprivileged mode needs ping sockets to be allowed for
# every group. Containers already allow them, and their /proc/sys is read
# only, so the range is only written when it has to be.
read -r lo hi <<<"$GROUPS_BEFORE"
if [ "$lo" -gt 0 ] || [ "$hi" -lt 2147483647 ]; then
  echo "0 2147483647" >/proc/sys/net/ipv4/ping_group_range
  GROUPS_CHANGED=1
fi

(cd test/compat && go build -o "$WORK/probing" . && go build -o "$WORK/tcpdial" ./tcpdial)

./pingmock -addr6= -rule 10.99.0.1=50ms -rule 10.99.0.2=delay=20ms,pattern=.x \
  -rule 10.99.0.3=unreachable -rule 8.8.8.8=30ms \
  -tcp 10.99.0.11:80=200ms -tcp 10.99.0.12:80=refuse -tcp 10.99.0.13:80=timeout -tcp 10.99.0.14:80=unreachable \
  -tcp 10.99.0.15:80=after=close -tcp 10.99.0.16:80=after=reset \
  >"$WORK/pingmock.log" 2>&1 &
PM=$!
for _ in $(seq 50); do curl -sf localhost:9100/rules >/dev/null && break; sleep 0.1; done
curl -sf localhost:9100/rules >/dev/null || { cat "$WORK/pingmock.log"; fail "pingmock did not start"; }

HOSTS="10.99.0.1 10.99.0.2 10.99.0.3 8.8.8.8"

# expect TOOL MODE HOST RECEIVED AVG_LOW AVG_HIGH checks one result line,
# "TOOL MODE HOST sent=4 recv=N avg=MS", from $results. With nothing received,
# the average is not checked.
expect() {
  local line recv avg
  line=$(grep "^$1 $2 $3 " <<<"$results") || fail "$1 $2 $3: no result in: $results"
  recv=$(sed -n 's/.* recv=\([0-9]*\).*/\1/p' <<<"$line")
  avg=$(sed -n 's/.* avg=\([0-9.]*\).*/\1/p' <<<"$line")
  [ "$recv" = "$4" ] || fail "$1 $2 $3: received $recv of 4, want $4"
  if [ "$4" != 0 ] && ! awk -v a="$avg" -v l="$5" -v h="$6" 'BEGIN { exit !(a >= l && a <= h) }'; then
    fail "$1 $2 $3: average ${avg}ms, want ${5}-${6}ms"
  fi
  if [ "$4" = 0 ]; then pass "$1 $2 $3: none of 4"; else pass "$1 $2 $3: $recv of 4, ${avg}ms"; fi
}

# Every tool, mode and host: 50ms; every other ping lost; unreachable, which
# the tools count as lost; and a real address, hijacked.
check_all() { # TOOL MODES...
  local tool=$1 mode
  shift
  for mode in "$@"; do
    expect "$tool" "$mode" 10.99.0.1 4 48 60
    expect "$tool" "$mode" 10.99.0.2 2 18 30
    expect "$tool" "$mode" 10.99.0.3 0
    expect "$tool" "$mode" 8.8.8.8 4 28 40
  done
}

# fping prints "HOST : xmt/rcv/%loss = 4/2/50%, min/avg/max = 20.5/20.5/20.5".
# shellcheck disable=SC2086 # HOSTS is a list of addresses
results=$(fping -c 4 -p 200 $HOSTS 2>&1 | awk -F'[ /=,]+' '/xmt/ {
  avg = ""; for (i = 1; i <= NF; i++) if ($i == "min") avg = $(i + 4)
  printf "fping raw %s sent=%s recv=%s avg=%s\n", $1, $6, $7, avg }') || true
check_all fping raw

# shellcheck disable=SC2086
results=$("$WORK/probing" $HOSTS 2>&1) || fail "pro-bing: $results"
check_all pro-bing raw dgram

# shellcheck disable=SC2086
results=$(python3 test/compat/icmplib_ping.py $HOSTS 2>&1) || fail "icmplib: $results"
check_all icmplib raw dgram

# TCP rules, seen by the standard libraries of Go and Python: the error each
# outcome gives, the connect time, and on an accepted connection what a
# request gets: a read timeout under after=hang, on which pingmock never
# sends anything, end of file under after=close, a reset under after=reset.
TARGETS="10.99.0.11:80 10.99.0.12:80 10.99.0.13:80 10.99.0.14:80 10.99.0.15:80 10.99.0.16:80"
expect_tcp() { # LANG TARGET OUTCOME [MS_LOW MS_HIGH]; OUTCOME is a pattern
  local line
  line=$(grep "^$1 $2 " <<<"$results") || fail "$1 $2: no result in: $results"
  # shellcheck disable=SC2254 # OUTCOME is matched as a pattern
  case "$line" in
  "$1 $2 "$3*) ;;
  *) fail "$1 $2: got '$line', want $3" ;;
  esac
  if [ -n "${4:-}" ]; then
    local ms
    ms=$(sed -n 's/.* accepted \([0-9]*\)ms.*/\1/p' <<<"$line")
    [ "$ms" -ge "$4" ] && [ "$ms" -le "$5" ] || fail "$1 $2: connected in ${ms}ms, want $4-$5ms"
    grep -q 'read=timeout' <<<"$line" || fail "$1 $2: $line, want the read to time out"
  fi
  pass "$line"
}
for lang in go python; do
  if [ "$lang" = go ]; then
    # shellcheck disable=SC2086 # TARGETS is a list of addresses
    results=$("$WORK/tcpdial" $TARGETS 2>&1) || fail "tcpdial: $results"
  else
    # shellcheck disable=SC2086
    results=$(python3 test/compat/tcp_connect.py $TARGETS 2>&1) || fail "tcp_connect.py: $results"
  fi
  expect_tcp "$lang" 10.99.0.11:80 accepted 195 230
  expect_tcp "$lang" 10.99.0.12:80 refused
  expect_tcp "$lang" 10.99.0.13:80 timeout
  expect_tcp "$lang" 10.99.0.14:80 unreachable
  expect_tcp "$lang" 10.99.0.15:80 "accepted [0-9]*ms read=closed"
  expect_tcp "$lang" 10.99.0.16:80 "accepted [0-9]*ms read=reset"
done

echo "all compatibility checks passed"
