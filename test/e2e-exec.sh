#!/usr/bin/env bash
# End-to-end checks for "ghostwire exec". Run it as an unprivileged user to
# test the rootless path, and as root to test the root path:
#   go build -o ghostwire . && test/e2e-exec.sh && sudo test/e2e-exec.sh
set -euo pipefail
cd "$(dirname "$0")/.."
PM=./ghostwire

# ghostwire's own log goes to a file, printed whenever a check fails, so a
# failure in CI always shows its cause.
LOG=$(mktemp)
trap 'status=$?; echo "FAIL at line $LINENO (exit $status)"; echo "--- ghostwire log:"; tail -n 20 "$LOG"; exit $status' ERR

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; echo "--- ghostwire log:"; tail -n 20 "$LOG"; exit 1; }
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }
# Ping results are the shortest round trip of a few pings, as in e2e.sh.
near() {
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
run() { "$PM" exec -addr6 "" "$@" 2>>"$LOG"; }

if [ "$(id -u)" = 0 ]; then echo "running as root"; else echo "running as $(id -un) (rootless)"; fi

rtt=$(run -rule 10.99.0.1=45ms -- sh -c "ping -c 3 -i 0.2 10.99.0.1 | awk -F'[ /=]+' '/^rtt/ {print \$6}'")
near "ping inside exec" "$rtt" 45 5

# ghostwire exec needs no programs either: with an empty PATH it still sets
# up the namespace, and only the command needs a full path.
ping=$(command -v ping)
rtt=$(env PATH=/nonexistent "$PM" exec -addr6 "" -rule 10.99.0.1=30ms -- "$ping" -c 3 -i 0.2 10.99.0.1 2>>"$LOG" | awk -F'[ /=]+' '/^rtt/ {print $6}')
near "ghostwire exec without ip on the PATH" "$rtt" 30 5

status=0
run -- sh -c 'exit 7' || status=$?
same "exit status of the command is passed on" "$status" 7

status=0
run -- no-such-command-ghostwire || status=$?
same "missing command exits 127" "$status" 127

status=0
run -tcp 10.99.0.1:80=nonsense -- true || status=$?
same "ghostwire exec's own failure exits 125, not a status the command could have" "$status" 125
status=0
run -- sh -c 'exit 2' || status=$?
same "the command's exit status 2 is its own" "$status" 2

api=$(run -rule 10.99.0.1=10ms -- sh -c 'echo "$GHOSTWIRE_API"')
same "GHOSTWIRE_API is set for the command" "$api" "http://127.0.0.1:9100"

changed=$(run -rule 10.99.0.1=10ms -- sh -c "
  curl -sf -X PUT \"\$GHOSTWIRE_API/rules/10.99.0.1\" -d 120ms >/dev/null
  ping -c 2 -i 0.2 10.99.0.1 | awk -F'[ /=]+' '/^rtt/ {print \$6}'")
near "rules change through the API inside exec" "$changed" 120 6

# POST /reset puts the startup rules back; GET /version names the build.
reset=$(run -rule 10.99.0.1=10ms -- sh -c "
  curl -sf -X PUT \"\$GHOSTWIRE_API/rules/10.99.0.1\" -d 120ms >/dev/null
  curl -sf -X POST \"\$GHOSTWIRE_API/reset\"
  ping -c 2 -i 0.2 10.99.0.1 | awk -F'[ /=]+' '/^rtt/ {print \$6}'")
near "POST /reset restores the startup rules" "$reset" 10 5
version=$(run -- sh -c 'curl -sf "$GHOSTWIRE_API/version"' | grep -o '"version": *"[^"]*"' | cut -d'"' -f4)
same "GET /version reports the build" "$version" "$("$PM" -version | cut -d' ' -f2)"

# -host gives the command a private /etc/hosts; the system's is untouched.
before=$(md5sum /etc/hosts)
resolved=$(run -host test.example=10.99.0.1 -rule 10.99.0.1=35ms -- sh -c "
  getent hosts test.example | awk '{print \$1}'
  ping -c 2 -i 0.2 test.example | awk -F'[ /=]+' '/^rtt/ {print \$6}'")
same "-host resolves the name for the command" "$(echo "$resolved" | head -1)" "10.99.0.1"
near "-host: ping by name gets the rule's delay" "$(echo "$resolved" | tail -1)" 35 5
same "-host leaves the system's /etc/hosts alone" "$(md5sum /etc/hosts)" "$before"
same "-host leaves no file behind" "$(ls /tmp/ghostwire-hosts-* 2>/dev/null | wc -l)" 0

# nscd, where it runs, resolves on the host for every program using the C
# library: -host must win over it. Run as root, where nscd can be started.
if [ "$(id -u)" = 0 ] && command -v nscd >/dev/null; then
  started=
  if ! pgrep -x nscd >/dev/null; then mkdir -p /var/run/nscd; nscd; started=1; sleep 0.5; fi
  [ -S /var/run/nscd/socket ] || fail "nscd did not start"
  same "-host wins over the host's nscd" "$(run -host nscd-test.example=10.99.0.9 -- getent ahosts nscd-test.example | awk 'NR == 1 {print $1}')" 10.99.0.9
  [ -n "$started" ] && nscd -K
else
  echo "SKIP -host with nscd running (needs root and nscd)"
fi

# A rule for a name: the command's DNS server resolves it to an address
# from the top of the subnet, both ways, and other names still resolve
# through the host's resolver.
before_resolv=$(md5sum /etc/resolv.conf 2>/dev/null || true)
named=$(run -rule by-name.example=45ms -- sh -c "
  getent hosts by-name.example | awk '{print \$1}'
  getent hosts 10.99.0.253 | awk '{print \$2}'
  ping -c 2 -i 0.2 by-name.example | awk -F'[ /=]+' '/^rtt/ {print \$6}'
  getent hosts does-not-exist.invalid >/dev/null; echo \$?")
same "a rule for a name resolves to the top of the subnet" "$(echo "$named" | sed -n 1p)" "10.99.0.253"
same "the address resolves back to the name" "$(echo "$named" | sed -n 2p)" "by-name.example"
same "a mocked name resolves over TCP" "$(run -rule by-name.example=1ms -- sh -c "RES_OPTIONS=use-vc timeout 10 getent hosts by-name.example" | awk '{print $1}')" "10.99.0.253"
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
  # Over TCP too, which resolvers use for answers too large for UDP:
  # RES_OPTIONS=use-vc makes the C library ask over TCP only.
  forwarded=$(run -rule by-name.example=1ms -- sh -c "RES_OPTIONS=use-vc timeout 10 getent hosts github.com | awk '{print \$1}' | head -1")
  [ -n "$forwarded" ] && pass "a real name is forwarded over TCP: $forwarded" || fail "github.com did not resolve over TCP inside exec"
else
  echo "SKIP forwarding to the host's resolver (this host cannot resolve github.com)"
fi
leftover=0
for f in /tmp/ghostwire-*; do [ -e "$f" ] && [ "$f" != /tmp/ghostwire-e2e.log ] && leftover=$((leftover + 1)); done
same "no temporary file is left behind" "$leftover" 0

# TCP rules inside exec: a connection to an address is accepted after the
# delay, one to a name is refused, and a port without a rule is not
# answered. curl's time_connect is 0 for a connection never made; the
# delay is the fastest of three connections, as in e2e.sh.
tcp=$(run -tcp 10.99.0.1:5432=30ms -tcp db.example.com:5432=refuse -- bash -c "
  for _ in 1 2 3; do
    curl -s -o /dev/null --connect-timeout 0.5 -m 0.5 -w '%{time_connect}\n' http://10.99.0.1:5432/ || true
  done | sort -n | head -1
  timeout 3 bash -c ': <>/dev/tcp/db.example.com/5432' 2>&1 | sed -n 's/.*connect: //p'
  curl -s -o /dev/null --connect-timeout 0.5 -m 0.5 -w '%{time_connect}\n' http://10.99.0.1:5433/ || true")
near "TCP inside exec: accepted after the rule's delay" "$(echo "$tcp" | sed -n 1p | awk '{printf "%.1f", $1 * 1000}')" 30 5
same "TCP inside exec: a rule for a name refuses" "$(echo "$tcp" | sed -n 2p)" "Connection refused"
same "TCP inside exec: a port without a rule is not answered" "$(echo "$tcp" | sed -n 3p)" "0.000000"

# after= inside exec, on a name: curl's exit status tells an empty reply
# (52, the connection closed) from a reset while receiving (56).
tcp=$(run -tcp db.example.com:80=after=close -tcp db.example.com:81=after=reset -- bash -c "
  curl -s -o /dev/null -m 2 http://db.example.com/; echo \$?
  curl -s -o /dev/null -m 2 http://db.example.com:81/; echo \$?")
same "TCP inside exec: after=close closes the connection (curl exit 52)" "$(echo "$tcp" | sed -n 1p)" 52
same "TCP inside exec: after=reset resets it (curl exit 56)" "$(echo "$tcp" | sed -n 2p)" 56

# DNS rules inside exec. Python's getaddrinfo tells the errors apart:
# -2 (EAI_NONAME) for NXDOMAIN, -3 (EAI_AGAIN) for SERVFAIL and timeouts.
# Names end with a dot, so that the C library does not go on to try the
# host's search domains, whose answers would decide the result instead.
lookup='import socket, sys, time
for name in sys.argv[1:]:
    t = time.monotonic()
    try:
        r = socket.getaddrinfo(name, None, socket.AF_INET)[0][4][0]
    except socket.gaierror as e:
        r = "error%d" % e.errno
    print(name, r, round((time.monotonic() - t) * 1000))'
dns=$(run -host fixed.example=10.99.0.5 -rule api.example.com=5ms \
  -dns gone.example=nxdomain -dns broken.example=servfail -dns api.example.com=60ms \
  -dns 'flaky.example=nxdomain for 2 requests; 0ms' -host flaky.example=10.99.0.7 \
  -dns slow.example=timeout -- sh -c "
  RES_OPTIONS='timeout:1 attempts:1' python3 -c '$lookup' gone.example. broken.example. api.example.com. \
    flaky.example. flaky.example. flaky.example. slow.example. fixed.example.
  grep -c 'api.example.com' /etc/hosts || true
  curl -s localhost:9100/rules | tr -d ' \n' | grep -o '\"flaky.example/A\":{[^}]*}'
  curl -s 'localhost:9100/history?target=gone.example' | tr -d ' \n' | grep -o '\"result\":\"[a-z]*\"' | sort -u
  curl -s 'localhost:9100/history?target=gone.example' | tr -d ' \n' | grep -o '\"src\":\"[^\"]*\",\"dst\":\"[^\"]*\",\"id\":[0-9]*' | head -1
  curl -s 'localhost:9100/history?target=127.0.0.1' | tr -d ' \n' | grep -c '\"probe\":\"dns\"' || true")
field() { echo "$dns" | awk -v n="$1" -v f="$2" 'NR == n {print $f}'; }
same "DNS: nxdomain fails the lookup as not found (EAI_NONAME)" "$(field 1 2)" error-2
same "DNS: servfail fails it as a temporary failure (EAI_AGAIN)" "$(field 2 2)" error-3
same "DNS: a delayed name still resolves to its mocked address" "$(field 3 2)" 10.99.0.253
awk -v ms="$(field 3 3)" 'BEGIN { exit !(ms >= 55 && ms < 300) }' && pass "DNS: the lookup takes the rule's delay: $(field 3 3)ms" || fail "DNS: delayed lookup took $(field 3 3)ms, want about 60ms"
same "DNS: nxdomain for 2 requests fails two lookups, then the name resolves" "$(field 4 2) $(field 5 2) $(field 6 2)" "error-2 error-2 10.99.0.7"
same "DNS: timeout leaves the query unanswered (EAI_AGAIN after the resolver's timeout)" "$(field 7 2)" error-3
same "DNS: a name without a DNS rule resolves as before" "$(field 8 2)" 10.99.0.5
same "DNS: a name with a DNS rule is left out of the command's /etc/hosts" "$(echo "$dns" | sed -n 9p)" 0
echo "$dns" | sed -n 10p | grep -q '"requests":3,"answered":1,"nxdomain":2' && pass "DNS: statistics count the queries per name and type" || fail "DNS: stats: $(echo "$dns" | sed -n 10p)"
same "DNS: the history records the result of each query" "$(echo "$dns" | sed -n 11p)" '"result":"nxdomain"'
echo "$dns" | sed -n 12p | grep -q '^"src":"","dst":"","id":[1-9]' && pass "DNS: events have no addresses and the query's ID" || fail "DNS: event fields: $(echo "$dns" | sed -n 12p)"
same "DNS: events do not match an address filter" "$(echo "$dns" | sed -n 13p)" 0
# Rules change while the command runs, also for names that are not
# mocked (localhost.example is forwarded otherwise).
changed=$(run -dns gone.example=nxdomain -host gone.example=10.99.0.8 -- sh -c "
  curl -s -X PUT localhost:9100/dns/localhost.example -d servfail >/dev/null
  curl -s -X PUT localhost:9100/dns/gone.example -d 0ms >/dev/null
  RES_OPTIONS='timeout:1 attempts:1' python3 -c '$lookup' localhost.example. gone.example.
  curl -s -o /dev/null -w '%{http_code}\n' -X DELETE localhost:9100/dns/gone.example")
same "DNS: a rule set through the API takes effect" "$(echo "$changed" | awk 'NR == 1 {print $2}')" error-3
same "DNS: a rule changed to answer gives the mocked address again" "$(echo "$changed" | awk 'NR == 2 {print $2}')" 10.99.0.8
same "DNS: DELETE /dns removes a rule" "$(echo "$changed" | sed -n 3p)" 204

# A UDP rule for a name inside exec.
same "UDP inside exec: a rule for a name refuses" "$(run -udp syslog.example:514=refuse -- python3 -c '
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(0.5)
s.connect((socket.gethostbyname("syslog.example."), 514)); s.send(b"x")
try:
    s.recv(10)
except OSError as e:
    print(type(e).__name__)')" ConnectionRefusedError

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
run -- sleep 1 &
bg=$!
sleep 0.5
if ip link show ghostwire0 >/dev/null 2>&1; then fail "a ghostwire0 device appeared on the host"; fi
wait "$bg"
pass "the host network is untouched"

# Parallel runs are isolated: same device name and API port, different rules.
out=$(mktemp -d)
for d in 20 50 80; do
  run -rule 10.99.0.1=${d}ms -- sh -c "sleep 0.3; ping -c 2 -i 0.2 10.99.0.1 | awk -F'[ /=]+' '/^rtt/ {print \$6}'" >"$out/$d" &
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

# Routes follow the rules: once a real address's rule is deleted, the
# command's network is as before it had one, here with no way out.
same "a deleted rule's address is unreachable again, as before the rule" "$(run -rule 8.8.8.8=5ms -- bash -c "
  ping -c 1 -W 1 8.8.8.8 >/dev/null && echo -n 'answered, '
  curl -sf -X DELETE \"\$GHOSTWIRE_API/rules/8.8.8.8\" >/dev/null
  ping -c 1 -W 1 8.8.8.8 2>&1 | grep -o 'Network is unreachable' | head -1")" "answered, Network is unreachable"

# Outside access, through pasta: the command reaches a service on the
# host's loopback with -outside-port, and only with it, while mocked
# targets keep their rules and the subnet's unmocked ports stay silent.
# None of it needs the internet. Without pasta, exec fails as itself, and
# the checks are skipped, unless REQUIRE_OUTSIDE=1 (as in CI) makes that a
# failure.
same "-outside without pasta fails as ghostwire exec itself (125)" \
  "$(PATH=/nonexistent "$PM" exec -addr6 "" -outside -- true 2>>"$LOG"; echo $?)" 125
if command -v pasta >/dev/null; then
  python3 -m http.server 18765 --bind 127.0.0.1 -d /tmp >/dev/null 2>&1 &
  srv=$!
  for _ in $(seq 50); do curl -s -o /dev/null http://127.0.0.1:18765/ && break; sleep 0.1; done
  inside=$(run -outside-port 18765 -rule 10.99.0.1=100ms -- bash -c "
    curl -s -o /dev/null -m 2 -w '%{http_code}\n' http://127.0.0.1:18765/ || true
    ping -c 3 -i 0.2 10.99.0.1 | awk -F'[ /=]+' '/^rtt/ {print \$6}'
    curl -s -o /dev/null --connect-timeout 0.5 -m 0.5 -w '%{time_connect}\n' http://10.99.0.9/ || true
    curl -s \"\$GHOSTWIRE_API/rules\" | python3 -c 'import json, sys; print(json.load(sys.stdin)[\"outside\"][\"ports\"])'")
  same "-outside-port reaches a service on the host's loopback" "$(sed -n 1p <<<"$inside")" 200
  near "a mocked target keeps its rule with outside access" "$(sed -n 2p <<<"$inside")" 100 5
  same "an unmocked port of the subnet is not answered with outside access" "$(sed -n 3p <<<"$inside")" "0.000000"
  same "GET /rules shows the outside access" "$(sed -n 4p <<<"$inside")" "['18765/tcp']"
  # A real address mocked through the API goes back to pasta's route once
  # its rule is deleted.
  # pasta's device takes the name of the host's, so only ghostwire0 is known.
  devs=$(run -outside -- bash -c "
    curl -sf -X PUT \"\$GHOSTWIRE_API/tcp/1.1.1.1:80\" -d refuse >/dev/null
    ip route get 1.1.1.1 | grep -o 'dev [^ ]*' | head -1
    curl -sf -X DELETE \"\$GHOSTWIRE_API/tcp/1.1.1.1:80\" >/dev/null
    ip route get 1.1.1.1 | grep -o 'dev [^ ]*' | head -1")
  same "with outside access, a mocked real address goes to ghostwire" "$(sed -n 1p <<<"$devs")" "dev ghostwire0"
  same "and once its rule is deleted, out through pasta again" "$(sed -n 2p <<<"$devs" | grep -c -v -e ghostwire0 -e '^$' || true)" 1
  same "without -outside-port, the host's loopback is out of reach" \
    "$(run -outside -- curl -s -o /dev/null -m 2 -w '%{http_code}' http://127.0.0.1:18765/ || true)" 000
  kill "$srv" 2>/dev/null || true
  # Pasta forwards nothing to the host: the command's API stays its own.
  # With its defaults, pasta would publish the ports the command listens
  # on, which it scans for again every second or so.
  run -outside -- sleep 3 &
  bg=$!
  sleep 2.5
  same "the command's API is not published on the host" "$(ss -Hltn 'sport = :9100' | wc -l)" 0
  wait "$bg"
  sleep 1.5
  same "pasta stops with the command" "$(pgrep -c -x pasta || true)" 0
elif [ "${REQUIRE_OUTSIDE:-0}" = 1 ]; then
  fail "pasta (package passt) is not installed, but REQUIRE_OUTSIDE=1"
else
  echo "SKIP outside access: pasta (package passt) is not installed"
fi

rm -f "$LOG"
echo "all exec checks passed"
