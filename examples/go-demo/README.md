# Go example: testing a program that picks servers by latency

English | [中文](README.zh-CN.md)

This example shows how to test Go code that **makes decisions from ping latency** with pingmock. The code under test knows nothing about pingmock and is not changed for the tests, and no root is needed.

## The code under test

[`fastest.go`](fastest.go) is a common case: it measures the latency of a few servers with the system's `ping` command, picks the fastest, and moves to another one when the current server gets slow or stops answering.

| Function | What it does |
|---|---|
| `Measure` | pings one server a few times and works out the average latency, the loss and the "unreachable" errors |
| `Pick` | measures several servers at once and returns the fastest usable one (losing at most 25% of the pings) |
| `Selector.Check` | keeps the current server while it stays good, and picks again otherwise |
| `Connect` | opens a TCP connection to the first of the servers that accepts one, moving on when a connection is refused or times out |

[`cmd/fastest`](cmd/fastest/main.go) is a command that uses it.

## Running the tests

[Install pingmock](../../README.md#installation) first. Then, in this directory:

```bash
go test -exec 'pingmock exec -config pingmock.yaml --' ./...
```

`go test -exec` hands the compiled test binary to `pingmock exec`, so the tests run in pingmock's private network: [`pingmock.yaml`](pingmock.yaml) sets up three mocked servers, and the tests add other network conditions as they need them through pingmock's [Go client](https://pkg.go.dev/github.com/nanmuyao-tools/pingmock/client).

Outside pingmock (a plain `go test ./...`, say), the tests skip themselves instead of failing.

## What each test shows

| Test | Network conditions | pingmock feature |
|---|---|---|
| `TestPicksFastest` | three servers at 80ms, 20ms and 50ms | rules in the [configuration file](../../README.md#configuration-file) |
| `TestSkipsUnreachableServer` | the fastest server answers "destination unreachable" | `unreachable` |
| `TestSkipsServerThatTimesOut` | a server does not answer | `timeout` |
| `TestAvoidsLossyServer` | a server loses every other ping | `pattern=.x` (deterministic loss, the same on every run) |
| `TestSwitchesWhenCurrentServerSlowsDown` | the current server slows down in the middle of the test | changing a rule while running, with the client |
| `TestSwitchesWhenCurrentServerGoesDown` | a server answers 3 pings, then goes down | a [scenario](../../README.md#scenarios-rules-that-change-over-time): `15ms for 3 pings; timeout` |
| `TestSendsConfiguredNumberOfPings` | — | the [request history](../../README.md#runtime-control-api), to check how many pings the code under test really sent |
| `TestConnectFailsOverWhenTheConnectionTimesOut` | a database never answers the connection, so `connect` waits its timeout | a [TCP rule](../../README.md#tcp-connections): `timeout` |
| `TestConnectFailsOverAtOnceWhenRefused` | a database is down and refuses the connection at once | a TCP rule, `refuse`, and the history, to check that the code tried once |

The tests do not get in each other's way: each uses its own addresses and removes the rules it added with `t.Cleanup`.

## Running the command

Running `cmd/fastest` in pingmock's network shows plainly how network conditions change what the program chooses:

```bash
go build -o fastest ./cmd/fastest
pingmock exec -config pingmock.yaml -- ./fastest 10.99.0.1 10.99.0.2 10.99.0.3
```

```
10.99.0.2         21.1ms  loss   0%
10.99.0.3         51.2ms  loss   0%
10.99.0.1         80.5ms  loss   0%
fastest: 10.99.0.2
```

Without changing any code, make the fastest server lose pings, and the program picks another one:

```bash
pingmock exec -config pingmock.yaml -rule '10.99.0.2=delay=20ms,pattern=.x' -- ./fastest 10.99.0.1 10.99.0.2 10.99.0.3
```

```
10.99.0.3         50.5ms  loss   0%
10.99.0.1         80.7ms  loss   0%
10.99.0.2         20.4ms  loss  33%
fastest: 10.99.0.3
```

**Build first, then run.** Programs inside `pingmock exec` have no network access beyond the mocked targets, so a `go run` there that needs to download dependencies fails with `network is unreachable`. Running `go build` (or `go mod download`) outside first avoids that. `go test -exec` always builds first, so it is not affected.

## Using it in your own project

1. In the tests, connect to pingmock with `client.FromEnv()` and `t.Skip` on error, as the `pingmock` function in [`fastest_test.go`](fastest_test.go) does
2. Write a configuration file with the network the tests start from; add what each test needs with `SetRule` (`SetTCP` for connections) and remove it with `t.Cleanup`
3. Run the tests with `go test -exec 'pingmock exec -config pingmock.yaml --' ./...`, in CI as well. [`examples/github-actions`](../github-actions/) is a workflow ready to copy

The configuration sets `seed: 42`, so even random loss and jitter give the same results on every run.
