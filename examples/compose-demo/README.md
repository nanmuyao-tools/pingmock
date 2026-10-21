# Docker Compose example: pingmock in containers

English | [中文](README.zh-CN.md)

This example shows how to run a **service under test** in pingmock's network with Docker Compose. The service is the failover script of the [shell example](../shell-demo/), here running as a monitor that checks the links every 2 seconds.

## What is in it

[`compose.yaml`](compose.yaml) defines three services:

| Service | What it does |
|---|---|
| `pingmock` | runs pingmock, mocking two links as [`pingmock.yaml`](pingmock.yaml) says; the control API is published on the host's `127.0.0.1:9100` |
| `monitor` | **the service under test**: runs `failover.sh` every 2 seconds and logs the link to use |
| `tests` | runs the shell example's tests, `test.sh` |

The key is this line:

```yaml
network_mode: service:pingmock
```

It puts `monitor` and `tests` in the `pingmock` container's network, so their pings and connections are mocked and `127.0.0.1:9100` is the control API. `pingmock`'s health check makes the other two wait until it is ready.

The pingmock container needs `NET_ADMIN` and the TUN device (`cap_add` and `devices`); the containers under test need no special privileges.

## A live demonstration

Start pingmock and the monitor, and follow the monitor's log:

```bash
docker compose up -d monitor
docker compose logs -f monitor
```

```
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
```

In **another terminal**, take the primary link down through the API on the host:

```bash
curl -X PUT localhost:9100/rules/10.99.0.1 -d timeout
```

The monitor switches to the backup at once:

```
monitor-1  | link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms
```

Bring the primary back, and the protection against flapping shows: first "recovering 1/2", and only the next check returns to the primary:

```bash
curl -X PUT localhost:9100/rules/10.99.0.1 -d 20ms
```

```
monitor-1  | link=backup primary recovering (1/2): 3/3 replies, 20ms
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
```

Stop everything when done:

```bash
docker compose down
```

## Running the tests

```bash
docker compose run --rm tests
```

```
PASS uses the primary while it is healthy
...
15 passed, 0 failed
```

`docker compose run` starts `pingmock`, which `tests` depends on, but **not** `monitor`. That matters: the monitor pings the primary every 2 seconds, which would disturb tests such as the one counting the pings sent. So if you started the monitor with `up` before, run `docker compose down` before the tests.

`docker compose run` exits with the tests' status, so this works as is in CI.

## Using it in your own project

1. Copy the `pingmock` service: its `image`, `cap_add`, `devices` and `healthcheck`
2. Give the service under test `network_mode: service:pingmock` and a `depends_on` that waits for pingmock to be healthy
3. Control pingmock from the tests at `http://127.0.0.1:9100`; to control it from the host, have pingmock listen on `0.0.0.0:9100` and publish the port on `127.0.0.1`, as this example does

Note that a service with `network_mode: service:pingmock` cannot have `ports` of its own: publish them on the `pingmock` service instead. Docker also disables IPv6 in containers by default, so pingmock runs with IPv4 only and says so in its log.
