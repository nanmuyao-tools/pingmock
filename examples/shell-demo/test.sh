#!/usr/bin/env bash
# Tests for failover.sh, run inside pingmock exec:
#
#   pingmock exec -config pingmock.yaml -- ./test.sh
#
# Each test starts by stating the whole network it needs through pingmock's
# control API, with nothing but curl.
set -uo pipefail
cd "$(dirname "$0")" || exit 1

if [ -z "${PINGMOCK_API:-}" ]; then
  echo "SKIP: not running under pingmock; run: pingmock exec -config pingmock.yaml -- ./test.sh"
  exit 0
fi

PRIMARY=10.99.0.1
BACKUP=10.99.0.2
passed=0
failed=0

# network PRIMARY_RULE BACKUP_RULE sets both links and starts from a fresh
# state file, as if failover.sh had never run.
network() {
  curl -sf -X PUT "$PINGMOCK_API/rules/$PRIMARY" -d "$1" >/dev/null
  curl -sf -X PUT "$PINGMOCK_API/rules/$BACKUP" -d "$2" >/dev/null
  state=$(mktemp)
}

# run runs failover.sh, leaving its output in $out and exit status in $status.
run() {
  out=$(./failover.sh -s "$state" "$PRIMARY" "$BACKUP")
  status=$?
}

# expect NAME LINK STATUS [TEXT] checks the last run: the link chosen, the
# exit status, and optionally some text in the reason.
expect() {
  local want="link=$2"
  if [[ $out == "$want "* ]] && [ "$status" = "$3" ] && [[ $out == *"${4:-}"* ]]; then
    echo "PASS $1"
    passed=$((passed + 1))
  else
    echo "FAIL $1: got \"$out\" (exit $status), want $want (exit $3)${4:+ mentioning \"$4\"}"
    failed=$((failed + 1))
  fi
}

network 20ms 40ms
run
expect "uses the primary while it is healthy" primary 0 "primary healthy"

network timeout 40ms
run
expect "switches to the backup when the primary does not answer" backup 1 "no reply"

network unreachable 40ms
run
expect "switches to the backup when the primary is unreachable" backup 1 "unreachable"

network 300ms 40ms
run
# All 3 pings are answered, so it is the latency that makes it unhealthy.
expect "switches to the backup when the primary is slow" backup 1 "primary unhealthy (3/3 replies"

# pattern= loses exactly the pings it says, on every run: one in three is
# still healthy (2 of 3 replies), two in three is not.
network "delay=20ms,pattern=..x" 40ms
run
expect "keeps a primary that loses one ping in three" primary 0 "2/3 replies"
network "delay=20ms,pattern=.xx" 40ms
run
expect "leaves a primary that loses two pings in three" backup 1 "1/3 replies"

network timeout timeout
run
expect "reports no link when both are down" none 2 "backup unhealthy"

# A scenario: the primary misses one check (3 pings), then recovers. Each
# check sends it 3 pings, so the phases line up with the runs.
network "timeout for 3 pings; 20ms" 40ms
run
expect "recovery: leaves the primary while it is down" backup 1
run
expect "recovery: waits for a second healthy check" backup 1 "recovering (1/2)"
run
expect "recovery: goes back to the primary" primary 0 "primary healthy"

# A primary that flaps between down and up, forever, never gets two
# healthy checks in a row, so the script stays on the backup.
network "timeout for 3 pings; 20ms for 3 pings; repeat" 40ms
for i in 1 2 3 4; do
  run
  expect "flapping primary: stays on the backup (check $i)" backup 1
done

# pingmock's history shows what the script really did: one check sends
# COUNT pings to the primary, and none to the backup while the primary is
# healthy.
network 20ms 40ms
curl -sf -X DELETE "$PINGMOCK_API/history" >/dev/null
run
to_primary=$(curl -sf "$PINGMOCK_API/history?target=$PRIMARY" | grep -c '"seq":' || true)
to_backup=$(curl -sf "$PINGMOCK_API/history?target=$BACKUP" | grep -c '"seq":' || true)
out="link=primary $to_primary to primary, $to_backup to backup"
status=0
expect "sends 3 pings to the primary and none to the backup" primary 0 "3 to primary, 0 to backup"

echo "$passed passed, $failed failed"
[ "$failed" -eq 0 ]
