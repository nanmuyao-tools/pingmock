# Testing a ping library: pro-bing under pingmock

English | [中文](README.zh-CN.md)

This example is for people who **write or maintain a ping library**, or any code that sends ICMP itself. Their tests have a problem: the behaviors that matter most, a host that never answers, one that answers "unreachable", packet loss, an odd TTL, cannot be produced on demand with a real network, so they go untested or get tested against `www.google.com` in CI. pingmock produces them on demand, offline, and the same way on every run.

The library under test is [pro-bing](https://github.com/prometheus-community/pro-bing), the most used Go ping library, taken as it is. Nothing in it is mocked; only the network is.

## The hosts

[`pingmock.yaml`](pingmock.yaml) defines one host per behavior:

| Host | Rule | Behavior |
|---|---|---|
| 10.99.0.1 | `20ms` | answers every ping after 20ms |
| 10.99.0.2 | `timeout` | never answers |
| 10.99.0.3 | `unreachable` | answers "destination host unreachable" at once |
| 10.99.0.4 | `delay=10ms,pattern=.x` | answers one ping, loses the next, and so on |
| 10.99.0.5 | `delay=10ms,ttl=128` | answers with TTL 128, as a Windows host would |

## Running the tests

[Install pingmock](../../README.md#installation) first. Then, in this directory:

```bash
go test -exec 'pingmock exec -config pingmock.yaml --' ./...
```

`go test -exec` hands the test binary to `pingmock exec`, which runs it in a private network where those five hosts exist. No root is needed, even though the library opens a raw socket: inside `pingmock exec` the test process is root in its own user namespace. Outside pingmock (a plain `go test ./...`), the tests skip themselves.

## What each test shows

| Test | What it checks | What it shows about pro-bing |
|---|---|---|
| `TestRepliesGiveTheRtt` | 4 replies, each about 20ms; pingmock saw 4 requests | the RTTs come out as the rule says, and the [request history](../../README.md#runtime-control-api) confirms what the library really sent |
| `TestTimeoutEndsTheRun` | `Run` returns when `Timeout` is over, with no replies | a timeout is reported through the statistics, **not as an error**: `Run` returns `nil`. This is the behavior discussed in [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70); whichever way a library decides, a test like this one pins it down |
| `TestUnreachableLooksLikeLoss` | no replies, and the run lasts until the timeout | pro-bing only counts echo replies, so a host that rejects pings is indistinguishable from one that is silent. pingmock's history shows the difference: every request was answered, with `unreachable` |
| `TestLossIsCounted` | 6 sent, 3 received, 50% loss, on every run | `Count` counts replies, so with loss the run lasts until the timeout; the deterministic `pattern` makes the numbers exact |
| `TestTTLIsReported` | `OnRecv` and `Statistics().TTLs` both say 128 | the TTL of the reply reaches the caller unchanged |

Each test pings only its own host, so they do not interfere, and none of them needs the control API to change anything: the configuration file is the whole test fixture.

## Using it for your own library

Copy `pingmock.yaml` and the two helpers at the top of [`probing_test.go`](probing_test.go), then write a test per behavior your library should handle. The rules you will want most:

- `timeout` and `unreachable` (also `unreachable=net`, `unreachable=admin`) for the error paths
- `pattern=..x` for exact loss, `loss=10%` with a `seed` for random but reproducible loss
- `delay=50ms,jitter=20ms` for RTT statistics
- `30ms for 3 pings; timeout` for a host that goes down mid-run, see [scenarios](../../README.md#scenarios-rules-that-change-over-time)
- `ttl=1` to `ttl=255`

The [GitHub Actions example](../github-actions/) shows how to run such tests in CI.
