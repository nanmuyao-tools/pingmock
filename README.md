# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

English | [中文](README.zh-CN.md)

Mock ping (ICMP and ICMPv6 Echo) responses on Linux. Set the **delay, jitter, packet loss and unreachable errors** for each target address precisely, and change them while your tests are running.

Use it when the software under test measures network latency with ping and behaves differently depending on the result: picking the fastest server, failing over to another link, deciding whether the network is up, showing a signal-strength indicator, and so on. With pingmock those behaviors become reproducible and testable.

## How it works

pingmock creates a TUN virtual network device and routes test subnets (`10.99.0.0/24` and `fd99::/64` by default), plus any addresses you choose, to it. The kernel hands every echo request sent to those addresses to pingmock, which waits according to the matching rule and then sends back a reply or a destination unreachable error, or drops the request.

The software under test needs **no changes at all**: whether it runs the `ping` command or sends ICMP itself through a raw socket, it gets mocked. The request payload is echoed back unchanged, so tools like `ping` that put a timestamp in the payload report exactly the configured latency.

## Installation

The `ip` command (iproute2) must be available at runtime.

**Download a prebuilt binary** (no Go required) from the [Releases](https://github.com/nanmuyao/pingmock/releases) page, for example:

```bash
curl -L -o pingmock https://github.com/nanmuyao/pingmock/releases/latest/download/pingmock-linux-amd64
chmod +x pingmock
```

**Install with Go** (Go 1.22+, standard library only):

```bash
go install github.com/nanmuyao/pingmock@latest
```

**Build from source:**

```bash
git clone https://github.com/nanmuyao/pingmock.git
cd pingmock
go build -o pingmock .
```

Programs installed with `go install` live in `$(go env GOPATH)/bin`. Since pingmock must run as root, `sudo` may not find it on its `PATH`; use the full path instead, e.g. `sudo $(go env GOPATH)/bin/pingmock`.

## Usage

pingmock needs root privileges (or `CAP_NET_ADMIN`).

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

They can be combined, for example `delay=80ms,jitter=10ms,loss=2%` or `delay=200ms,unreachable=net` (the error arrives after 200ms). `loss` and `pattern` cannot be used together. With a pattern, `unreachable=` selects the code used for `u`.

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

If the software pings a hostname, point that name at an address in the test subnet in `/etc/hosts`, e.g. `10.99.0.1 api.example.com`.

### Configuration file

With many rules, a JSON file is easier to maintain, and it can live next to the tests that use it. See [`examples/pingmock.json`](examples/pingmock.json):

```json
{
  "seed": 42,
  "default": "1ms",
  "rules": {
    "10.99.0.1": "20ms",
    "10.99.0.2": "delay=120ms,jitter=15ms,loss=2%",
    "10.99.0.3": "30ms for 10 pings; timeout for 5s; 30ms",
    "10.99.0.4": {
      "phases": [
        {"spec": "delay=40ms,pattern=....x", "for": "20s"},
        {"spec": {"delay": "400ms", "jitter": "100ms"}, "for": "10s"}
      ],
      "repeat": true
    },
    "10.99.0.5": "delay=50ms,unreachable=admin",
    "8.8.8.8": "timeout"
  }
}
```

```bash
sudo ./pingmock -config examples/pingmock.json
```

A rule is either the same text as `-rule` or a JSON object. The file can also set `dev`, `addr`, `addr6`, `api` and `history`. Flags given on the command line override the file, so one file can serve many tests: `-config base.json -rule 10.99.0.1=500ms` replaces just that rule. Unknown keys are rejected, so typos are caught at startup.

### Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-rule` | | `TARGET=RULE`, repeatable |
| `-default` | `0ms` | Rule used for targets without a matching rule |
| `-config` | | JSON configuration file |
| `-seed` | random | Seed for `loss` and `jitter`; the same seed reproduces the same run |
| `-addr` | `10.99.0.254/24` | IPv4 address and prefix of the virtual device. Every address in the subnet **except this one** is a mocked host |
| `-addr6` | `fd99::fe/64` | IPv6 address and prefix of the virtual device; an empty string disables IPv6 |
| `-dev` | `pingmock0` | Name of the virtual device |
| `-api` | `127.0.0.1:9100` | Listen address of the control API; an empty string disables it |
| `-history` | `1000` | Number of recent requests kept for `GET /history`; `0` disables it |
| `-v` | `false` | Log every request |
| `-version` | | Print the version and exit |

## Runtime control API

Rules can be changed over HTTP at any time during a test, and changes take effect immediately. The request body may use the same text format as `-rule`, or JSON.

```bash
# Show the seed, the rules and per-target statistics (requests, replies, drops, unreachable answers, current phase)
curl localhost:9100/rules

# Create or change a rule
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,pattern=.x'
curl -X PUT localhost:9100/rules/10.99.0.1 -d '50ms for 3 pings; timeout'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# Delete a rule (the default applies again)
curl -X DELETE localhost:9100/rules/10.99.0.1

# Change the default rule
curl -X PUT localhost:9100/default -d timeout

# Reset the statistics
curl -X DELETE localhost:9100/stats

# Recent requests, oldest first; filter by address or prefix, keep the last N
curl 'localhost:9100/history?target=10.99.0.1&limit=20'

# Clear the history
curl -X DELETE localhost:9100/history
```

Each history event looks like this. `result` is `reply`, `drop` or `unreachable`, `code` is present for unreachable answers, `delay` for anything that was answered, and `phase` counts from 1:

```json
{"time": "2026-10-17T11:16:10.113Z", "src": "10.99.0.254", "dst": "10.99.0.1",
 "id": 4242, "seq": 3, "result": "unreachable", "code": "host", "reply": false,
 "delay": "40ms", "rule": "10.99.0.1/32", "phase": 2}
```

## Using it in automated tests

A typical flow: start pingmock with a fixed seed, let the software under test ping the mocked addresses, change the network conditions in the middle of the test, and check how the software reacts.

```python
import subprocess, time, requests

API = "http://127.0.0.1:9100"
pm = subprocess.Popen(["sudo", "./pingmock", "-seed", "42",
                       "-rule", "10.99.0.1=20ms", "-rule", "10.99.0.2=200ms"])
time.sleep(1)
try:
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # Network conditions change: the fast link becomes slow
    requests.put(f"{API}/rules/10.99.0.1", data="500ms")
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

    # Retry logic: the first two pings are lost, then the link answers
    requests.delete(f"{API}/history")
    requests.put(f"{API}/rules/10.99.0.3", data="timeout for 2 pings; 30ms")
    assert my_app.check_reachable("10.99.0.3")
    events = requests.get(f"{API}/history", params={"target": "10.99.0.3"}).json()["events"]
    assert [e["reply"] for e in events] == [False, False, True]
finally:
    pm.terminate()
```

The unit tests do not need root, the end-to-end checks in [`test/e2e.sh`](test/e2e.sh) do:

```bash
make test   # go vet and unit tests with the race detector
make e2e    # builds pingmock and runs test/e2e.sh with sudo
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for how the code is organized.

## Limitations

- Only ping (ICMP and ICMPv6 Echo) is mocked. TCP and UDP traffic to mocked addresses gets no response.
- Latency is accurate to within 1ms (a 50ms rule measures about 50.4ms); the extra fraction of a millisecond comes from kernel and scheduling overhead.
- Fragmented requests (e.g. `ping -s 2000`) and IPv6 requests with extension headers are not supported and are ignored.

## License

[MIT](LICENSE)
