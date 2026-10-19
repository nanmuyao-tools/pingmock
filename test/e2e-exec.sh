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
