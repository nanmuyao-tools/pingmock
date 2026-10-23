#!/usr/bin/env bash
# End-to-end check: run as root on Linux with iproute2, ping and curl.
#   make e2e    (or: go build -o pingmock . && sudo test/e2e.sh)
# The IPv6 checks are skipped when the kernel has no IPv6, unless
# REQUIRE_IPV6=1 is set (as in CI), which makes that a failure.
set -euo pipefail
cd "$(dirname "$0")/.."

PM=""
start() { # args passed to pingmock
  ./pingmock "$@" >/tmp/pingmock-e2e.log 2>&1 &
  PM=$!
  for _ in $(seq 50); do curl -sf localhost:9100/rules >/dev/null && return; sleep 0.1; done
  echo "FAIL pingmock did not start:"; cat /tmp/pingmock-e2e.log; exit 1
}
stop() { kill "$PM" 2>/dev/null || true; wait "$PM" 2>/dev/null || true; PM=""; }
trap stop EXIT

avg() { ping -c "$2" -i 0.2 -W 2 "$1" | awk -F'/' '/^rtt/ {print $5}'; }
# Sequence numbers of the echo replies (not of error messages) ping received.
received() { ping -c "$2" -i 0.05 -W 1 "$1" 2>/dev/null | grep 'bytes from' | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true; }
pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; exit 1; }
near() { # name, actual avg ms, expected ms, tolerance ms
  if awk -v a="$2" -v e="$3" -v t="$4" 'BEGIN { exit !(a >= e - t && a <= e + t) }'; then
    pass "$1: ${2}ms"
  else
    fail "$1: got ${2}ms, want ${3}±${4}ms"
  fi
}
same() { [ "$2" = "$3" ] && pass "$1" || fail "$1: got '$2', want '$3'"; }

start -seed 1 -rule 10.99.0.1=50ms -rule 10.99.0.2=timeout \
  -rule '10.99.0.3=delay=1ms,pattern=..x' -rule '10.99.0.4=10ms for 2 pings; 120ms'

near "static 50ms" "$(avg 10.99.0.1 3)" 50 5
curl -sf -X PUT localhost:9100/rules/10.99.0.1 -d 250ms >/dev/null
near "changed at runtime to 250ms" "$(avg 10.99.0.1 3)" 250 10

if ping -c 2 -i 0.2 -W 1 10.99.0.2 >/dev/null 2>&1; then fail "timeout target answered"; fi
pass "timeout target gives no reply"

same "pattern ..x drops every third request" "$(received 10.99.0.3 9)" "1 2 4 5 7 8 "

near "count phase 1 (10ms for 2 pings)" "$(avg 10.99.0.4 2)" 10 3
near "count phase 2 (120ms afterwards)" "$(avg 10.99.0.4 2)" 120 5

events=$(curl -sf "localhost:9100/history?target=10.99.0.3" | grep -c '"seq":' || true)
same "history records every request" "$events" 9

# ttl= sets the TTL of the replies; without it, 64.
curl -sf -X PUT localhost:9100/rules/10.99.0.5 -d 'delay=1ms,ttl=128' >/dev/null
ttl() { ping -c 1 -W 2 "$1" | grep -o 'ttl=[0-9]*' | head -1; }
same "ttl=128 shows in ping" "$(ttl 10.99.0.5)" "ttl=128"
same "the default TTL is 64" "$(ttl 10.99.0.1)" "ttl=64"
stop

# -host is for exec only: the server mode refuses it instead of ignoring it.
status=0
./pingmock -host test.example=10.99.0.1 >/dev/null 2>&1 || status=$?
same "-host is refused outside pingmock exec" "$status" 2
status=0
./pingmock -tcp db.example.com:5432=refuse >/dev/null 2>&1 || status=$?
same "a TCP rule for a name is refused outside pingmock exec" "$status" 2

# Destination unreachable: ping must report each code, and fail fast.
start -rule 10.99.0.8=unreachable -rule 10.99.0.9=unreachable=net \
  -rule 10.99.0.10=unreachable=admin -rule '10.99.0.11=delay=5ms,pattern=..u'
