#!/usr/bin/env bash
# Checks the container image and the Compose examples. Needs Docker:
#   test/e2e-docker.sh
set -euo pipefail
cd "$(dirname "$0")/.."
IMAGE=pingmock:e2e
NAME=pingmock-e2e-$$

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; echo "--- container log:"; docker logs "$NAME" 2>&1 | tail -n 20 || true; exit 1; }
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }
near() {
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
DEMO=examples/compose-demo/compose.yaml
cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  PINGMOCK_IMAGE=$IMAGE docker compose -f examples/docker-compose.yml down >/dev/null 2>&1 || true
  PINGMOCK_IMAGE=$IMAGE docker compose -f "$DEMO" --profile tests down >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -q -t "$IMAGE" --build-arg VERSION=e2e . >/dev/null
same "the image reports its version" "$(docker run --rm "$IMAGE" -version)" "pingmock e2e"

docker run -d --name "$NAME" --cap-add NET_ADMIN --device /dev/net/tun "$IMAGE" \
  -rule 10.99.0.1=40ms -rule 10.99.0.2=unreachable >/dev/null
for _ in $(seq 50); do
  docker exec "$NAME" wget -q -O /dev/null http://127.0.0.1:9100/rules 2>/dev/null && break
  sleep 0.2
done
docker exec "$NAME" wget -q -O /dev/null http://127.0.0.1:9100/rules || fail "the API did not come up"
pass "pingmock starts in a container"

# A second container sharing pingmock's network gets its pings mocked.
peer() { docker run --rm --network "container:$NAME" --entrypoint "$1" "$IMAGE" "${@:2}"; }
avg=$(peer ping -c 3 -i 0.2 10.99.0.1 | awk -F/ '/^rtt/ {print $5}')
near "ping from a container sharing the network" "$avg" 40 5
errors=$(peer ping -c 2 -i 0.2 -W 2 10.99.0.2 2>/dev/null | grep -c 'Destination Host Unreachable' || true)
same "unreachable answers in a container" "$errors" 2
seed=$(peer wget -q -O - http://127.0.0.1:9100/rules | grep -c '"seed"' || true)
same "the API is reachable from the sharing container" "$seed" 1
docker rm -f "$NAME" >/dev/null

# The Compose example, with the image just built.
status=0
PINGMOCK_IMAGE=$IMAGE docker compose -f examples/docker-compose.yml up \
  --abort-on-container-exit --exit-code-from app >/tmp/compose.log 2>&1 || status=$?
if [ "$status" != 0 ]; then echo "--- compose log:"; tail -n 30 /tmp/compose.log; fi
same "the Compose example runs the app against pingmock" "$status" 0
grep -q 'time=5[0-9]' /tmp/compose.log && pass "the Compose app sees the 50ms rule" || fail "no 50ms replies in the Compose output"

# examples/compose-demo: the tests on their own, then the monitor reacting
# to a rule changed from the host, as its README shows.
demo() { PINGMOCK_IMAGE=$IMAGE docker compose -f "$DEMO" "$@"; }
demo_fail() { echo "FAIL $1"; demo --profile tests logs 2>&1 | tail -n 30; exit 1; }
status=0
demo run --rm --build tests >/tmp/compose-demo-tests.log 2>&1 || status=$?
tail -n 1 /tmp/compose-demo-tests.log | grep -qE '^[0-9]+ passed, 0 failed$' && [ "$status" = 0 ] ||
  { cat /tmp/compose-demo-tests.log; demo_fail "compose-demo tests"; }
pass "compose-demo: $(tail -n 1 /tmp/compose-demo-tests.log)"
demo --profile tests down >/dev/null 2>&1

demo up -d --build monitor >/dev/null 2>&1 || demo_fail "compose-demo: monitor did not start"
for _ in $(seq 30); do demo logs monitor 2>/dev/null | grep -q 'link=primary' && break; sleep 1; done
demo logs monitor 2>/dev/null | grep -q 'link=primary' || demo_fail "compose-demo: the monitor never chose the primary"
pass "compose-demo: the monitor uses the primary"
curl -sf -X PUT localhost:9100/rules/10.99.0.1 -d timeout >/dev/null || demo_fail "compose-demo: the API is not published on localhost:9100"
for _ in $(seq 30); do demo logs --tail 1 monitor 2>/dev/null | grep -q 'link=backup' && break; sleep 1; done
demo logs --tail 1 monitor 2>/dev/null | grep -q 'link=backup' || demo_fail "compose-demo: the monitor did not switch to the backup"
pass "compose-demo: a rule set from the host makes the monitor switch to the backup"
curl -sf -X PUT localhost:9100/rules/10.99.0.1 -d 20ms >/dev/null
for _ in $(seq 30); do demo logs --tail 1 monitor 2>/dev/null | grep -q 'link=primary' && break; sleep 1; done
log=$(demo logs monitor 2>/dev/null)
after_backup=$(printf '%s\n' "$log" | sed -n '/link=backup primary unhealthy/,$p')
printf '%s\n' "$after_backup" | grep -q 'link=backup primary recovering (1/2)' &&
  printf '%s\n' "$after_backup" | tail -n 1 | grep -q 'link=primary primary healthy' ||
  { printf '%s\n' "$log" | tail -n 12; demo_fail "compose-demo: the monitor did not recover through 'recovering (1/2)'"; }
pass "compose-demo: when the primary comes back, the monitor waits one check, then returns to it"
echo "--- the monitor's log around the switch (as in the README):"
printf '%s\n' "$log" | grep -m 1 -B 1 'link=backup primary unhealthy'
printf '%s\n' "$after_backup" | grep -A 1 'recovering (1/2)' | head -n 2
demo down >/dev/null 2>&1

echo "all docker checks passed"
