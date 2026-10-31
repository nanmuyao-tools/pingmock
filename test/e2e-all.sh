#!/usr/bin/env bash
# Runs the end-to-end checks at the same time: e2e.sh, e2e-compat.sh, and
# e2e-exec.sh rootless and then as root. Run as a user who can sudo, on
# Linux with everything the three scripts need:
#   make e2e-all
# e2e.sh and e2e-compat.sh both run ghostwire as root, with the device
# ghostwire0, routes for 10.99.0.0/24 and the API on port 9100, so each gets
# a network namespace of its own; e2e-exec.sh checks that the host has no
# ghostwire0, which they would otherwise break. The two runs of e2e-exec.sh
# share the host's nscd, so they stay in order. Each script writes to its
# own log, printed when it fails.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

LOGS=$(mktemp -d)
trap 'rm -rf "$LOGS"' EXIT

# A new network namespace has its loopback down and its own sysctls, among
# them ping_group_range, which e2e-compat.sh may have to write: /proc/sys is
# remounted writable in a mount namespace of its own, since containers mount
# it read only. The namespace has no way out, so e2e-compat.sh builds its
# helpers from the module cache, downloaded here first.
isolated() {
  sudo env "PATH=$PATH" GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off unshare --net --mount bash -c \
    'mount -o remount,rw /proc/sys 2>/dev/null; ip link set lo up && exec "$@"' _ "$@"
}
(cd test/compat && go mod download) || exit 1

start=$(date +%s)
isolated env REQUIRE_IPV6="${REQUIRE_IPV6:-}" test/e2e.sh >"$LOGS/e2e" 2>&1 &
e2e=$!
isolated test/e2e-compat.sh >"$LOGS/e2e-compat" 2>&1 &
compat=$!
{ test/e2e-exec.sh && sudo test/e2e-exec.sh; } >"$LOGS/e2e-exec" 2>&1 &
exec=$!

failed=0
for name in e2e e2e-compat e2e-exec; do
  case $name in
  e2e) pid=$e2e ;;
  e2e-compat) pid=$compat ;;
  e2e-exec) pid=$exec ;;
  esac
  if wait "$pid"; then
    echo "ok   $name: $(grep -c '^PASS' "$LOGS/$name") checks passed"
  else
    echo "FAIL $name:"
    sed 's/^/    /' "$LOGS/$name"
    failed=1
  fi
done
echo "$(( $(date +%s) - start ))s"
exit "$failed"
