# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

English | [中文](README.zh-CN.md)

**pingmock fakes the network for your tests.** Your program pings servers, connects to databases, looks up names, and decides things from what happens: it picks the fastest server, retries, fails over, shows "offline". Testing those decisions needs a network that is slow, broken or unreachable exactly when the test wants it. With pingmock you write rules such as "this server answers in 300 ms", "that database refuses connections" or "this name does not exist", and your program, unchanged, gets those answers.

It runs on Linux and mocks ping (ICMP and ICMPv6 Echo), TCP connections, the ICMP errors of UDP and, under `pingmock exec`, DNS: the **delay, jitter, packet loss, unreachable errors, refused connections and DNS failures** of each address, port and name, changed while your tests run. New to networking? [Network basics](docs/basics.md) explains ping, TCP and DNS with diagrams, and [First steps](#first-steps) tries them out one command at a time.

![A program that pings three servers and picks the fastest chooses a different one each time pingmock changes their latency](docs/demo/demo.svg)

The shortest example, with no root needed:

```bash
pingmock exec -rule 10.99.0.1=50ms -rule 10.99.0.2=unreachable -- ./my-app
```

Connections work the same way, for programs that connect rather than ping: `pingmock exec -tcp 10.99.0.1:5432=timeout -tcp 10.99.0.2:5432=5ms -- ./my-app` makes the first database never answer and the second accept in 5ms, see [TCP connections](#tcp-connections).

Compared to `tc netem`, rules are set per **target address** rather than per interface, they can be **changed while the program runs** through an HTTP API, and `pingmock exec` needs **no root** and leaves the host network alone.

**Try it now with Docker**, with nothing to install. pingmock answers the first three pings to 10.99.0.1 in 20ms and the next ones in 300ms, and reports 10.99.0.2 unreachable:

<!-- docker-demo -->
```bash
docker run --rm --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh ghcr.io/nanmuyao/pingmock -c '
  pingmock -addr6= -rule "10.99.0.1=20ms for 3 pings; 300ms" -rule 10.99.0.2=unreachable &
  until wget -q -O /dev/null 127.0.0.1:9100/rules 2>/dev/null; do kill -0 $! 2>/dev/null || exit 1; sleep 0.1; done
  ping -c 6 -i 0.5 10.99.0.1; ping -c 2 -i 0.5 10.99.0.2'
```
<!-- /docker-demo -->

The output, shortened: pingmock logs its rules, the fourth ping takes 300ms instead of 20ms, and 10.99.0.2 is reported unreachable at once:

```
2026/10/18 10:23:05 rule 10.99.0.1/32: delay=20ms for 3 pings; delay=300ms
2026/10/18 10:23:05 rule 10.99.0.2/32: delay=0s,unreachable
...
64 bytes from 10.99.0.1: icmp_seq=1 ttl=64 time=20.2 ms
64 bytes from 10.99.0.1: icmp_seq=2 ttl=64 time=20.3 ms
64 bytes from 10.99.0.1: icmp_seq=3 ttl=64 time=20.3 ms
64 bytes from 10.99.0.1: icmp_seq=4 ttl=64 time=301 ms
64 bytes from 10.99.0.1: icmp_seq=5 ttl=64 time=301 ms
64 bytes from 10.99.0.1: icmp_seq=6 ttl=64 time=301 ms
...
From 10.99.0.2 icmp_seq=1 Destination Host Unreachable
From 10.99.0.2 icmp_seq=2 Destination Host Unreachable
```

This runs pingmock and `ping` in one container for brevity. To test your own program, run it under [`pingmock exec`](#running-without-root-pingmock-exec), or in pingmock's network with Docker Compose as in [`examples/compose-demo`](examples/compose-demo/).

## How it works

pingmock creates a TUN virtual network device and routes test subnets (`10.99.0.0/24` and `fd99::/64` by default), plus any addresses you choose, to it. The kernel hands every echo request sent to those addresses to pingmock, which waits according to the matching rule and then sends back a reply or a destination unreachable error, or drops the request.

The software under test needs **no changes at all**: whether it runs the `ping` command or sends ICMP itself through a raw socket, it gets mocked. The request payload is echoed back unchanged, so tools like `ping` that put a timestamp in the payload report exactly the configured latency.

Connection attempts travel the same way: pingmock reads the TCP SYN from the device and writes back the SYN-ACK, the reset or nothing, and the program's own kernel does the rest of the handshake. DNS queries never reach the device: under `pingmock exec`, the command's `resolv.conf` points at a small DNS server pingmock runs for it. [Network basics](docs/basics.md#8-how-pingmock-gets-in-the-middle) draws this, and the [code tour](docs/architecture.md#connections-and-dns-queries) shows the code.

### When to use it, and when to use something else

pingmock tests **programs that decide things from ping or from whether a connection opens**. It does not slow real traffic down, and it does not touch the data of TCP connections. Each of these tools has its place:

| You want to | Use |
|---|---|
| Test a program that pings or connects: server selection, failover, "is the network up", a latency display, connect timeouts and refusals | **pingmock** |
| Slow down or lose real traffic on an interface, for every protocol: load tests, "how does the app feel on 3G" | [`tc netem`](https://man7.org/linux/man-pages/man8/tc-netem.8.html), the kernel's network emulator |
| Slow down or break the data of TCP connections to one service: latency, bandwidth, resets in the middle of a stream, for a database or an API | [Toxiproxy](https://github.com/Shopify/toxiproxy), a TCP proxy with an API and client libraries |
| Make names fail to resolve for the whole machine or a cluster, outside a test | a DNS server you point clients at, such as CoreDNS's `erratic` plugin, or a chaos tool; for one name, an `/etc/hosts` entry. pingmock's [DNS faults](#dns-faults) work per name, under `pingmock exec` |

netem works on an interface and queue, so one rule tends to catch all traffic, including your own SSH session, and it needs root. Toxiproxy sits between the application and one TCP service, so the application has to be pointed at the proxy. pingmock is **transparent**: the program keeps pinging and connecting to the addresses and ports it always uses, with no proxy to point it at, and `pingmock exec` needs **no root** and changes nothing outside the command it runs. On top of that it takes the test-oriented side of both: faults are declared **per target address, prefix and port** in a configuration file kept with the tests, the random loss and jitter are **reproducible** with a seed, and rules change **while the program runs** through the API.

### What you can rely on

Four things hold for everything pingmock does:

| | What it means | How |
|---|---|---|
| **Transparent** | The program under test is not changed: no code, no configuration. It keeps pinging, connecting to and resolving the addresses, ports and names it always uses | A TUN device and routes catch the requests, and under `pingmock exec` a private DNS server answers the names; even the `ping` command works as it is |
| **Per target** | Every target can have its own network conditions | Rules apply to an address or a prefix, for TCP also to a port, and under `pingmock exec` to a name |
| **Reproducible** | The same setup gives the same results on every run | `-seed`, deterministic `pattern`, scenarios by count or time, `POST /reset` |
| **Controllable and observable** | Conditions can change in the middle of a test, and afterwards you can see what the program really did | The HTTP API and the Go client, the request history and statistics |

## Installation

The `ip` command (iproute2) must be available at runtime.

**Download a prebuilt binary** (no Go required) from the [Releases](https://github.com/nanmuyao/pingmock/releases) page, for example:

```bash
curl -L -o pingmock https://github.com/nanmuyao/pingmock/releases/latest/download/pingmock-linux-amd64
chmod +x pingmock
```

**Install with Go** (Go 1.22+):

```bash
go install github.com/nanmuyao/pingmock@latest
```

**Container image** (amd64 and arm64), see [Docker](#docker):

```bash
docker pull ghcr.io/nanmuyao/pingmock
```

**Build from source:**

```bash
git clone https://github.com/nanmuyao/pingmock.git
cd pingmock
go build -o pingmock .
```

Programs installed with `go install` live in `$(go env GOPATH)/bin`. When pingmock runs as root, `sudo` may not find it on its `PATH`; use the full path instead, e.g. `sudo $(go env GOPATH)/bin/pingmock`.

## First steps

Five commands, each showing one thing pingmock does. They use `pingmock exec`, which needs no root: it runs the command after `--` in a private network, so nothing on your machine changes. `-addr6=` turns IPv6 off to keep the output short. pingmock prints its rules first, in lines starting with `[pingmock]`; they are left out below.

**1. A server that answers in 200 ms.** pingmock pretends there is a machine at 10.99.0.1, and answers each ping after 200 ms:

```console
$ pingmock exec -addr6= -rule 10.99.0.1=200ms -- ping -c 3 10.99.0.1
64 bytes from 10.99.0.1: icmp_seq=1 ttl=64 time=201 ms
64 bytes from 10.99.0.1: icmp_seq=2 ttl=64 time=200 ms
64 bytes from 10.99.0.1: icmp_seq=3 ttl=64 time=203 ms
3 packets transmitted, 3 received, 0% packet loss, time 2005ms
```

There is no machine at 10.99.0.1: `ping` is unchanged, and pingmock answers for it ([how](docs/basics.md#8-how-pingmock-gets-in-the-middle)). `time=` is the latency the rule set.

**2. A server that is down, two ways.** `unreachable` answers with an error at once; `timeout` never answers, so `ping` waits (here 1 second, `-W 1`) and gets nothing:

```console
$ pingmock exec -addr6= -rule 10.99.0.2=unreachable -rule 10.99.0.3=timeout -- \
    sh -c 'ping -c 2 -W 1 10.99.0.2; ping -c 2 -W 1 10.99.0.3'
From 10.99.0.2 icmp_seq=1 Destination Host Unreachable
From 10.99.0.2 icmp_seq=2 Destination Host Unreachable
2 packets transmitted, 0 received, +2 errors, 100% packet loss, time 1057ms
2 packets transmitted, 0 received, 100% packet loss, time 1018ms
```

A program usually treats the two differently: an error is known at once, a timeout costs waiting ([why](docs/basics.md#4-when-there-is-no-answer-timeout-versus-unreachable)).

**3. Change a rule while the program runs.** pingmock listens on `localhost:9100` for changes. The server is fast, then becomes slow:

```console
$ pingmock exec -addr6= -rule 10.99.0.1=20ms -- sh -c '
    ping -c 2 -q 10.99.0.1 | tail -1
    curl -s -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
    ping -c 2 -q 10.99.0.1 | tail -1'
rtt min/avg/max/mdev = 20.515/20.919/21.324/0.404 ms
{
  "rule": "delay=300ms",
  "target": "10.99.0.1/32"
}
rtt min/avg/max/mdev = 300.179/300.573/300.967/0.394 ms
```

This is how a test makes the network change in the middle: from the test code, with `curl` or a [client library](#using-it-in-automated-tests).

**4. A database that refuses connections, and one that never answers.** TCP rules work on an address and a port:

```console
$ pingmock exec -addr6= -tcp 10.99.0.1:80=refuse -tcp 10.99.0.2:80=timeout -- \
    sh -c 'curl -sS http://10.99.0.1/; curl -sS --connect-timeout 2 http://10.99.0.2/'
curl: (7) Failed to connect to 10.99.0.1 port 80 after 0 ms: Couldn't connect to server
curl: (28) Failed to connect to 10.99.0.2 port 80 after 2002 ms: Timeout was reached
```

The first is refused at once, the second times out after the 2 seconds curl was given ([TCP basics](docs/basics.md#5-tcp-connections)).

**5. A name that does not exist.** DNS rules make lookups fail, here with "no such name" (NXDOMAIN); `getent` exits with 2, "not found":

```console
$ pingmock exec -addr6= -dns api.example.com=nxdomain -- getent hosts api.example.com.
$ echo $?
2
```

The dot at the end of the name keeps the lookup from trying your network's search domains ([DNS basics](docs/basics.md#7-dns-from-a-name-to-an-address), [DNS faults](#dns-faults)).

To test your own program, put it after `--` instead: `pingmock exec -rule 10.99.0.1=200ms -- ./my-app`. The rest of this README goes through each of these in detail.

## Usage

pingmock runs in one of two ways:

- `sudo pingmock [flags]` mocks pings and connections for the whole machine until you stop it. This is what the rest of this section describes.
- `pingmock exec [flags] -- CMD` runs a command in a private network where its pings, connections and name lookups are mocked. It needs **no root** and leaves the host network alone, which makes it the best fit for tests and CI. See [Running without root](#running-without-root-pingmock-exec).

In the first mode, pingmock needs root privileges (or `CAP_NET_ADMIN`).

```bash
sudo ./pingmock \
  -rule 10.99.0.1=50ms \
  -rule 10.99.0.2=delay=200ms,jitter=30ms \
  -rule 10.99.0.3=loss=20% \
  -rule 10.99.0.4=timeout
```

Then, in another terminal:

```bash
ping 10.99.0.1   # about 50ms
ping 10.99.0.2   # random, between 170 and 230ms
ping 10.99.0.3   # about 20% packet loss
ping 10.99.0.4   # every request times out
ping 10.99.0.9   # no rule, so the default applies (immediate reply)
```

When you stop pingmock with Ctrl+C, the virtual device and its routes are removed automatically.

### Rule format

`-rule TARGET=RULE`, repeatable.

**TARGET** is an IPv4 or IPv6 address or CIDR prefix, such as `10.99.0.1`, `10.99.0.128/25` or `fd99::1`. When several rules match, **the most specific prefix wins**.

A **RULE** is made of these settings:

| Form | Meaning |
|---|---|
| `120ms`, `120`, `1.5s` | Fixed delay (a bare number means milliseconds) |
| `delay=200ms,jitter=30ms` | Delay uniformly distributed between 170 and 230ms |
| `loss=5%` or `loss=0.05` | Each request is dropped with a 5% probability |
| `pattern=..x` | Deterministic outcomes, repeated in order: `.` replies, `x` drops, `u` answers with destination unreachable (here every third request is dropped) |
| `unreachable` | Answer with ICMP destination unreachable (host unreachable) instead of a reply |
| `unreachable=net` / `unreachable=admin` | Network unreachable / administratively prohibited, as a firewall would send |
| `timeout` | No request gets an answer; same as `loss=100%` |
| `ttl=128` | TTL (IPv6 hop limit) of the answers, 1 to 255; `ping` shows it as `ttl=128`. Default 64, as a Linux host sends. Software sometimes guesses the system or the distance from it |

They can be combined, for example `delay=80ms,jitter=10ms,loss=2%`, `delay=200ms,unreachable=net` (the error arrives after 200ms) or `delay=30ms,ttl=128`. `loss` and `pattern` cannot be used together. With a pattern, `unreachable=` selects the code used for `u`.

### Unreachable versus timeout

Software often handles the two failures differently: an unreachable error arrives immediately, while a lost request makes the software wait for its timeout. `ping` reports them like this:

| Rule | IPv4 | IPv6 |
|---|---|---|
| `unreachable` | `Destination Host Unreachable` | `Destination unreachable: Address unreachable` |
| `unreachable=net` | `Destination Net Unreachable` | `Destination unreachable: No route` |
| `unreachable=admin` | `Packet filtered` | `Destination unreachable: Administratively prohibited` |
| `timeout` | (no answer) | (no answer) |

The error comes from the target address itself (`From 10.99.0.3 icmp_seq=1 Destination Host Unreachable`) and quotes the original request, so ping and other tools match it to the request they sent.

### Scenarios: rules that change over time

A rule can consist of several **phases** separated by `;`. Each phase lasts for a duration (`for 10s`) or a number of requests (`for 5 pings`), and the last phase stays in effect. End with `repeat` to loop instead:

```bash
# Normal for 10 pings, then the link goes down for 5 seconds, then it recovers
sudo ./pingmock -rule '10.99.0.1=30ms for 10 pings; timeout for 5s; 30ms'

# A flapping link: 20 seconds good, 10 seconds bad, forever
sudo ./pingmock -rule '10.99.0.2=40ms for 20s; delay=400ms,jitter=100ms for 10s; repeat'
```

Time-based phases are measured from the moment the rule was set; request-based phases count the requests to each target address separately. Setting a rule again, for example through the API, restarts it from the first phase.

For tests that must give the same result every time, prefer request-based phases (`for 5 pings`): they do not depend on how fast the test runs.

### Reproducible randomness

`loss` and `jitter` are random, but reproducible. Every target address gets its own random sequence derived from a **seed** and the address, so the requests to one target always get the same outcomes, no matter how requests to other targets interleave with them.

Without `-seed`, pingmock picks a random seed and logs it at startup:

```
seed 4871203317 (pass -seed 4871203317 to reproduce this run)
```

When a test fails, rerun it with that seed to get exactly the same drops and delays. In CI, set a fixed seed (`-seed 42`) so every run behaves the same.

### IPv6

IPv6 works the same way. The virtual device gets `fd99::fe/64`, so `fd99::1`, `fd99::2` and so on are mocked hosts, and rules, the API and configuration files accept IPv6 addresses and prefixes:

```bash
sudo ./pingmock -rule fd99::1=30ms -rule 'fd99::2=delay=10ms,pattern=..u' -rule 2001:db8::8888=timeout
ping -6 fd99::1
```

If the kernel has no IPv6, pingmock logs `IPv6 is unavailable, continuing with IPv4 only` and runs with IPv4 alone; rules for IPv6 targets are then rejected. Passing `-addr6` explicitly makes a missing IPv6 an error instead, and `-addr6 ""` turns IPv6 off.

### Mocking real addresses

If the software under test pings a hard-coded real address such as `8.8.8.8` or `2001:4860:4860::8888`, just add a rule for it:

```bash
sudo ./pingmock -rule 8.8.8.8=120ms
```

pingmock routes that address to its virtual device. **Note: while pingmock is running, all traffic from this machine to that address is intercepted**, not only ping. Normal routing comes back when pingmock exits. Running it on a test machine or in a container is recommended.

A rule can also cover a whole network the host is on, such as `192.168.20.0/24` on a VLAN interface. pingmock then routes it as two halves, `192.168.20.0/25` and `192.168.20.128/25`, which are more specific and so take the traffic, and leaves the host's own route for the network in place, so that the host is not cut off from it when pingmock exits. It logs when it does so. A single address that already has a route of its own cannot be split around: pingmock refuses it rather than replace that route. `pingmock exec` has none of this to worry about, since it never touches the host's routes.

If the software pings a hostname, point that name at an address in the test subnet in `/etc/hosts`, e.g. `10.99.0.1 api.example.com`.

### Configuration file

With many rules, a configuration file is easier to maintain, and it can live next to the tests that use it. Configuration files are YAML, so they can have comments. See [`examples/pingmock.yaml`](examples/pingmock.yaml):

```yaml
seed: 42           # same random loss and jitter on every run
default: 1ms       # for targets that no rule matches

rules:
  10.99.0.1: 20ms
  10.99.0.2: delay=120ms,jitter=15ms,loss=2%
  10.99.0.3: 30ms for 10 pings; timeout for 5s; 30ms   # goes down, then recovers
  10.99.0.4:                                           # flaps forever
    phases:
      - spec: delay=40ms,pattern=....x
        for: 20s
      - spec: {delay: 400ms, jitter: 100ms}
        for: 10s
    repeat: true
  10.99.0.5: delay=50ms,unreachable=admin
  10.99.0.6: delay=25ms,ttl=128                        # answers like a Windows host
  fd99::1: 30ms                                        # IPv6 needs no quotes
  8.8.8.8: timeout
```

```bash
sudo ./pingmock -config examples/pingmock.yaml
```

A rule is either the same text as `-rule`, a number of milliseconds, or a mapping with `delay`, `jitter`, `loss` (`0.05` or `5%`), `pattern`, `unreachable` and `ttl`, or with `phases` and `repeat`, each phase a `spec` with either `for: 10s` or `count: 5` (requests). The file can also set `dev`, `addr`, `addr6`, `api`, `history`, [`paths`](#paths-traceroute-and-mtr), [`tcp`](#tcp-connections), [`udp`](#udp-datagrams) and, for `pingmock exec`, [`hosts`](#names) and [`dns`](#dns-faults); under `pingmock exec`, a rule's key may be a name. TCP rules take `refuse`, `after`, `send`, `reply`, `limit` and `wait` in the mapping form too, and DNS rules `nxdomain` and `servfail` (`{delay: 5ms, after: reset}`, `{servfail: true}`). In the mapping form, `send` and `reply` are text, with YAML's own escapes in double quotes (`reply: "HTTP/1.1 503 x\r\n\r\n"`); in the text form, write the rule in single quotes, so that YAML leaves the Go string to pingmock (`'reply="\xff\x00"'`).

Mistakes are reported with their line, and unknown keys are rejected, so typos are caught at startup. JSON configuration files from earlier versions keep working unchanged, since JSON is valid YAML.

Flags given on the command line override the file, so one file can serve many tests: `-config base.yaml -rule 10.99.0.1=500ms` replaces just that rule.

### Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-rule` | | `TARGET=RULE`, repeatable; under `pingmock exec`, `TARGET` may be a [name](#names) |
| `-default` | `0ms` | Rule used for targets without a matching rule |
| `-config` | | YAML configuration file |
| `-seed` | random | Seed for `loss` and `jitter`; the same seed reproduces the same run |
| `-addr` | `10.99.0.254/24` | IPv4 address and prefix of the virtual device. Every address in the subnet **except this one** is a mocked host |
| `-addr6` | `fd99::fe/64` | IPv6 address and prefix of the virtual device; an empty string disables IPv6 |
| `-dev` | `pingmock0` | Name of the virtual device |
| `-api` | `127.0.0.1:9100` | Listen address of the control API; an empty string disables it |
| `-history` | `1000` | Number of recent requests kept for `GET /history`; `0` disables it |
| `-host` | | `NAME=ADDR`, repeatable, `pingmock exec` only: the command resolves `NAME` to `ADDR`, see [Names](#names) |
| `-path` | | `TARGET=PATH`, repeatable: the route traceroute and mtr see to `TARGET`, see [Paths](#paths-traceroute-and-mtr) |
| `-tcp` | | `TARGET=RULE`, repeatable: how TCP connections to `TARGET` (`ADDR[:PORT]`, `PREFIX[:PORT]`, `[IPv6]:PORT`) are answered, see [TCP connections](#tcp-connections) |
| `-udp` | | `TARGET=RULE`, repeatable: how UDP datagrams to `TARGET` fail, see [UDP datagrams](#udp-datagrams) |
| `-dns` | | `NAME=RULE`, repeatable, `pingmock exec` only: how the command's DNS queries for `NAME` are answered, see [DNS faults](#dns-faults) |
| `-v` | `false` | Log every request |

`pingmock version` (or `-version`), as the only argument, prints the version and exits.

## TCP connections

Much software that checks the network also connects: to a database, an API, a port it watches. A **TCP rule** decides how connection attempts to an address and port are answered, so the program sees a slow connect, a refused one, or one that never completes, without any change to it:

```bash
pingmock exec \
  -tcp 10.99.0.1:5432=timeout \
  -tcp 10.99.0.2:5432=5ms \
  -tcp '10.99.0.3:443=refuse for 3 requests; 20ms' \
  -- ./my-app
```

Here the first database never answers, so `connect` times out and the program should fail over to the second, which accepts in 5ms. The third server refuses three connections (`ECONNREFUSED` at once), then accepts.

The target is `ADDR:PORT`, `PREFIX:PORT` or, without a port, every port of `ADDR` or `PREFIX`. IPv6 is written in brackets, as in URLs: `[fd99::1]:443`, `[fd99::/64]:443`, `[fd99::1]`. Without them `fd99::1:443` would itself be an address, so it is rejected. Under `pingmock exec` the target may be a [name](#names), such as `db.example.com:5432`.

The rule is written as for `-rule`, and means:

| Setting | The program sees |
|---|---|
| `20ms`, `delay=`, `jitter=` | The connection is accepted after the delay: `connect` takes that long |
| `refuse` | The connection is refused at once (after the delay, if there is one): a TCP reset, `ECONNREFUSED` |
| `timeout`, `loss=30%` | No answer: `connect` times out, for every attempt or 30% of them |
| `unreachable[=net\|admin]` | `connect` fails at once: `EHOSTUNREACH` ("No route to host"), or `ENETUNREACH` ("Network is unreachable") for `net` |
| `pattern=..r` | Attempts in turn: `.` accepted, `x` no answer, `u` unreachable, `r` refused |
| `after=close` | The connection is accepted, then closed at once: a read gets end of file, an HTTP client an empty reply (curl: "Empty reply from server") |
| `after=reset` | The connection is accepted, then reset when the program first sends or closes: `ECONNRESET` ("Connection reset by peer") |
| `after=hang` | The default: the connection is accepted and held open, nothing is sent |
| `send="..."`, `reply="..."`, `limit=`, `wait=` | Bytes sent on the connection, a limit on what the program may send, and a delay, see [Sending data](#sending-data) |
| `ttl=` | The TTL of the answers |

Phases count connection attempts: `for 3 requests` (or `connections`). An attempt is counted once, however many times the client's kernel sends its SYN again while waiting, so `timeout for 2 requests` means two attempts whatever the kernel's retry timer. When several rules match, the most specific prefix wins, then a rule for the port over one for every port.

As with ping rules, a TCP rule for a real address in root mode routes **all** traffic to that address to pingmock, see [Mocking real addresses](#mocking-real-addresses); `pingmock exec` affects only the command.

### After the connection opens

An accepted connection is held open, and unless the rule [sends data](#sending-data) nothing is sent on it: a program that reads gets a read timeout, which tells it apart from a connect timeout. Ports without a TCP rule are not answered, as before, unless the address has a [path](#paths-traceroute-and-mtr), whose target accepts every port.

The same rule tests a **TLS handshake timeout**: the connection opens, the client sends its ClientHello and never gets an answer. `pingmock exec -tcp api.example.com:443=5ms -- ./my-app` makes every HTTPS request to that name hang in the handshake: Python's `ssl` reports "The handshake operation timed out", curl gives up with exit 28, and Go's `http.Transport` fails after its `TLSHandshakeTimeout`.

`after=` says what happens next instead, to test how a program handles a server that drops it: `-tcp '10.99.0.1:5432=after=reset'` makes every query to that database fail with "connection reset by peer", and `-tcp '10.99.0.1:443=5ms,after=close for 2 requests; 5ms'` gives two empty replies before the server works (accepts, then holds the connection).

`after=reset` waits for the program to send: a reset sent as soon as the connection opens would sometimes arrive while `connect` is still returning, and fail it instead. A program that waits for the server to speak first, as SSH, SMTP and MySQL clients do, therefore gets a read timeout under `after=reset`, and end of file under `after=close`.

Data the program writes after the close gets a reset, as from a real server that closed its socket; a write after that fails with `EPIPE`. The action is decided when the connection is accepted, so it does not change if a phase ends before the program sends. A connection that stays idle for 10 minutes is forgotten, and gets nothing after that.

### Sending data

A rule can also send fixed bytes on the connection, to test how a program handles what a server says, or the server that stops listening:

```bash
pingmock exec \
  -tcp '10.99.0.1:22=send="SSH-2.0-OpenSSH_9.6\r\n"' \
  -tcp '10.99.0.2:80=reply="HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n",after=close' \
  -tcp '10.99.0.3:5432=limit=1000,after=reset' \
  -tcp '10.99.0.4:443=wait=2s,reply="HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"' \
  -- ./my-app
```

| Setting | What pingmock does |
|---|---|
| `send="..."` | Sends the bytes as soon as the connection opens, as servers that speak first do: SSH, SMTP, FTP, MySQL |
| `reply="..."` | Sends the bytes when the program first sends data: one fixed answer to the request, whatever it was |
| `limit=1000` | Accepts 1000 bytes from the program, then ends the connection as `after=` says; it needs `after=close` or `after=reset` |
| `wait=2s` | Waits that long before sending `send` or `reply`: a server that is slow to answer |

The bytes are written as a Go string in double quotes, with `\r`, `\n`, `\t`, `\"`, `\\` and `\xff` for any byte, up to 16 KiB each. pingmock sends them as they are and never looks at what the program sends: it plays a server that always says the same thing, not a protocol. When the rule sends data or has a limit, pingmock acknowledges what the program sends, so its writes do not stall; without them, nothing is acknowledged, as above.

`after=` decides what follows the data: with `after=close`, pingmock closes the connection once it has sent everything (after `reply` if there is one), so an HTTP client gets the reply and the end of the response; with `after=reset`, the program's next data or close gets a reset; with `after=hang`, the connection stays open and silent. With `limit`, `after=` applies at the limit instead. pingmock never sends anything twice: on a network that loses packets, use the rule's `delay` and `loss` for the connection attempts instead.

`wait` delays only pingmock's data: the program's own data is acknowledged at once, as a slow server's kernel would, so the program waits for an answer rather than resending its request.

### Configuration file, API and history

In a configuration file (quote a key with brackets) and through the API:

```yaml
tcp:
  10.99.0.1:5432: timeout
  10.99.0.3:443: refuse for 3 requests; 20ms
  "[fd99::1]:443": {delay: 200ms, jitter: 20ms}
  10.99.0.4:8080: {delay: 5ms, after: reset}
```

```bash
curl -X PUT localhost:9100/tcp/10.99.0.1:5432 -d refuse
curl -X DELETE localhost:9100/tcp/10.99.0.1:5432
```

The Go client has `SetTCP`, `DeleteTCP` and `TCPStats`. `GET /rules` lists the TCP rules under `tcp`, and their statistics under `tcp_stats`, keyed by address and port (`"10.99.0.1:5432"`): requests, accepted, refused, dropped, unreachable, and of the accepted ones, closed and reset. History events of connection attempts carry `"probe": "tcp"` and the `port`, with the result `syn-ack` (accepted), `refused`, `drop` or `unreachable`; a SYN the kernel sent again is recorded with `"repeat": true` and not counted. Closing or resetting an accepted connection adds an event with the result `closed` or `reset`, and the rule and phase that accepted it. Randomness comes from the seed, per address and port, separate from the pings to the same address, whose seeded results do not change.

## UDP datagrams

UDP has no connections, so a program that sends to a UDP service (syslog, statsd, NTP, a game server) learns that it is down only from an ICMP error, or from silence. A **UDP rule** decides which, per address and port; pingmock never sends a UDP answer:

```bash
pingmock exec \
  -udp 10.99.0.1:514=refuse \
  -udp '10.99.0.2:123=timeout for 2 requests; refuse' \
  -udp 10.99.0.3=unreachable=admin \
  -- ./my-app
```

| Setting | What pingmock does | What a connected UDP socket sees on its next receive |
|---|---|---|
| `refuse` | ICMP port unreachable, as a host with nothing on the port sends | `ECONNREFUSED` ("Connection refused") |
| `unreachable=admin` | ICMP administratively prohibited, as a firewall that rejects sends | `EHOSTUNREACH` over IPv4, `EACCES` over IPv6 |
| `unreachable`, `unreachable=net` | ICMP host or network unreachable | nothing: Linux reports these to UDP sockets only with `IP_RECVERR` |
| `timeout`, `loss=30%` | no answer | a receive timeout |
| `pattern=xxr` | datagrams in turn: `x` no answer, `u` unreachable, `r` refused | |
| `delay=`, `jitter=`, `ttl=` | the delay and TTL of the ICMP error | |

The target is written as for [TCP rules](#tcp-connections), with names under `pingmock exec`. Phases count datagrams: every datagram is a request. A rule must say how datagrams fail: `20ms` alone, with no error to delay, is rejected. A UDP rule for a target with a [path](#paths-traceroute-and-mtr) decides at the destination, and the hops still answer traceroute's probes on the way. The command's own DNS server under `pingmock exec` is not on the virtual device, so `-udp` does not reach it: use [DNS faults](#dns-faults).

In a configuration file and through the API:

```yaml
udp:
  10.99.0.1:514: refuse
  10.99.0.2:123: {refuse: true, delay: 5ms}
```

```bash
curl -X PUT localhost:9100/udp/10.99.0.1:514 -d refuse
curl -X DELETE localhost:9100/udp/10.99.0.1:514
```

The Go client has `SetUDP`, `DeleteUDP` and `UDPStats`. `GET /rules` lists the UDP rules under `udp`, and their statistics under `udp_stats`, keyed by address and port: requests, refused, dropped, unreachable. History events of datagrams carry `"probe": "udp"` and the `port`, with the result `refused`, `unreachable` or `drop`.

## Paths: traceroute and mtr

For software that looks at the route, not just the destination, a target can get a **path**: the routers between here and it, each answering probes whose TTL runs out there, as real routers do. traceroute and mtr then show the hops, in every probe mode (UDP, ICMP and TCP), and reach the target.

```bash
sudo pingmock -rule 8.8.8.8=30ms \
  -path '8.8.8.8=192.168.1.1@2ms > 10.0.0.1@delay=15ms,jitter=3ms > * > 72.14.0.1@25ms'
```

```
$ traceroute -n 8.8.8.8
 1  192.168.1.1  2.3 ms
 2  10.0.0.1  15.8 ms
 3  *
 4  72.14.0.1  25.4 ms
 5  8.8.8.8  30.6 ms
```

A path is hops separated by `>`, each `ADDR@SETTINGS`, or `*` for a router that never answers. The settings are:

| Setting | Meaning |
|---|---|
| `2ms`, `delay=2ms` | Round trip to this hop |
| `jitter=3ms` | Delay uniform in ±3ms |
| `loss=10%` | This hop does not answer 10% of the probes that end there, as routers that rate-limit ICMP errors do. Nothing else is affected: mtr shows loss on that line only |
| `cut=5%` | This hop loses 5% of everything going through it: the probes for later hops and the target, and the pings to the target. A lossy link looks like this: loss from that hop on |
| `ttl=250` | TTL of this hop's answers (default 64) |

The target's own rule still decides what the target answers: its delay and loss apply to pings as before, and to traceroute's last line. A target with a path also answers UDP probes with port unreachable and TCP probes with a SYN-ACK, on every port, which is how traceroute's UDP and TCP modes know they arrived; a [TCP rule](#tcp-connections) for the port takes precedence at the target. Targets without a path keep ignoring UDP, and TCP on ports without a TCP rule. The hops' addresses get routes to the virtual device, so the kernel accepts their answers under strict reverse-path filtering; inside `pingmock exec` this touches nothing outside the command. Randomness comes from the seed, per target, so `loss` and `cut` repeat exactly with `-seed`.

In a configuration file (quote a path that starts with `*`, which YAML would read as an alias) and through the API:

```yaml
paths:
  8.8.8.8: 192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms
```

```bash
curl -X PUT localhost:9100/paths/8.8.8.8 -d '192.168.1.1@2ms > * > 72.14.0.1@25ms'
curl -X DELETE localhost:9100/paths/8.8.8.8
```

The Go client has `SetPath` and `DeletePath`. `GET /rules` lists the paths, and the history records each probe with its `ttl`, the `hop` that answered or lost it and the address it answered `from`; a probe's `result` is `time-exceeded` from a hop, `port-unreachable` or `syn-ack` from the target.

## Running without root: pingmock exec

```bash
pingmock exec [flags] -- CMD [ARGS...]
```

runs `CMD` in a **private network namespace** in which pingmock answers pings and connections, and, for `-dns` rules and names, DNS queries, with the same flags as above:

```bash
pingmock exec -rule 10.99.0.1=50ms -rule 8.8.8.8=unreachable -- ./my-app --check-latency
```

- **No root needed.** As an unprivileged user, pingmock also creates a user namespace in which it may set up the network. As root, it creates only the network namespace.
- **The host network is not touched.** Mocking `8.8.8.8` affects only `CMD`, and several runs can happen at once, each with its own rules, device and API port.
- **Ready before CMD starts.** Pings are answered and the API listens as soon as `CMD` runs. `CMD` finds the API address in `$PINGMOCK_API`.
- **Exits with CMD's status**, so it fits in scripts and CI: the exit status of `CMD`, `128+N` if signal `N` killed it, or `127` if it was not found. `SIGTERM`, `SIGHUP` and `SIGQUIT` are passed on to `CMD`.
- **Nothing is left behind.** When `CMD` exits, processes it started in the background are stopped too.

Inside the private network, `CMD` reaches only the mocked targets and the API on `127.0.0.1`; it has no other network access. So build programs before running them there: a `go run` (or `npm install`, `pip install`...) that needs to download dependencies fails with `network is unreachable`.

### Names

Much software pings a name rather than an address. Under `pingmock exec`, a rule can be written for a name:

```bash
pingmock exec -rule api.example.com=80ms -rule backup.example.com=timeout -- ./my-app
```

The command resolves `api.example.com` to a mocked address and gets the rule when it pings it. The address comes from the top of the subnet down (`10.99.0.253`, `10.99.0.252`, ...), the same one on every run, and the startup log says which: `rule api.example.com (10.99.0.253): delay=80ms`. To choose the address yourself, or to make a name resolve to an IPv6 address, use `-host NAME=ADDR`, repeatable:

```bash
pingmock exec -host api.example.com=fd99::1 -rule api.example.com=80ms -- ./my-app
```

How it works: in a mount namespace of its own, the command gets its **own `/etc/hosts`** (the system's, with the mocked names on top) and a **`resolv.conf` that points at a small DNS server** pingmock runs for it. That server answers for the mocked names, both ways (the address resolves back to the name), and forwards every other name to the host's resolver, so real names keep resolving even though their addresses are unreachable from the private network. It answers over UDP and TCP, which resolvers use for large answers, and each query on its own; when the host's resolver does not answer, neither does it, so the command times out as it would without pingmock, and when it cannot be reached at all, the command gets a failure at once. Everything that resolves names sees the same thing: `ping` and other programs using the C library, Go, Python... Where the host runs nscd, the name service cache, the command cannot reach it, since nscd would answer from the host's files and resolver. The system's files are not changed, nothing is left behind, and no root is needed. In a configuration file, the same is written as:

```yaml
rules:
  api.example.com: 80ms
  backup.example.com: timeout
hosts:                            # only to choose the addresses
  api.example.com: fd99::1
```

Names are for `pingmock exec` only. When pingmock runs for the whole machine with `sudo pingmock`, add the names to the machine's `/etc/hosts` and write the rules for the addresses.

### DNS faults

Software also breaks when names stop resolving: a DNS server that fails, a name that disappears, a lookup that hangs. Under `pingmock exec`, a **DNS rule** decides how the command's queries for a name are answered:

```bash
pingmock exec \
  -dns api.example.com=nxdomain \
  -dns 'backup.example.com=servfail for 2 requests; 10ms' \
  -dns github.com=delay=2s \
  -dns slow.example=timeout \
  -- ./my-app
```

The rule is written as for `-rule`, and means:

| Setting | The program sees |
|---|---|
| `20ms`, `delay=`, `jitter=` | The usual answer after the delay: the mocked address for a mocked name, the host resolver's answer for any other |
| `nxdomain` | The name does not exist: `getaddrinfo` fails at once with `EAI_NONAME` ("Name or service not known"), Go with `no such host` |
| `servfail` | The server failed: the resolver tries again, then fails with `EAI_AGAIN` ("Temporary failure in name resolution") |
| `timeout`, `loss=30%` | No answer: the resolver waits its timeout, by default 5 seconds, tries again, then fails with `EAI_AGAIN` |
| `pattern=.ns` | Queries in turn: `.` answered, `x` no answer, `n` NXDOMAIN, `s` SERVFAIL |

`unreachable` and `ttl` have no meaning for DNS and are rejected. A rule covers every query type of its name; `api.example.com/AAAA`, for one type only, is reserved and rejected for now.

A rule works for mocked names (with `-host` or a rule for the name) and for real ones, which are then failed without asking the host. Its name is left out of the command's `/etc/hosts`, which programs read before asking DNS.

Phases count **queries**, for each name and query type apart. A lookup usually asks for A and AAAA at once, and a resolver asks again after a SERVFAIL or a timeout, so how many lookups a phase covers depends on what they get: `nxdomain for 2 requests` fails two lookups, since NXDOMAIN is believed at once; `servfail for 2 requests` fails one, since the C library asks twice by default (`attempts:2` in `resolv.conf`). The statistics, keyed as `api.example.com/A` and `api.example.com/AAAA`, show what happened.

Names a resolver tries with the search domains of `resolv.conf` (`api.example.com.corp.example`) are other names, forwarded as usual. The C library tries them after a name fails, and reports the last answer: where the host has a `search` line, a SERVFAIL or a timeout for `api.example.com` can end as "not found", or even as an address, from the search domain. To see the rule's failure itself, ask for the name with a final dot (`api.example.com.`), or test on a host without search domains.

Delays close to the resolver's timeout behave differently by resolver: the C library still takes an answer that comes after its timeout, if it has asked again in the meantime, and Go's resolver does not.

In a configuration file and through the API:

```yaml
dns:
  api.example.com: nxdomain
  backup.example.com: servfail for 2 requests; 10ms
  github.com: {delay: 2s}
```

```bash
curl -X PUT localhost:9100/dns/api.example.com -d servfail
curl -X DELETE localhost:9100/dns/api.example.com
```

The Go client has `SetDNS`, `DeleteDNS` and `DNSStats`. Through the API, a rule can be set for any name not in the command's `/etc/hosts`; to change the answers for a mocked name later, give it a DNS rule from the start (`-dns api.example.com=0ms`). `GET /rules` lists the DNS rules under `dns`, and their statistics under `dns_stats`: requests, answered, nxdomain, servfail, dropped. Each query for a name with a rule is recorded in the history with `"probe": "dns"`, the `name`, the `qtype` and the result `answer`, `nxdomain`, `servfail` or `drop`; `GET /history?target=api.example.com` returns them. PTR queries are not affected. Randomness comes from the seed, per name and query type.

DNS rules are for `pingmock exec` only: for the whole machine, pingmock cannot get between programs and the machine's resolver, so `sudo pingmock -dns` is refused.

### Requirements

`pingmock exec` without root needs unprivileged user namespaces and a `/dev/net/tun` that everyone may open (`crw-rw-rw-`), which is the default on most distributions. If something is missing, pingmock says what and how to fix it.

**Ubuntu 23.10 and later** restrict unprivileged user namespaces with AppArmor. Rather than lifting the restriction for every program, allow it for pingmock alone with the profile in [`contrib/apparmor/pingmock`](contrib/apparmor/pingmock), the way Ubuntu does for other programs that need it. Edit the path in it if pingmock is not installed as `/usr/local/bin/pingmock`, then:

```bash
sudo cp contrib/apparmor/pingmock /etc/apparmor.d/pingmock
sudo apparmor_parser -r /etc/apparmor.d/pingmock
```

On GitHub Actions' Ubuntu runners, do the same in a step before running tests; [`examples/github-actions`](examples/github-actions/) is a workflow ready to copy that installs pingmock and the profile.

## Docker

The image `ghcr.io/nanmuyao/pingmock` needs `NET_ADMIN` and the TUN device. Containers that share its network get their pings and connections mocked and reach the API at `127.0.0.1:9100`:

```bash
docker run -d --name pingmock --cap-add NET_ADMIN --device /dev/net/tun \
  ghcr.io/nanmuyao/pingmock -rule 10.99.0.1=50ms
docker run --rm --network container:pingmock alpine ping -c 3 10.99.0.1
```

With Docker Compose, put the code under test in pingmock's network with `network_mode: service:pingmock`. [`examples/docker-compose.yml`](examples/docker-compose.yml) shows a complete setup, including a health check so that the app starts only once pingmock is ready:

```bash
docker compose -f examples/docker-compose.yml up --abort-on-container-exit --exit-code-from app
```

[`examples/compose-demo`](examples/compose-demo/) goes further: a service under test reacting live to rules changed from the host, and tests run with `docker compose run`.

Without arguments, the image runs `pingmock -api 0.0.0.0:9100`, so the API can be published with `-p 9100:9100`; arguments replace that default, so add `-api 0.0.0.0:9100` to them to publish the API. The image runs pingmock for its network namespace, as `sudo pingmock` does for a machine, so names, `-host` and `-dns` are refused there: use addresses. Docker disables IPv6 in containers by default, so pingmock runs with IPv4 only unless IPv6 is enabled for the container.

## Runtime control API

Rules can be changed over HTTP at any time during a test, and changes take effect immediately. The request body may use the same text format as `-rule`, or JSON.

```bash
# Show the seed, the default, the rules, the paths and per-target statistics
# (stats: requests, replied, dropped, unreachable, hops, rule, phase), and the
# same for TCP rules (tcp, tcp_stats), UDP rules (udp, udp_stats) and DNS rules (dns, dns_stats)
curl localhost:9100/rules

# Create or change a rule
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,pattern=.x'
curl -X PUT localhost:9100/rules/10.99.0.1 -d '50ms for 3 pings; timeout'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# Delete a rule (the default applies again)
curl -X DELETE localhost:9100/rules/10.99.0.1

# TCP rules: refuse connections to a port, reset accepted ones, then remove a rule
curl -X PUT localhost:9100/tcp/10.99.0.1:5432 -d refuse
curl -X PUT localhost:9100/tcp/10.99.0.2:443 -d 5ms,after=reset
curl -X DELETE localhost:9100/tcp/10.99.0.1:5432

# UDP rules: datagrams to a port are refused (ICMP port unreachable)
curl -X PUT localhost:9100/udp/10.99.0.1:514 -d refuse
curl -X DELETE localhost:9100/udp/10.99.0.1:514

# DNS rules, under pingmock exec: a name stops resolving, then resolves again
curl -X PUT localhost:9100/dns/api.example.com -d nxdomain
curl -X DELETE localhost:9100/dns/api.example.com

# Paths, for traceroute and mtr: give a target a route, then remove it
curl -X PUT localhost:9100/paths/8.8.8.8 -d '192.168.1.1@2ms > * > 72.14.0.1@25ms'
curl -X DELETE localhost:9100/paths/8.8.8.8

# Change the default rule
curl -X PUT localhost:9100/default -d timeout

# Reset the statistics
curl -X DELETE localhost:9100/stats

# Recent requests, oldest first; filter by address or prefix (or, for DNS
# queries, a name), keep the last N
curl 'localhost:9100/history?target=10.99.0.1&limit=20'

# Clear the history
curl -X DELETE localhost:9100/history

# Start over: the rules and default from the command line and configuration file,
# no statistics, no history. Targets restart their scenarios and random sequences.
curl -X POST localhost:9100/reset

# Which pingmock this is: {"version": "vX.Y.Z"}
curl localhost:9100/version
```

`POST /reset` at the start of each test gives it the same state on every run, whatever the tests before it changed.

Each history event looks like this. `result` is `reply`, `drop` or `unreachable`, `code` is present for unreachable answers, `delay` for anything that was answered, and `phase` counts from 1. Connection attempts, DNS queries and the probes of traceroute and mtr add fields, described in [TCP connections](#tcp-connections), [DNS faults](#dns-faults) and [Paths](#paths-traceroute-and-mtr):

```json
{"time": "2026-10-17T11:16:10.113Z", "src": "10.99.0.254", "dst": "10.99.0.1",
 "id": 4242, "seq": 3, "result": "unreachable", "code": "host", "reply": false,
 "delay": "40ms", "rule": "10.99.0.1/32", "phase": 2}
```

All the fields an event can have:

| Field | Present | Values |
|---|---|---|
| `time`, `src`, `dst`, `rule`, `phase`, `reply` | always | `reply` is true for echo replies only; DNS queries have `src` and `dst` 127.0.0.1, the command's DNS server |
| `id`, `seq` | pings | the identifier and sequence number of the echo request |
| `result` | always | pings: `reply`, `drop`, `unreachable`; paths: `time-exceeded`, `port-unreachable`, `syn-ack`; TCP rules: `syn-ack`, `refused`, `drop`, `unreachable`, `sent`, `closed`, `reset`; UDP rules: `refused`, `unreachable`, `drop`; DNS rules: `answer`, `nxdomain`, `servfail`, `drop` |
| `code` | unreachable answers | `host`, `net` or `admin` |
| `delay` | anything answered | such as `40ms` |
| `probe` | anything but a ping | `udp`, `tcp` or `dns` |
| `ttl`, `hop`, `from` | UDP and TCP probes | the probe's TTL, the hop that answered or lost it (left out for the destination), the address the answer came from |
| `port`, `repeat` | TCP and UDP | the destination port; `repeat` is `true` for a SYN sent again, not counted |
| `bytes` | TCP, result `sent` | how many bytes of `send` or `reply` pingmock sent; `delay` is its `wait` |
| `name`, `qtype` | DNS | the name asked for, and `A`, `AAAA`... |

## Using it in automated tests

### Tools and libraries known to work

[`test/e2e-compat.sh`](test/e2e-compat.sh) checks these against pingmock in CI, on Ubuntu 24.04 and 26.04: delays, deterministic loss, unreachable errors and a hijacked real address all come out as the rules say.

| Tool or library | Sockets |
|---|---|
| `ping` from iputils (20240117 on Ubuntu 24.04, 20250605 on 26.04) | raw |
| [fping](https://fping.org) | raw |
| [pro-bing](https://github.com/prometheus-community/pro-bing), Go | raw, and unprivileged ping sockets |
| [icmplib](https://github.com/ValentinBELYN/icmplib), Python | raw, and unprivileged ping sockets |

traceroute and mtr show the hops of targets that have a [path](#paths-traceroute-and-mtr), in UDP, ICMP and TCP mode; CI checks all three. Targets without a path look one hop away, see [Limitations](#limitations).

For [TCP rules](#tcp-connections), the same script connects with the standard libraries of Go (`net.Dial`) and Python (`socket`), and checks that each sees an accepted connection after the delay and then a read timeout, `ECONNREFUSED`, a connect timeout and `EHOSTUNREACH`. curl and bash's `/dev/tcp` are checked in the end-to-end tests.

The easiest way is to run the tests themselves under `pingmock exec`: no root, no setup or teardown code, and every run is isolated. Fix the seed so random loss and jitter are the same on every run.

### Go

Run the test binary inside pingmock with `go test -exec`, and control pingmock from the tests with the [`client`](https://pkg.go.dev/github.com/nanmuyao/pingmock/client) package:

```bash
go test -exec 'pingmock exec -seed 42 -rule 10.99.0.1=20ms -rule 10.99.0.2=200ms --' ./...
```

```go
import "github.com/nanmuyao/pingmock/client"

func TestPicksFastestServer(t *testing.T) {
	pm, err := client.FromEnv()
	if err != nil {
		t.Skip(err) // not running under pingmock exec
	}
	ctx := context.Background()

	if got := pickFastest("10.99.0.1", "10.99.0.2"); got != "10.99.0.1" {
		t.Fatalf("picked %s", got)
	}

	// Network conditions change: the fast link becomes slow.
	if err := pm.SetRule(ctx, "10.99.0.1", "500ms"); err != nil {
		t.Fatal(err)
	}
	if got := pickFastest("10.99.0.1", "10.99.0.2"); got != "10.99.0.2" {
		t.Fatalf("picked %s after the slowdown", got)
	}

	// Retry logic: the first two pings are lost, then the link answers.
	pm.ClearHistory(ctx)
	pm.SetRule(ctx, "10.99.0.3", "timeout for 2 pings; 30ms")
	checkReachable("10.99.0.3")
	events, _ := pm.History(ctx, "10.99.0.3", 0)
	if len(events) != 3 || events[2].Result != "reply" {
		t.Errorf("history = %+v", events)
	}
}
```

The client covers the whole API: rules (`SetRule`, `DeleteRule`, `SetDefault`), paths (`SetPath`, `DeletePath`), TCP, UDP and DNS rules (`SetTCP`, `DeleteTCP`, `SetUDP`, `DeleteUDP`, `SetDNS`, `DeleteDNS`), statistics (`State`, `Stats`, `TCPStats`, `UDPStats`, `DNSStats`, `ResetStats`), the history (`History`, `ClearHistory`), `Reset` to put everything back as at startup (for the beginning of each test) and `Version`; `New`, `FromEnv` and `URL` connect it, and `IsNotFound` tells a missing rule from other errors. See its [documentation](https://pkg.go.dev/github.com/nanmuyao/pingmock/client). [`examples/go-demo`](examples/go-demo/) is a complete example to copy from: a program that picks servers by latency, with tests for timeouts, unreachable servers, loss, slowdowns and outages.

### Other languages

Run the test command under `pingmock exec` and call the [control API](#runtime-control-api) at `$PINGMOCK_API`, for example with pytest:

```bash
pingmock exec -seed 42 -rule 10.99.0.1=20ms -rule 10.99.0.2=200ms -- pytest
```

[`examples/python-demo`](examples/python-demo/) is a complete example, with a pytest fixture you can copy.

```python
import os, requests

API = os.environ["PINGMOCK_API"]

def test_picks_fastest_server():
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # Network conditions change: the fast link becomes slow
    requests.put(f"{API}/rules/10.99.0.1", data="500ms").raise_for_status()
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

def test_retries():
    # The first two pings are lost, then the link answers
    requests.delete(f"{API}/history")
    requests.put(f"{API}/rules/10.99.0.3", data="timeout for 2 pings; 30ms")
    assert my_app.check_reachable("10.99.0.3")
    events = requests.get(f"{API}/history", params={"target": "10.99.0.3"}).json()["events"]
    assert [e["result"] for e in events] == ["drop", "drop", "reply"]
```

If the tests cannot run under `pingmock exec`, start `sudo pingmock` before them instead, or use the [container image](#docker).

### This repository's tests

The unit tests do not need root; the end-to-end checks in [`test/`](test/) mostly do:

```bash
make test         # go vet and unit tests with the race detector
make e2e          # builds pingmock and runs test/e2e.sh with sudo
make e2e-exec     # pingmock exec, rootless and as root
make e2e-compat   # ping, fping, pro-bing, icmplib, and Go's and Python's TCP clients against pingmock (needs fping and icmplib)
make e2e-docker   # the container image and the Compose example
```

On a Mac, or wherever there is no Linux, the [development container](.devcontainer/) in this repository runs all of them but `make e2e-docker`.

[docs/packet-path.md](docs/packet-path.md) follows one ping through the kernel and back, [docs/packets.md](docs/packets.md) takes the packets apart byte by byte, [docs/architecture.md](docs/architecture.md) is a tour of the code, and [CONTRIBUTING.md](CONTRIBUTING.md) explains how to contribute.

## Limitations

- Ping (ICMP and ICMPv6 Echo) is mocked, and TCP connections to ports with a [TCP rule](#tcp-connections) are accepted, refused or left unanswered, and accepted ones can be closed or reset. Under `pingmock exec`, DNS queries for names with a [DNS rule](#dns-faults) get the rule's answer. On a TCP connection pingmock sends only the fixed bytes of a rule's `send` and `reply`, never sends anything twice and never reads what the program sends: the only service it runs is the DNS server it gives a command under `pingmock exec`. Other TCP traffic gets no response, and UDP gets an ICMP error only from a [UDP rule](#udp-datagrams) or, for traceroute and mtr, a path; pingmock never sends a UDP answer.
- Pings bound to a network interface are not mocked: `ping -I eth0 8.8.8.8` leaves through `eth0` whatever the routes say, so it bypasses pingmock. Multi-WAN tools such as OpenWrt's mwan3 track their links this way. Binding to a source *address* (`ping -I 192.0.2.2 8.8.8.8`) is mocked as usual.
- Without a [path](#paths-traceroute-and-mtr), a target looks one hop away to traceroute and mtr: its pings are answered, its TCP probes only on ports with a TCP rule, and its UDP probes not at all. With a path, UDP and TCP probes get the answers that mean "reached", on every port, unless a TCP rule for the port decides otherwise.
- Latency is accurate to within 1ms (a 50ms rule measures about 50.4ms); the extra fraction of a millisecond comes from kernel and scheduling overhead.
- Fragmented requests (e.g. `ping -s 2000`) and IPv6 requests with extension headers are not supported and are ignored.
- Linux only. `pingmock exec` without root needs unprivileged user namespaces (see [Requirements](#requirements)).

## License

[MIT](LICENSE)
