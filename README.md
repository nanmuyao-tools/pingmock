# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

English | [中文](README.zh-CN.md)

Mock ping (ICMP Echo) responses on Linux. Set the **delay, jitter and packet loss** for each target address precisely, and change them while your tests are running.

Use it when the software under test measures network latency with ping and behaves differently depending on the result: picking the fastest server, failing over to another link, deciding whether the network is up, showing a signal-strength indicator, and so on. With pingmock those behaviors become reproducible and testable.

## How it works

pingmock creates a TUN virtual network device and routes a test subnet (`10.99.0.0/24` by default), plus any addresses you choose, to it. The kernel hands every ICMP echo request sent to those addresses to pingmock, which waits according to the matching rule and then sends back a reply, or drops the request.

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

`-rule TARGET=SPEC`, repeatable.

**TARGET** is an IPv4 address or a CIDR prefix, such as `10.99.0.1` or `10.99.0.128/25`. When several rules match, **the most specific prefix wins**.

**SPEC** can be:

| Form | Meaning |
|---|---|
| `120ms`, `120`, `1.5s` | Fixed delay (a bare number means milliseconds) |
| `delay=200ms,jitter=30ms` | Delay uniformly distributed between 170 and 230ms |
| `loss=5%` or `loss=0.05` | 5% of requests get no reply |
| `timeout` | No request gets a reply; same as `loss=100%` |

The keys can be combined, for example `delay=80ms,jitter=10ms,loss=2%`.

### Mocking real addresses

If the software under test pings a hard-coded real address such as `8.8.8.8`, just add a rule for it:

```bash
sudo ./pingmock -rule 8.8.8.8=120ms
```

pingmock routes that address to its virtual device. **Note: while pingmock is running, all traffic from this machine to that address is intercepted**, not only ping. Normal routing comes back when pingmock exits. Running it on a test machine or in a container is recommended.

If the software pings a hostname, point that name at an address in the test subnet in `/etc/hosts`, e.g. `10.99.0.1 api.example.com`.

### Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-rule` | | `TARGET=SPEC`, repeatable |
| `-default` | `0ms` | Spec used for targets without a matching rule |
| `-addr` | `10.99.0.254/24` | Address and prefix of the virtual device. Every address in the subnet **except this one** is a mocked host |
| `-dev` | `pingmock0` | Name of the virtual device |
| `-api` | `127.0.0.1:9100` | Listen address of the control API; an empty string disables it |
| `-v` | `false` | Log every request |
| `-version` | | Print the version and exit |

## Runtime control API

Rules can be changed over HTTP at any time during a test, and changes take effect immediately. The request body may use the same text format as `-rule`, or JSON.

```bash
# Show the current rules and per-target statistics (requests, replies, drops)
curl localhost:9100/rules

# Create or change a rule
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,loss=10%'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# Delete a rule (the default applies again)
curl -X DELETE localhost:9100/rules/10.99.0.1

# Change the default spec
curl -X PUT localhost:9100/default -d timeout

# Reset the statistics
curl -X DELETE localhost:9100/stats
```

## Using it in automated tests

A typical flow: start pingmock, let the software under test ping the mocked addresses, change the latency through the API in the middle of the test, and check how the software reacts.

```python
import subprocess, time, requests

pm = subprocess.Popen(["sudo", "./pingmock", "-rule", "10.99.0.1=20ms", "-rule", "10.99.0.2=200ms"])
time.sleep(1)
try:
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # Network conditions change: the fast link becomes slow
    requests.put("http://127.0.0.1:9100/rules/10.99.0.1", data="500ms")
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

    # The statistics confirm that the software really sent pings
    stats = requests.get("http://127.0.0.1:9100/rules").json()["stats"]
    assert stats["10.99.0.1"]["requests"] > 0
finally:
    pm.terminate()
```

`e2e_test.sh` is a complete end-to-end check:

```bash
go build -o pingmock . && sudo ./e2e_test.sh
```

The unit tests do not need root:

```bash
go test -race ./...
```

## Limitations

- IPv4 only; ICMPv6 ping is not mocked.
- Only ICMP Echo is mocked. TCP and UDP traffic to mocked addresses gets no response.
- Latency is accurate to within 1ms (a 50ms rule measures about 50.4ms); the extra fraction of a millisecond comes from kernel and scheduling overhead.
- Fragmented ICMP packets (e.g. `ping -s 2000`) are not supported and are ignored.

## License

[MIT](LICENSE)
