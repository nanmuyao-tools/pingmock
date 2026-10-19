#!/usr/bin/env bash
# End-to-end checks for "pingmock exec". Run it as an unprivileged user to
# test the rootless path, and as root to test the root path:
#   go build -o pingmock . && test/e2e-exec.sh && sudo test/e2e-exec.sh
set -euo pipefail
cd "$(dirname "$0")/.."
PM=./pingmock

# pingmock's own log goes to a file, printed whenever a check fails, so a
# failure in CI always shows its cause.
LOG=$(mktemp)
trap 'status=$?; echo "FAIL at line $LINENO (exit $status)"; echo "--- pingmock log:"; tail -n 20 "$LOG"; exit $status' ERR

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; echo "--- pingmock log:"; tail -n 20 "$LOG"; exit 1; }
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }
near() {
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
run() { "$PM" exec -addr6 "" "$@" 2>>"$LOG"; }

if [ "$(id -u)" = 0 ]; then echo "running as root"; else echo "running as $(id -un) (rootless)"; fi

avg=$(run -rule 10.99.0.1=45ms -- sh -c "ping -c 3 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'")
near "ping inside exec" "$avg" 45 5

status=0
run -- sh -c 'exit 7' || status=$?
same "exit status of the command is passed on" "$status" 7

status=0
run -- no-such-command-pingmock || status=$?
same "missing command exits 127" "$status" 127

api=$(run -rule 10.99.0.1=10ms -- sh -c 'echo "$PINGMOCK_API"')
same "PINGMOCK_API is set for the command" "$api" "http://127.0.0.1:9100"

changed=$(run -rule 10.99.0.1=10ms -- sh -c "
  curl -sf -X PUT \"\$PINGMOCK_API/rules/10.99.0.1\" -d 120ms >/dev/null
  ping -c 2 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'")
near "rules change through the API inside exec" "$changed" 120 6

# POST /reset puts the startup rules back; GET /version names the build.
reset=$(run -rule 10.99.0.1=10ms -- sh -c "
  curl -sf -X PUT \"\$PINGMOCK_API/rules/10.99.0.1\" -d 120ms >/dev/null
  curl -sf -X POST \"\$PINGMOCK_API/reset\"
  ping -c 2 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'")
near "POST /reset restores the startup rules" "$reset" 10 5
version=$(run -- sh -c 'curl -sf "$PINGMOCK_API/version"' | grep -o '"version": *"[^"]*"' | cut -d'"' -f4)
same "GET /version reports the build" "$version" "$("$PM" -version | cut -d' ' -f2)"

# -host gives the command a private /etc/hosts; the system's is untouched.
before=$(md5sum /etc/hosts)
resolved=$(run -host test.example=10.99.0.1 -rule 10.99.0.1=35ms -- sh -c "
  getent hosts test.example | awk '{print \$1}'
  ping -c 2 -i 0.2 test.example | awk -F/ '/^rtt/ {print \$5}'")
same "-host resolves the name for the command" "$(echo "$resolved" | head -1)" "10.99.0.1"
near "-host: ping by name gets the rule's delay" "$(echo "$resolved" | tail -1)" 35 5
same "-host leaves the system's /etc/hosts alone" "$(md5sum /etc/hosts)" "$before"
same "-host leaves no file behind" "$(ls /tmp/pingmock-hosts-* 2>/dev/null | wc -l)" 0

# A rule for a name: the command's DNS server resolves it to an address
# from the top of the subnet, both ways, and other names still resolve
# through the host's resolver.
before_resolv=$(md5sum /etc/resolv.conf 2>/dev/null || true)
named=$(run -rule by-name.example=45ms -- sh -c "
  getent hosts by-name.example | awk '{print \$1}'
  getent hosts 10.99.0.253 | awk '{print \$2}'
  ping -c 2 -i 0.2 by-name.example | awk -F/ '/^rtt/ {print \$5}'
  getent hosts does-not-exist.invalid >/dev/null; echo \$?")
same "a rule for a name resolves to the top of the subnet" "$(echo "$named" | sed -n 1p)" "10.99.0.253"
same "the address resolves back to the name" "$(echo "$named" | sed -n 2p)" "by-name.example"
near "ping by name gets the name's rule" "$(echo "$named" | sed -n 3p)" 45 5
# Docker Desktop's resolver answers every name with a made-up address, so
# this check only means something where the host does not resolve the name.
if getent hosts does-not-exist.invalid >/dev/null 2>&1; then
  echo "SKIP an unknown name does not resolve (this host's resolver answers for does-not-exist.invalid)"
else
  same "an unknown name does not resolve" "$(echo "$named" | sed -n 4p)" 2
fi
same "the system's resolv.conf is left alone" "$(md5sum /etc/resolv.conf 2>/dev/null || true)" "$before_resolv"
if getent hosts github.com >/dev/null 2>&1; then
  forwarded=$(run -rule by-name.example=1ms -- sh -c "getent hosts github.com | awk '{print \$1}' | head -1")
  [ -n "$forwarded" ] && pass "a real name is forwarded to the host's resolver: $forwarded" || fail "github.com did not resolve inside exec"
else
  echo "SKIP forwarding to the host's resolver (this host cannot resolve github.com)"
fi
leftover=0
for f in /tmp/pingmock-*; do [ -e "$f" ] && [ "$f" != /tmp/pingmock-e2e.log ] && leftover=$((leftover + 1)); done
same "no temporary file is left behind" "$leftover" 0

# A path inside exec: traceroute, which opens raw sockets, works there
# without root, and sees the hops.
if command -v traceroute >/dev/null; then
  seen=$(run -rule 10.99.0.80=30ms -path '10.99.0.80=192.168.1.1@2ms > * > 72.14.0.1@25ms' -- sh -c \
    "traceroute -n -q 1 -w 1 -m 6 10.99.0.80 2>/dev/null | tail -n +2 | awk '{print \$2}' | tr '\n' ' '")
  same "traceroute inside exec shows the path" "$seen" "192.168.1.1 * 72.14.0.1 10.99.0.80 "
else
  echo "SKIP paths inside exec: traceroute is not installed"
fi

# The host network is untouched while a run is in progress.
run -- sleep 2 &
bg=$!
sleep 1
if ip link show pingmock0 >/dev/null 2>&1; then fail "a pingmock0 device appeared on the host"; fi
wait "$bg"
pass "the host network is untouched"

# Parallel runs are isolated: same device name and API port, different rules.
out=$(mktemp -d)
for d in 20 50 80; do
  run -rule 10.99.0.1=${d}ms -- sh -c "sleep 0.3; ping -c 2 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'" >"$out/$d" &
done
wait
for d in 20 50 80; do near "parallel run with ${d}ms" "$(cat "$out/$d")" "$d" 5; done
rm -rf "$out"

# Background processes left by the command are stopped when it exits.
before=$(pgrep -c -x -u "$(id -u)" sleep || true)
run -- sh -c 'sleep 120 & exit 0'
sleep 0.5
after=$(pgrep -c -x -u "$(id -u)" sleep || true)
same "background processes are stopped" "$after" "$before"

rm -f "$LOG"
echo "all exec checks passed"