errors() { ping -c "$2" -i 0.2 -W 2 "$1" 2>/dev/null | grep -c "$3" || true; }
same "unreachable reports host unreachable" "$(errors 10.99.0.8 2 'Destination Host Unreachable')" 2
same "unreachable=net reports net unreachable" "$(errors 10.99.0.9 2 'Destination Net Unreachable')" 2
same "unreachable=admin reports packet filtered" "$(errors 10.99.0.10 2 'Packet filtered')" 2
same "pattern ..u answers every third with unreachable" "$(received 10.99.0.11 6)" "1 2 4 5 "
start_ns=$(date +%s%N)
ping -c 1 -W 3 10.99.0.8 >/dev/null 2>&1 || true
elapsed_ms=$(( ($(date +%s%N) - start_ns) / 1000000 ))
if [ "$elapsed_ms" -lt 1000 ]; then pass "unreachable fails fast (${elapsed_ms}ms, timeout 3s)"; else fail "unreachable took ${elapsed_ms}ms"; fi
stop

# The same seed must reproduce the same random losses across runs.
start -seed 1234 -rule 10.99.0.5=loss=50%
first=$(received 10.99.0.5 20)
stop
start -seed 1234 -rule 10.99.0.5=loss=50%
second=$(received 10.99.0.5 20)
stop
same "same seed reproduces random loss ($first)" "$second" "$first"

# A configuration file, with a comment, a bare number of milliseconds and
# a flag overriding one of its rules.
cfg=$(mktemp --suffix=.yaml)
printf 'seed: 3\nrules:\n  10.99.0.6: 30   # milliseconds\n  10.99.0.7: timeout\n  10.99.0.8: unreachable\n' >"$cfg"
start -config "$cfg" -rule 10.99.0.7=60ms
near "rule from the config file" "$(avg 10.99.0.6 2)" 30 4
near "flag overrides the config file" "$(avg 10.99.0.7 2)" 60 5
same "unreachable rule from the config file" "$(ping -c 1 -W 2 10.99.0.8 2>/dev/null | grep -c 'Destination Host Unreachable' || true)" 1
stop
rm -f "$cfg"

# IPv6.
if [ ! -e /proc/net/if_inet6 ]; then
  if [ "${REQUIRE_IPV6:-0}" = 1 ]; then fail "IPv6 is unavailable but REQUIRE_IPV6=1"; fi
  echo "SKIP IPv6 checks: the kernel has no IPv6"
else
  start -addr6 fd99::fe/64 -rule fd99::1=40ms -rule fd99::2=unreachable \
    -rule fd99::3=unreachable=net -rule fd99::4=unreachable=admin \
    -rule 'fd99::5=delay=5ms,pattern=..x' -rule 2001:db8::8888=25ms
  avg6() { ping -6 -c "$2" -i 0.2 -W 2 "$1" | awk -F'/' '/^rtt/ {print $5}'; }
  near "IPv6 delay 40ms" "$(avg6 fd99::1 3)" 40 5
  near "IPv6 real address mocked through a route" "$(avg6 2001:db8::8888 2)" 25 5
  errors6() { ping -6 -c 2 -i 0.2 -W 2 "$1" 2>/dev/null | grep -c "$2" || true; }
  same "IPv6 unreachable reports address unreachable" "$(errors6 fd99::2 'Destination unreachable: Address unreachable')" 2
  same "IPv6 unreachable=net reports no route" "$(errors6 fd99::3 'Destination unreachable: No route')" 2
  same "IPv6 unreachable=admin reports administratively prohibited" "$(errors6 fd99::4 'Destination unreachable: Administratively prohibited')" 2
  got6=$(ping -6 -c 6 -i 0.05 -W 1 fd99::5 2>/dev/null | grep 'bytes from' | grep -o 'icmp_seq=[0-9]*' | cut -d= -f2 | tr '\n' ' ' || true)
  same "IPv6 pattern ..x" "$got6" "1 2 4 5 "
  events=$(curl -sf "localhost:9100/history?target=fd99::/64" | grep -c '"dst": "fd99::' || true)
  [ "$events" -gt 0 ] && pass "history filters IPv6 prefixes ($events events)" || fail "no IPv6 events in history"
  stop
fi

# A network the host is on: pingmock must take its traffic without replacing
# the host's route for it, which would cut the host off from that network,
# even after pingmock exits. The device pmhost0 stands for such a network.
ip tuntap add dev pmhost0 mode tun
trap 'stop; ip link del pmhost0 2>/dev/null || true' EXIT
ip addr add 192.168.77.5/24 dev pmhost0
ip link set pmhost0 up
host_route() { ip -o "$1" route show exact "$2" table main | grep -c 'dev pmhost0' || true; }
start -rule 192.168.77.0/24=60ms
near "a network the host is on is mocked" "$(avg 192.168.77.9 2)" 60 5
same "the host's route for it stays while pingmock runs" "$(host_route -4 192.168.77.0/24)" 1
grep -q "leaving the host's route for 192.168.77.0/24 (dev pmhost0) in place" /tmp/pingmock-e2e.log &&
  pass "pingmock logs that it left the route in place" || fail "no log line about the route left in place"
