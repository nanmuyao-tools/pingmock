# Developing in a container

English | [中文](README.zh-CN.md)

pingmock runs on Linux only, and its end-to-end checks need root, a TUN device and iproute2. This directory is a [development container](https://containers.dev) with all of that, so that the work can be done on a Mac, or on any machine with Docker: the repository is mounted in the container, commands run in there, and a file changed on either side is changed on the other at once.

## What is inside

Go (the version is pinned in the [`Dockerfile`](Dockerfile)), iproute2, `ping`, `curl`, traceroute, mtr, fping, Python with icmplib and pytest, shellcheck, git, and Claude Code. The container runs as the unprivileged user `dev`, who can `sudo` without a password for the checks that need root.

## Opening it

With VS Code: install the Dev Containers extension, open the repository, and choose *Reopen in Container*. With the [devcontainer CLI](https://github.com/devcontainers/cli):

```bash
npx @devcontainers/cli up --workspace-folder .
npx @devcontainers/cli exec --workspace-folder . make test
```

Use a current CLI: 0.89.0 works, while 0.80.0 hangs after starting the container on Docker Desktop 4.68, waiting for a `docker events` line that never comes.

Or with Docker alone, which is how CI uses it:

```bash
docker build -t pingmock-dev -f .devcontainer/Dockerfile .
docker run --rm -it --cap-add NET_ADMIN --cap-add SYS_ADMIN --device /dev/net/tun \
  -v "$PWD:/workspace" -v pingmock-gomod:/go/pkg/mod -v pingmock-cache:/home/dev/.cache \
  -w /workspace pingmock-dev
```

The two volumes keep the Go module and build caches, so that the second build of anything is incremental. The image builds in a few minutes the first time and in seconds after that.

## How long things take

Measured on a Mac with Docker Desktop. The end-to-end checks spend their time waiting for timeouts and for mtr, not computing, so a faster machine does not change them much.

| Command | Time | Use it when |
|---|---|---|
| `go test ./internal/rule/... ./internal/engine/... ./internal/icmp/... ./internal/dns/... ./internal/config/... ./internal/api/... ./client/...` on the Mac itself | seconds | Changing anything but `internal/app` and `internal/tun`, which build on Linux only |
| `make test` | about 15 s | Any change |
| `make e2e` | about 25 s | Packets, rules, routes, paths |
| `make e2e-exec` | about 30 s | `pingmock exec`, names, DNS |
| `make e2e-compat` | about 40 s the first time, then 20 s | The `icmp` and `engine` packages, which the tools and libraries exercise |

## What runs where

| Check | In the container | Notes |
|---|---|---|
| `make lint`, `make test` | yes | |
| `make e2e` | yes | With IPv6 on Docker Desktop, which enables it in containers. Docker on Linux does not by default, and the IPv6 checks are then skipped |
| `make e2e-exec` | yes | Rootless and as root |
| `make e2e-compat` | yes | |
| `make e2e-docker` | no | Needs Docker. CI runs it |
| Ubuntu's AppArmor restriction on user namespaces | no | The container has no AppArmor. CI's `test` job covers the profile in [`contrib/apparmor`](../contrib/apparmor/) |

CI builds this container and runs the first four inside it on every push, so that it stays usable.

## The privileges, and why

[`devcontainer.json`](devcontainer.json) gives the container `NET_ADMIN` and `/dev/net/tun`, which pingmock needs, and `SYS_ADMIN`, which `pingmock exec` needs in a container: Docker's default seccomp profile lets a container create namespaces only when it has that capability, and `exec` run as root creates a network namespace, which takes the capability itself. Nothing else is needed, neither `--privileged` nor turning seccomp off (verified with Docker Desktop 4.68 on a Mac).

## Differences from a Linux machine

- Docker Desktop's resolver answers every name, even one that does not exist, with a made-up address. The check that an unknown name does not resolve under `pingmock exec` skips there, and says so.
- `/proc/sys` is read-only in a container. The compatibility check writes `ping_group_range` only when it has to, and containers already allow ping sockets for every group.
- Timings are a little noisier in the virtual machine than on a Linux host: a check with a tight tolerance can fail once and pass when run again.

## Claude Code

`claude` is installed for the user `dev`. Its login and settings are kept in a volume, so they survive a rebuild of the container; the host's `~/.claude` is not shared with it.
