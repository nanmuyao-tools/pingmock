#!/usr/bin/env bash
# Checks the container image and the Compose example. Needs Docker:
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
cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  PINGMOCK_IMAGE=$IMAGE docker compose -f examples/docker-compose.yml down >/dev/null 2>&1 || true
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

echo "all docker checks passed"