stop
same "the host's route for it stays after pingmock exits" "$(host_route -4 192.168.77.0/24)" 1

# A single address with a route of its own cannot be split around: pingmock
# refuses to start rather than replace that route.
ip route add 192.168.77.99/32 dev pmhost0
if ./pingmock -addr6= -api= -rule 192.168.77.99=5ms >/tmp/pingmock-e2e.log 2>&1; then
  fail "pingmock started although 192.168.77.99 has a route of its own"
fi
grep -q "use pingmock exec" /tmp/pingmock-e2e.log && pass "an address with a route of its own is refused" ||
  { cat /tmp/pingmock-e2e.log; fail "the refusal does not suggest pingmock exec"; }
same "and its route stays" "$(host_route -4 192.168.77.99/32)" 1

if [ -e /proc/net/if_inet6 ]; then
  ip -6 addr add fd77::5/64 dev pmhost0 nodad
  start -rule fd77::/64=60ms
  near "IPv6: a network the host is on is mocked" "$(avg6 fd77::9 2)" 60 5
  same "IPv6: the host's route for it stays while pingmock runs" "$(host_route -6 fd77::/64)" 1
  stop
  same "IPv6: the host's route for it stays after pingmock exits" "$(host_route -6 fd77::/64)" 1
fi
ip link del pmhost0

# Paths: traceroute and mtr see the hops, in every probe mode, and reach
# the target; a hop written * never answers.
# TCP rules: connection attempts to an address and port are accepted after
# a delay, refused, left unanswered or answered as unreachable. curl
# reports how the connect went: exit 7 for refused or unreachable, 28 for
# a timeout, and the time the connection took.
# A connection that was never made has a time_connect of 0; the exit status
# alone cannot tell, since a connection that is made but never answered
# also ends in a timeout (28). The time limit, in seconds, is for the
# whole transfer, so an accepted connection that is never answered costs
# only that: the checks give a few times the expected delay.
connect() { # url, time limit: prints "exit time_connect"
  local out status=0
  out=$(curl -s -o /dev/null --connect-timeout "$2" -m "$2" -w '%{time_connect}' "$1" 2>/dev/null) || status=$?
  echo "$status ${out:-0}"
}
ms() { awk -v s="$1" 'BEGIN { printf "%.1f", s * 1000 }'; }
start -seed 1 -tcp 10.99.0.21:80=200ms -tcp 10.99.0.22:80=refuse -tcp 10.99.0.23:80=timeout \
  -tcp 10.99.0.24:80=unreachable -tcp '10.99.0.25:80=timeout for 1 requests; 10ms' \
  -tcp '10.99.0.26:80=refuse for 1 requests; 10ms'
