#!/usr/bin/env bash
# End-to-end checks for "pingmock exec". Run it as an unprivileged user to
# test the rootless path, and as root to test the root path:
#   go build -o pingmock . && test/e2e-exec.sh && sudo test/e2e-exec.sh
set -euo pipefail
cd "$(dirname "$0")/.."
PM=./pingmock

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; exit 1; }
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }
near() {
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
run() { "$PM" exec -addr6 "" "$@" 2>/dev/null; } # pingmock's own log goes to stderr

if [ "$(id -u)" = 0 ]; then echo "running as root"; else echo "running as $(id -un) (rootless)"; fi

avg=$(run -rule 10.99.0.1=45ms -- sh -c "ping -c 3 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'")
near "ping inside exec" "$avg" 45 5

set +e
run -- sh -c 'exit 7'
status=$?
set -e
same "exit status of the command is passed on" "$status" 7

set +e
run -- no-such-command-pingmock
status=$?
set -e
same "missing command exits 127" "$status" 127

api=$(run -rule 10.99.0.1=10ms -- sh -c 'echo "$PINGMOCK_API"')
same "PINGMOCK_API is set for the command" "$api" "http://127.0.0.1:9100"

changed=$(run -rule 10.99.0.1=10ms -- sh -c "
  curl -sf -X PUT \"\$PINGMOCK_API/rules/10.99.0.1\" -d 120ms >/dev/null
  ping -c 2 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print \$5}'")
near "rules change through the API inside exec" "$changed" 120 6

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

echo "all exec checks passed"
