# Python example: testing a module that tells whether the network works

English | [中文](README.zh-CN.md)

This example shows how to test Python code that **depends on ping results** with pingmock. The code under test knows nothing about pingmock and is not changed for the tests, and no root is needed. The tests use only pytest and the standard library, calling pingmock's control API over HTTP; no Go is involved.

## The code under test

[`netcheck.py`](netcheck.py) runs the system's `ping` against two public DNS servers, 8.8.8.8 and 1.1.1.1, at once, and tells the state of the connection:

| State | When | Exit status of the command |
|---|---|---|
| `online` | at least one answers within 200ms and loses at most 20% of the pings | 0 |
| `degraded` | they answer, but only too slowly or with too much loss | 1 |
| `offline` | neither answers | 2 |

The code pings **real public addresses**. Inside `pingmock exec`, mocking 8.8.8.8 affects only the program under test; everything else on the machine still reaches the real 8.8.8.8.

## Running the tests

[Install pingmock](../../README.md#installation) first. Then, in this directory:

```bash
pip install -r requirements.txt      # outside first: there is no network inside pingmock exec
pingmock exec -config pingmock.yaml -- python3 -m pytest -v
```

`pingmock exec` runs pytest in pingmock's private network and tells it the control API's address in the `PINGMOCK_API` environment variable. [`pingmock.yaml`](pingmock.yaml) makes both servers answer quickly, and each test changes what it needs.

Outside pingmock (a plain `python3 -m pytest`, say), the tests skip themselves instead of failing.

## What each test shows

| Test | Network conditions | pingmock feature |
|---|---|---|
| `test_online_when_hosts_answer_quickly` | both answer quickly | rules in the [configuration file](../../README.md#configuration-file) |
| `test_online_while_one_host_is_down` | 8.8.8.8 does not answer, 1.1.1.1 does | `timeout` |
| `test_degraded_when_every_host_is_slow` | both are slow, one with jitter | `delay=300ms,jitter=20ms` |
| `test_degraded_when_every_host_loses_pings` | both lose every third ping | `pattern=..x` (deterministic loss, the same on every run) |
| `test_offline_when_unreachable_is_reported_quickly` | both answer "destination unreachable", **and quickly** | `unreachable`, `unreachable=admin` |
| `test_offline_when_nothing_answers` | neither answers | `timeout` |
| `test_notices_when_the_connection_drops` | they answer 3 pings, then go silent | a [scenario](../../README.md#scenarios-rules-that-change-over-time): `15ms for 3 pings; timeout` |
| `test_pings_each_host_the_given_number_of_times` | — | the [request history](../../README.md#runtime-control-api), to check how many pings the code really sent |

Note the difference between unreachable and timeout: both mean offline, but an unreachable error comes back at once, so the fifth test also checks that the whole check takes less than a second.

## The test helper

[`conftest.py`](conftest.py) has a pytest fixture, `pingmock`: a small client written with the standard library alone, ready to copy into your own project:

```python
def test_something(pingmock):
    pingmock.set_rule("8.8.8.8", "timeout")    # for this test only
    ...
    events = pingmock.history("8.8.8.8")       # the pings the code under test sent
```

Before each test it calls `POST /reset`, so **every test starts from the rules in the configuration file**, with no statistics or history, whatever the tests before it changed. The code under test pings fixed addresses that the configuration file has rules for; a reset puts those rules back, where deleting a changed rule would leave the default in its place.

## Running the command

```bash
pingmock exec -config pingmock.yaml -- python3 netcheck.py
```

```
8.8.8.8           15.4ms  loss   0%
1.1.1.1           20.7ms  loss   0%
online: 8.8.8.8 answers in 15ms
```

Other network conditions, without changing any code:

```bash
pingmock exec -config pingmock.yaml -rule 8.8.8.8=350ms -rule '1.1.1.1=delay=500ms,pattern=..x' -- python3 netcheck.py
```

```
8.8.8.8          351.0ms  loss   0%
1.1.1.1          500.5ms  loss  33%
degraded: best is 8.8.8.8: 351ms, 0% lost
```

```bash
pingmock exec -config pingmock.yaml -rule 8.8.8.8=unreachable -rule 1.1.1.1=unreachable=admin -- python3 netcheck.py
```

```
8.8.8.8         no reply (3 errors)
1.1.1.1         no reply (3 errors)
offline: no host answers (6 unreachable errors)
```

## Using it in your own project

1. Copy [`conftest.py`](conftest.py) into your tests' directory; tests get the client by taking a `pingmock` argument
2. Write a configuration file with the network the tests start from
3. Install the dependencies first, then run the tests with `pingmock exec -config pingmock.yaml -- python3 -m pytest`, in CI as well. [`examples/github-actions`](../github-actions/) is a workflow ready to copy

The same approach works in any language: read the address from `PINGMOCK_API` and call the [control API](../../README.md#runtime-control-api) with that language's HTTP library.