read -r status took <<<"$(connect http://10.99.0.21/ 0.5)"
near "TCP: the connection is accepted after the rule's delay" "$(ms "$took")" 200 10
read -r _ took <<<"$(connect http://10.99.0.21:81/ 0.5)"
same "TCP: a port without a rule is not answered (no connection)" "$(ms "$took")" 0.0
read -r status _ <<<"$(connect http://10.99.0.22/ 2)"
same "TCP: refuse refuses the connection (curl exit 7)" "$status" 7
# bash's /dev/tcp prints the system's error, which tells the two apart.
tcperr() { timeout 3 bash -c ": <>/dev/tcp/$1/$2" 2>&1 | sed -n 's/.*connect: //p'; }
same "TCP: refuse gives connection refused" "$(tcperr 10.99.0.22 80)" "Connection refused"
same "TCP: unreachable gives no route to host" "$(tcperr 10.99.0.24 80)" "No route to host"
read -r _ took <<<"$(connect http://10.99.0.23/ 0.5)"
same "TCP: timeout leaves the connection unanswered (no connection)" "$(ms "$took")" 0.0
read -r status _ <<<"$(connect http://10.99.0.24/ 2)"
same "TCP: unreachable fails the connection (curl exit 7)" "$status" 7
# The first attempt times out although the kernel sends its SYN again after
# about a second: a retransmitted SYN is the same request. The second
# attempt is the next phase and connects.
read -r _ took <<<"$(connect http://10.99.0.25/ 1.5)"
same "TCP: a retransmitted SYN is not a new request (the first attempt never connects)" "$(ms "$took")" 0.0
read -r status took <<<"$(connect http://10.99.0.25/ 0.5)"
near "TCP: the second attempt gets the next phase" "$(ms "$took")" 10 5
read -r status _ <<<"$(connect http://10.99.0.26/ 2)"
same "TCP: refuse for 1 requests refuses the first attempt" "$status" 7
read -r _ took <<<"$(connect http://10.99.0.26/ 0.5)"
near "TCP: then the next phase accepts" "$(ms "$took")" 10 5
stats=$(curl -sf localhost:9100/rules | tr -d ' \n' | grep -o '"10.99.0.25:80":{[^}]*}')
echo "$stats" | grep -q '"requests":2,"accepted":1,"refused":0,"dropped":1' || fail "TCP: stats count attempts, not SYNs: $stats"
pass "TCP: statistics count attempts, not SYNs"
events=$(curl -sf "localhost:9100/history?target=10.99.0.25" | tr -d ' \n')
echo "$events" | grep -q '"repeat":true' && echo "$events" | grep -q '"port":80' || fail "TCP: history lacks port or repeat: $events"
pass "TCP: the history records the port and the retransmitted SYNs"
# after=: what happens once the connection is accepted. curl sends its
# request and gets an empty reply (exit 52) when pingmock closes, a reset
# while receiving (56) when it resets, and every attempt gets the same.
curl -sf -X PUT localhost:9100/tcp/10.99.0.27:80 -d 5ms,after=close >/dev/null
curl -sf -X PUT localhost:9100/tcp/10.99.0.28:80 -d after=reset >/dev/null
exits() { for _ in 1 2 3 4 5; do curl -s -o /dev/null -m 2 "$1"; echo -n "$? "; done; }
same "TCP: after=close closes every connection (curl exit 52)" "$(exits http://10.99.0.27/)" "52 52 52 52 52 "
same "TCP: after=reset resets every connection (curl exit 56)" "$(exits http://10.99.0.28/)" "56 56 56 56 56 "
# Data written after pingmock's FIN gets a reset, as from a real server that
# closed its socket, and no socket is left waiting for pingmock's ACK
# (under after=hang, the default, a client that gives up is left waiting).
same "TCP: after=close, then a write is refused (EPIPE)" "$(python3 -c '
import socket, time
s = socket.create_connection(("10.99.0.27", 80), timeout=2)
s.sendall(b"x"); s.recv(1); time.sleep(0.2)
try:
    s.sendall(b"y"); print("written")
except BrokenPipeError:
    print("EPIPE")
')" EPIPE
# A client that closes without writing gets its FIN acknowledged.
for _ in 1 2 3; do timeout 2 bash -c ': <>/dev/tcp/10.99.0.27/80'; done
sleep 0.2
same "TCP: no connection is left half closed" "$(ss -Htan exclude time-wait '( dst 10.99.0.27 or dst 10.99.0.28 )' | wc -l)" 0
stats=$(curl -sf localhost:9100/rules | tr -d ' \n')
echo "$stats" | grep -q '"10.99.0.27:80":{"requests":9,"accepted":9,[^}]*"closed":9,"reset":0' &&
  echo "$stats" | grep -q '"10.99.0.28:80":{"requests":5,"accepted":5,[^}]*"closed":0,"reset":5' ||
  fail "TCP: closed and reset counts: $stats"
pass "TCP: statistics count closed and reset connections"
events=$(curl -sf "localhost:9100/history?target=10.99.0.28" | tr -d ' \n')
[ "$(grep -o '"result":"reset"' <<<"$events" | wc -l)" -eq 5 ] || fail "TCP: history of resets: $events"
pass "TCP: the history records each reset once"
# Data on the connection: a banner sent at once, a reply to the request,
# and a limit on what the client may send.
curl -sf -X PUT localhost:9100/tcp/10.99.0.29:22 -d 'send="SSH-2.0-OpenSSH_9.6\r\n"' >/dev/null
curl -sf -X PUT localhost:9100/tcp/10.99.0.30:80 -d 'reply="HTTP/1.1 503 Service Unavailable\r\nContent-Length: 4\r\n\r\ndown",after=close' >/dev/null
curl -sf -X PUT localhost:9100/tcp/10.99.0.31:80 -d 'limit=1000,after=reset' >/dev/null
curl -sf -X PUT localhost:9100/tcp/10.99.0.32:80 -d 'wait=500ms,reply="HTTP/1.0 200 OK\r\n\r\nhi",after=close' >/dev/null
same "TCP: send= is the first thing the client reads" "$(python3 -c '
import socket
print(socket.create_connection(("10.99.0.29", 22), timeout=2).recv(100).decode().strip())')" "SSH-2.0-OpenSSH_9.6"
same "TCP: reply= answers the request (curl gets the status and body)" "$(curl -s -m 2 -w ' %{http_code}' http://10.99.0.30/)" "down 503"
same "TCP: limit= resets the connection once the client sent that much" "$(python3 -c '
import socket, time
s = socket.create_connection(("10.99.0.31", 80), timeout=2)
try:
    for _ in range(10):
        s.sendall(b"x" * 500); time.sleep(0.05)
    s.recv(1); print("no error")
