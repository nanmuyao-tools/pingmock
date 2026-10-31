#!/usr/bin/env bash
# Decides which network link to use: the primary while it works, the
# backup when it does not, and the primary again once it has recovered.
#
# This is the code under test in this example. It knows nothing about
# ghostwire and runs the system's ping, as monitoring scripts do; test.sh
# tests it against ghostwire.
#
#   failover.sh [-s STATE_FILE] PRIMARY BACKUP
#
# It prints "link=primary", "link=backup" or "link=none" with the reason,
# and exits with 0, 1 or 2 accordingly. With -s it remembers the link in
# use between runs, and after switching to the backup it only goes back to
# the primary once the primary has been healthy RECOVER_CHECKS times in a
# row, so that a flapping primary does not make it flap too.
set -euo pipefail

COUNT=${COUNT:-3}                  # pings per check
MIN_REPLIES=${MIN_REPLIES:-2}      # fewer replies than this is unhealthy
MAX_MS=${MAX_MS:-150}              # a slower average is unhealthy
RECOVER_CHECKS=${RECOVER_CHECKS:-2}

state=""
if [ "${1:-}" = "-s" ]; then
  state=$2
  shift 2
fi
if [ $# -ne 2 ]; then
  echo "usage: failover.sh [-s STATE_FILE] PRIMARY BACKUP" >&2
  exit 64
fi
primary=$1
backup=$2

# healthy HOST succeeds if HOST answers enough pings quickly enough. Either
# way it leaves what it measured in $detail.
healthy() {
  local out replies avg
  out=$(ping -n -c "$COUNT" -i 0.2 -W 1 "$1" 2>&1 || true)
  replies=$(grep -c 'bytes from' <<<"$out" || true)
  if [ "$replies" -eq 0 ]; then
    if grep -qiE 'unreachable|prohibited|filtered' <<<"$out"; then
      detail="unreachable"
    else
      detail="no reply"
    fi
    return 1
  fi
  avg=$(grep -oE 'time=[0-9.]+' <<<"$out" | cut -d= -f2 | awk '{ s += $1 } END { printf "%.0f", s / NR }')
  detail="$replies/$COUNT replies, ${avg}ms"
  [ "$replies" -ge "$MIN_REPLIES" ] && [ "$avg" -le "$MAX_MS" ]
}

current=primary   # the link in use after the previous run
streak=0          # healthy checks of the primary since leaving it
if [ -n "$state" ] && [ -s "$state" ]; then
  read -r current streak <"$state"
fi

if healthy "$primary"; then
  primary_detail=$detail
  if [ "$current" = backup ] && [ $((streak + 1)) -lt "$RECOVER_CHECKS" ] && healthy "$backup"; then
    streak=$((streak + 1))
    link=backup
    reason="primary recovering ($streak/$RECOVER_CHECKS): $primary_detail"
  else
    link=primary
    streak=0
    reason="primary healthy: $primary_detail"
  fi
else
  primary_detail=$detail
  streak=0
  if healthy "$backup"; then
    link=backup
    reason="primary unhealthy ($primary_detail); backup: $detail"
  else
    link=none
    reason="primary unhealthy ($primary_detail); backup unhealthy ($detail)"
  fi
fi

if [ -n "$state" ]; then
  echo "$link $streak" >"$state"
fi
echo "link=$link $reason"
case $link in
  primary) exit 0 ;;
  backup) exit 1 ;;
  *) exit 2 ;;
esac
