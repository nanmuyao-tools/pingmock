#!/usr/bin/env bash
# End-to-end check: run as root on Linux with iproute2 and ping installed.
#   go build -o pingmock . && sudo ./e2e_test.sh
set -euo pipefail
cd "$(dirname "$0")"

./pingmock -rule 10.99.0.1=50ms -rule 10.99.0.2=timeout >/tmp/pingmock-e2e.log 2>&1 &
PM=$!
trap 'kill $PM 2>/dev/null || true' EXIT
for _ in $(seq 50); do curl -sf localhost:9100/rules >/dev/null && break; sleep 0.1; done

avg() { ping -c "$2" -i 0.2 -W 2 "$1" | awk -F'/' '/^rtt/ {print $5}'; }
check() { # name, actual avg ms, expected ms, tolerance ms
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    echo "PASS $1: ${2}ms"
  else
    echo "FAIL $1: got ${2}ms, want ${3}±${4}ms"; exit 1
  fi
}

check "static 50ms" "$(avg 10.99.0.1 3)" 50 5
curl -sf -X PUT localhost:9100/rules/10.99.0.1 -d 250ms >/dev/null
check "changed at runtime to 250ms" "$(avg 10.99.0.1 3)" 250 10

if ping -c 2 -W 1 10.99.0.2 >/dev/null 2>&1; then
  echo "FAIL timeout target answered"; exit 1
else
  echo "PASS timeout target gives no reply"
fi
echo "all checks passed"