except OSError as e:
    print(type(e).__name__)')" ConnectionResetError
# The connection's own count of retransmitted segments (tcpi_total_retrans,
# at byte 100 of struct tcp_info): the system-wide count also has the
# retransmissions of earlier connections that pingmock left hanging.
read -r took retrans <<<"$(python3 -c '
import socket, struct, time
start = time.monotonic()
s = socket.create_connection(("10.99.0.32", 80), timeout=3)
s.sendall(b"GET / HTTP/1.0\r\n\r\n")
s.recv(100)
info = s.getsockopt(socket.IPPROTO_TCP, socket.TCP_INFO, 104)
print(time.monotonic() - start, struct.unpack_from("I", info, 100)[0])')"
near "TCP: wait= delays the reply" "$(ms "$took")" 500 60
same "TCP: and the client does not send its request again while waiting" "$retrans" 0
# UDP rules: a connected UDP socket learns of the ICMP error on its next
# receive, so the program sees "connection refused", "no route to host" or
# nothing at all.
curl -sf -X PUT localhost:9100/udp/10.99.0.33:514 -d refuse >/dev/null
curl -sf -X PUT localhost:9100/udp/10.99.0.34:123 -d unreachable=admin >/dev/null
curl -sf -X PUT localhost:9100/udp/10.99.0.36:123 -d unreachable >/dev/null
curl -sf -X PUT localhost:9100/udp/10.99.0.35:53 -d 'timeout for 1 request; refuse' >/dev/null
udp() { python3 -c '
import socket, sys
s = socket.socket(socket.AF_INET6 if ":" in sys.argv[1] else socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(0.5)
s.connect((sys.argv[1], int(sys.argv[2])))
s.send(b"hello")
try:
    s.recv(100); print("answer")
except OSError as e:
    print(type(e).__name__, e.errno or "")' "$@"; }
same "UDP: refuse is ICMP port unreachable (connection refused)" "$(udp 10.99.0.33 514)" "ConnectionRefusedError 111"
same "UDP: unreachable=admin is no route to host" "$(udp 10.99.0.34 123)" "OSError 113"
# Linux tells a UDP socket only of errors it counts as hard: host and
# network unreachable reach it only with IP_RECVERR.
same "UDP: unreachable (host) is not reported to a plain socket" "$(udp 10.99.0.36 123 | cut -d' ' -f1)" TimeoutError
same "UDP: timeout leaves the datagram unanswered, then the next phase refuses" "$(udp 10.99.0.35 53 | cut -d' ' -f1) $(udp 10.99.0.35 53 | cut -d' ' -f1)" "TimeoutError ConnectionRefusedError"
same "UDP: a port without a rule is not answered" "$(udp 10.99.0.33 515 | cut -d' ' -f1)" TimeoutError
stats=$(curl -sf localhost:9100/rules | tr -d ' \n' | grep -o '"10.99.0.35:53":{[^}]*}')
echo "$stats" | grep -q '"requests":2,"refused":1,"dropped":1' && pass "UDP: statistics count datagrams" || fail "UDP: stats: $stats"
# Rules change while running, through the API.
curl -sf -X PUT localhost:9100/tcp/10.99.0.21:80 -d refuse >/dev/null
read -r status _ <<<"$(connect http://10.99.0.21/ 2)"
same "TCP: a rule set through the API takes effect" "$status" 7
if [ -e /proc/net/if_inet6 ]; then
  curl -sf -X PUT 'localhost:9100/tcp/%5Bfd99::21%5D:80' -d 30ms >/dev/null
  read -r status took <<<"$(connect 'http://[fd99::21]/' 0.5)"
  near "TCP over IPv6: accepted after the delay" "$(ms "$took")" 30 5
  curl -sf -X PUT 'localhost:9100/tcp/%5Bfd99::22%5D:80' -d refuse >/dev/null
  read -r status _ <<<"$(connect 'http://[fd99::22]/' 2)"
  same "TCP over IPv6: refused" "$status" 7
  curl -sf -X PUT 'localhost:9100/tcp/%5Bfd99::27%5D:80' -d after=close >/dev/null
  curl -sf -X PUT 'localhost:9100/tcp/%5Bfd99::28%5D:80' -d after=reset >/dev/null
  curl -sf -X PUT 'localhost:9100/tcp/%5Bfd99::30%5D:80' -d 'reply="HTTP/1.1 503 x\r\nContent-Length: 2\r\n\r\nno",after=close' >/dev/null
  curl -sf -X PUT 'localhost:9100/udp/%5Bfd99::33%5D:514' -d refuse >/dev/null
  same "UDP over IPv6: refuse is port unreachable" "$(udp fd99::33 514)" "ConnectionRefusedError 111"
  same "TCP over IPv6: reply= answers the request" "$(curl -s -m 2 -w ' %{http_code}' 'http://[fd99::30]/')" "no 503"
  same "TCP over IPv6: after=close and after=reset (curl exits 52, 56)" \
    "$(exits 'http://[fd99::27]/' | cut -d' ' -f1) $(exits 'http://[fd99::28]/' | cut -d' ' -f1)" "52 56"
fi
stop

if command -v traceroute >/dev/null; then
  start -seed 1 -rule 10.99.0.80=30ms -path '10.99.0.80=192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms'
  hops() { traceroute "$@" -n -q 1 -w 0.5 -m 8 10.99.0.80 2>/dev/null | tail -n +2 | awk '{print $2}' | tr '\n' ' '; }
  want="192.168.1.1 10.0.0.1 * 72.14.0.1 10.99.0.80 "
  same "traceroute (UDP) shows the path" "$(hops)" "$want"
  same "traceroute -I shows the path" "$(hops -I)" "$want"
  same "traceroute -T shows the path" "$(hops -T)" "$want"
  hop2=$(traceroute -n -q 1 -w 0.5 -m 8 10.99.0.80 2>/dev/null | awk 'NR==3 {print $3}')
  near "the second hop answers after its own delay" "$hop2" 15 5
  near "ping still gets the target's rule" "$(avg 10.99.0.80 2)" 30 5
  if command -v mtr >/dev/null; then
    same "mtr shows the path" "$(mtr -n -r -c 1 -G 0.5 10.99.0.80 | tail -n +3 | awk '{print $2}' | tr '\n' ' ')" "192.168.1.1 10.0.0.1 ??? 72.14.0.1 10.99.0.80 "
  fi
  # cut= loses everything beyond the hop: the target becomes unreachable.
  curl -sf -X PUT localhost:9100/paths/10.99.0.80 -d '192.168.1.1@2ms,cut=100% > 10.0.0.1@15ms' >/dev/null
  same "a cut hop still answers its own probe" "$(hops | awk '{print $1}')" "192.168.1.1"
  if ping -c 1 -W 1 10.99.0.80 >/dev/null 2>&1; then fail "a ping got through a cut hop"; fi
  pass "a cut hop loses pings to the target"
  events=$(curl -sf "localhost:9100/history?target=10.99.0.80" | grep -c '"result": "time-exceeded"' || true)
  [ "$events" -ge 8 ] && pass "the history records the hops' answers ($events time-exceeded)" || fail "history has $events time-exceeded events"
  stop
  if [ -e /proc/net/if_inet6 ]; then
    start -seed 1 -rule fd99::80=30ms -path 'fd99::80=fd99::1:1@2ms > * > fd99::1:3@25ms'
    same "IPv6: traceroute shows the path" "$(traceroute -6 -n -q 1 -w 0.5 -m 6 fd99::80 2>/dev/null | tail -n +2 | awk '{print $2}' | tr '\n' ' ')" "fd99::1:1 * fd99::1:3 fd99::80 "
    same "IPv6: traceroute -I shows the path" "$(traceroute -6 -I -n -q 1 -w 0.5 -m 6 fd99::80 2>/dev/null | tail -n +2 | awk '{print $2}' | tr '\n' ' ')" "fd99::1:1 * fd99::1:3 fd99::80 "
    stop
  fi
else
  echo "SKIP paths: traceroute is not installed"
fi

echo "all checks passed"
