# Shell example: testing a link failover script

English | [中文](README.zh-CN.md)

This example shows how to test an **operations script that depends on ping results** with pingmock. Both the script under test and its tests use only `bash`, `ping` and `curl`: no programming language, and no root.

## The script under test

[`failover.sh`](failover.sh) is common failover logic: it checks a primary and a backup link and decides which one to use.

| Output | When | Exit status |
|---|---|---|
| `link=primary` | the primary is healthy: at least 2 of 3 pings answered, on average within 150ms | 0 |
| `link=backup` | the primary is unhealthy and the backup healthy | 1 |
| `link=none` | neither is healthy | 2 |

With `-s STATE_FILE` it remembers the link in use and **does not flap**: after switching to the backup, it only goes back once the primary has been healthy for **two checks in a row**. A primary that keeps going up and down does not make the script go back and forth with it.

Environment variables adjust the thresholds: `COUNT` (pings per check), `MIN_REPLIES`, `MAX_MS` and `RECOVER_CHECKS`.

## Running the tests

[Install pingmock](../../README.md#installation) first. Then, in this directory:

```bash
pingmock exec -config pingmock.yaml -- ./test.sh
```

```
PASS uses the primary while it is healthy
PASS switches to the backup when the primary does not answer
...
15 passed, 0 failed
```

Outside pingmock, the tests print `SKIP` and exit successfully.

## What each test shows

Each test in [`test.sh`](test.sh) starts with `network PRIMARY_RULE BACKUP_RULE`, **stating the whole network it needs**, so each test shows at a glance what it simulates, and the tests do not depend on each other.

| Test | Network conditions | pingmock feature |
|---|---|---|
| uses the primary while it is healthy | both links work | a fixed delay |
| switches to the backup when the primary does not answer | the primary is silent | `timeout` |
| switches to the backup when the primary is unreachable | the primary answers "destination unreachable" | `unreachable` |
| switches to the backup when the primary is slow | the primary answers every ping, slowly | a fixed delay of `300ms` |
| keeps / leaves a primary that loses pings | the primary loses one / two pings in three | `pattern=..x`, `pattern=.xx` (deterministic loss, right at the threshold) |
| reports no link when both are down | neither answers | `timeout` |
| recovery | the primary is down for one check, then recovers | a [scenario](../../README.md#scenarios-rules-that-change-over-time): `timeout for 3 pings; 20ms` |
| flapping primary | the primary keeps going down and up | a repeating scenario: `timeout for 3 pings; 20ms for 3 pings; repeat` |
| sends 3 pings to the primary and none to the backup | — | the [request history](../../README.md#runtime-control-api), to check how many pings the script really sent |

The recovery and flapping tests show what scenarios are for: each check sends exactly 3 pings, so the `for 3 pings` phases line up with the script's runs and the results are the same every time.

These tests catch real mistakes. Turn off the protection against flapping (`RECOVER_CHECKS=1`), and exactly the 3 tests that check it fail:

```
FAIL recovery: waits for a second healthy check: got "link=primary primary healthy: 3/3 replies, 20ms" (exit 0), want link=backup (exit 1) mentioning "recovering (1/2)"
FAIL flapping primary: stays on the backup (check 2): got "link=primary ..." (exit 0), want link=backup (exit 1)
FAIL flapping primary: stays on the backup (check 4): got "link=primary ..." (exit 0), want link=backup (exit 1)
12 passed, 3 failed
```

## Running the script

```bash
pingmock exec -config pingmock.yaml -- ./failover.sh 10.99.0.1 10.99.0.2
# link=primary primary healthy: 3/3 replies, 20ms

pingmock exec -config pingmock.yaml -rule 10.99.0.1=timeout -- ./failover.sh 10.99.0.1 10.99.0.2
# link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms

pingmock exec -config pingmock.yaml -rule 10.99.0.1=unreachable -rule 10.99.0.2=timeout -- ./failover.sh 10.99.0.1 10.99.0.2
# link=none primary unhealthy (unreachable); backup unhealthy (no reply)
```

The primary goes down and recovers; three checks in a row with a state file show the protection against flapping:

```bash
pingmock exec -config pingmock.yaml -rule '10.99.0.1=timeout for 3 pings; 20ms' -- \
  sh -c 'for i in 1 2 3; do ./failover.sh -s /tmp/link.state 10.99.0.1 10.99.0.2; done'
```

```
link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms
link=backup primary recovering (1/2): 3/3 replies, 20ms
link=primary primary healthy: 3/3 replies, 20ms
```

## Using it for your own scripts

1. In the tests, set network conditions with `curl -X PUT "$PINGMOCK_API/rules/ADDRESS" -d RULE`, and see the pings the script sent with `curl "$PINGMOCK_API/history?target=ADDRESS"`
2. Run them with `pingmock exec -- ./your-test.sh`; pingmock exits with the test script's status, so this works as is in CI
3. If the script pings real addresses (8.8.8.8, say), just give them rules; mocking inside `pingmock exec` does not affect anything else on the machine
