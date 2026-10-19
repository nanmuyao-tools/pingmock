# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

[English](README.md) | 中文

在 Linux 上模拟 ping（ICMP 和 ICMPv6 Echo）的响应，可以精确设定每个目标地址的**延迟、抖动、丢包率和不可达错误**，并在运行中随时修改。

![一个 ping 三台服务器并选出最快者的程序，随着 pingmock 改变各台的延迟，每次选出不同的服务器](docs/demo/demo.svg)

用 ping 探测网络的软件会根据结果做出不同的行为：选最快的服务器、切换线路、判断网络是否可用、显示信号强度。这些行为很难测试，因为真实网络不会刚好处在测试需要的状态。pingmock 让它们可以稳定复现：被测程序照常调用 `ping`（或自己发送 ICMP），一行代码不用改，得到的却是你设定的结果。

最短的例子，不需要 root：

```bash
pingmock exec -rule 10.99.0.1=50ms -rule 10.99.0.2=unreachable -- ./my-app
```

和 `tc netem` 相比：规则按**目标地址**设置，而不是按网卡；可以通过 HTTP API **在程序运行中修改**；`pingmock exec` **不需要 root**，也不影响宿主机的网络。

**用 Docker 马上试一试**，什么都不用安装。pingmock 让 10.99.0.1 前三个 ping 在 20ms 后回复、之后变成 300ms，并让 10.99.0.2 返回"目标不可达"：

<!-- docker-demo -->
```bash
docker run --rm --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh ghcr.io/nanmuyao/pingmock -c '
  pingmock -addr6= -rule "10.99.0.1=20ms for 3 pings; 300ms" -rule 10.99.0.2=unreachable &
  until wget -q -O /dev/null 127.0.0.1:9100/rules 2>/dev/null; do kill -0 $! 2>/dev/null || exit 1; sleep 0.1; done
  ping -c 6 -i 0.5 10.99.0.1; ping -c 2 -i 0.5 10.99.0.2'
```
<!-- /docker-demo -->

输出（节选）：pingmock 先打印生效的规则；第 4 个 ping 从 20ms 变成了 300ms；10.99.0.2 立即被报告为不可达：

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

为了简短，这里把 pingmock 和 `ping` 放在同一个容器里。测试自己的程序时，可以用 [`pingmock exec`](#不用-root-运行pingmock-exec) 运行它，或者像 [`examples/compose-demo`](examples/compose-demo/) 那样用 Docker Compose 让它加入 pingmock 的网络。

## 原理

pingmock 创建一块 TUN 虚拟网卡，把测试网段（默认是 `10.99.0.0/24` 和 `fd99::/64`）以及你指定的地址路由到它上面。内核会把发往这些地址的 echo 请求交给 pingmock，pingmock 按匹配的规则等待一段时间后回复，或者返回"目标不可达"错误，或者直接丢弃。

因此被测软件**不需要任何修改**：无论它调用 `ping` 命令，还是自己用原始套接字发送 ICMP，都会被模拟。回复会原样带回请求中的负载，所以 `ping` 这类在负载里写入时间戳的工具，计算出的延迟就是设定值。

## 安装

运行时需要 `ip` 命令（iproute2）。

**下载编译好的程序**（不需要安装 Go）：在 [Releases](https://github.com/nanmuyao/pingmock/releases) 页面下载对应架构的文件，例如：

```bash
curl -L -o pingmock https://github.com/nanmuyao/pingmock/releases/latest/download/pingmock-linux-amd64
chmod +x pingmock
```

**用 Go 安装**（需要 Go 1.22+）：

```bash
go install github.com/nanmuyao/pingmock@latest
```

**容器镜像**（amd64 和 arm64），参见 [Docker](#docker)：

```bash
docker pull ghcr.io/nanmuyao/pingmock
```

**从源码构建：**

```bash
git clone https://github.com/nanmuyao/pingmock.git
cd pingmock
go build -o pingmock .
```

用 `go install` 安装的程序在 `$(go env GOPATH)/bin` 下。因为需要 root 权限运行，`sudo` 时可能找不到它，可以写完整路径，例如 `sudo $(go env GOPATH)/bin/pingmock`。

## 使用

pingmock 有两种运行方式：

- `sudo pingmock [参数]` 为整台机器模拟 ping，直到你停止它。本节其余部分介绍的就是这种方式。
- `pingmock exec [参数] -- 命令` 在一个独立的网络里运行命令，该命令的 ping 会被模拟。这种方式**不需要 root**，也不影响宿主机的网络，最适合测试和 CI。参见[不用 root 运行](#不用-root-运行pingmock-exec)。

第一种方式需要 root 权限（或 `CAP_NET_ADMIN`）。

```bash
sudo ./pingmock \
  -rule 10.99.0.1=50ms \
  -rule 10.99.0.2=delay=200ms,jitter=30ms \
  -rule 10.99.0.3=loss=20% \
  -rule 10.99.0.4=timeout
```

然后在另一个终端：

```bash
ping 10.99.0.1   # 约 50ms
ping 10.99.0.2   # 170–230ms 之间随机
ping 10.99.0.3   # 约 20% 丢包
ping 10.99.0.4   # 全部超时
ping 10.99.0.9   # 没有规则，使用默认值（立即回复）
```

按 Ctrl+C 退出后，虚拟网卡和相关路由会自动删除。

### 规则格式

`-rule 目标=规则`，可以重复使用。

**目标**是 IPv4 或 IPv6 的地址或网段，如 `10.99.0.1`、`10.99.0.128/25`、`fd99::1`。多条规则匹配时，**网段最精确的那条生效**。

**规则**由以下设置组成：

| 写法 | 含义 |
|---|---|
| `120ms`、`120`、`1.5s` | 固定延迟（纯数字表示毫秒） |
| `delay=200ms,jitter=30ms` | 延迟在 170–230ms 之间均匀随机 |
| `loss=5%` 或 `loss=0.05` | 每个请求有 5% 的概率不回复 |
| `pattern=..x` | 确定性的结果，按顺序循环：`.` 回复，`x` 丢弃，`u` 返回目标不可达（这里是每 3 个请求丢第 3 个） |
| `unreachable` | 不正常回复，而是返回 ICMP"目标不可达"（主机不可达） |
| `unreachable=net` / `unreachable=admin` | 网络不可达 / 被管理策略禁止（防火墙通常会发送这种） |
| `timeout` | 全部不回复，等同于 `loss=100%` |
| `ttl=128` | 回复的 TTL（IPv6 为 hop limit），1 到 255，`ping` 会显示为 `ttl=128`。默认 64，和 Linux 主机发出的一样。有些软件据此推测对方的系统或距离 |

各项可以组合，例如 `delay=80ms,jitter=10ms,loss=2%`、`delay=200ms,unreachable=net`（200ms 后才返回错误）或者 `delay=30ms,ttl=128`。`loss` 和 `pattern` 不能同时使用。和 `pattern` 一起使用时，`unreachable=` 只用来指定 `u` 返回哪种原因。

### 不可达与超时

很多软件对这两种失败的处理不一样：不可达错误会立即返回，而请求丢失时软件要一直等到超时。`ping` 的显示如下：

| 规则 | IPv4 | IPv6 |
|---|---|---|
| `unreachable` | `Destination Host Unreachable` | `Destination unreachable: Address unreachable` |
| `unreachable=net` | `Destination Net Unreachable` | `Destination unreachable: No route` |
| `unreachable=admin` | `Packet filtered` | `Destination unreachable: Administratively prohibited` |
| `timeout` | （没有响应） | （没有响应） |

错误消息的来源是目标地址本身（`From 10.99.0.3 icmp_seq=1 Destination Host Unreachable`），并附带原始请求，所以 ping 等工具能把它和自己发出的请求对应起来。

### 场景：随时间变化的规则

一条规则可以由多个**阶段**组成，用 `;` 分隔。每个阶段持续一段时间（`for 10s`）或一定数量的请求（`for 5 pings`），最后一个阶段会一直保持。如果以 `repeat` 结尾，则会从头循环：

```bash
# 前 10 个 ping 正常，然后断网 5 秒，然后恢复
sudo ./pingmock -rule '10.99.0.1=30ms for 10 pings; timeout for 5s; 30ms'

# 线路时好时坏：好 20 秒，差 10 秒，一直循环
sudo ./pingmock -rule '10.99.0.2=40ms for 20s; delay=400ms,jitter=100ms for 10s; repeat'
```

按时间切换的阶段从规则设置的那一刻开始计时；按请求数切换的阶段，对每个目标地址分别计数。重新设置规则（例如通过 API）会让它从第一个阶段重新开始。

如果测试要求每次结果都一样，优先使用按请求数切换的阶段（`for 5 pings`），因为它不受测试运行快慢的影响。

### 可复现的随机性

`loss` 和 `jitter` 是随机的，但可以复现。每个目标地址有自己独立的随机序列，由**种子**和地址共同决定。所以发往某个地址的请求，无论和发往其他地址的请求怎样交错，得到的结果都相同。

不指定 `-seed` 时，pingmock 会随机选一个种子，并在启动时打印出来：

```
seed 4871203317 (pass -seed 4871203317 to reproduce this run)
```

测试失败时，用这个种子重新运行，就能得到完全相同的丢包和延迟。在 CI 里建议固定种子（例如 `-seed 42`），让每次运行的行为都一致。

### IPv6

IPv6 的用法完全相同。虚拟网卡的 IPv6 地址是 `fd99::fe/64`，所以 `fd99::1`、`fd99::2` 等都是模拟主机；规则、API 和配置文件都支持 IPv6 地址和网段：

```bash
sudo ./pingmock -rule fd99::1=30ms -rule 'fd99::2=delay=10ms,pattern=..u' -rule 2001:db8::8888=timeout
ping -6 fd99::1
```

如果系统内核没有启用 IPv6，pingmock 会打印 `IPv6 is unavailable, continuing with IPv4 only`，然后只用 IPv4 运行，此时 IPv6 目标的规则会被拒绝。如果明确指定了 `-addr6`，IPv6 不可用就会报错退出；`-addr6 ""` 则会关闭 IPv6。

### 模拟真实地址

如果被测软件 ping 的是写死的真实地址（比如 `8.8.8.8` 或 `2001:4860:4860::8888`），直接为它设置规则即可：

```bash
sudo ./pingmock -rule 8.8.8.8=120ms
```

pingmock 会把这个地址路由到虚拟网卡上。**注意：在 pingmock 运行期间，本机发往该地址的所有流量都会被拦截**（不只是 ping），退出后自动恢复。建议在测试机或容器里使用。

规则也可以覆盖本机所在的整个网络，比如 VLAN 网卡上的 `192.168.20.0/24`。这时 pingmock 会把它拆成 `192.168.20.0/25` 和 `192.168.20.128/25` 两半来添加路由：它们更精确，所以会接管流量，同时**保留本机原有的这个网络的路由**，pingmock 退出后本机依然能访问这个网络。这样做时 pingmock 会在日志中说明。单个地址如果已经有自己的路由，就无法这样拆分，pingmock 会拒绝这条规则，而不是替换那条路由。`pingmock exec` 不涉及这些问题，因为它从不改动宿主机的路由。

如果软件 ping 的是域名，可以在 `/etc/hosts` 里把域名指向测试网段的地址，例如 `10.99.0.1 api.example.com`。

### 配置文件

规则较多时，用配置文件更容易维护，还可以和测试代码放在一起。配置文件使用 YAML 格式，可以写注释。参见 [`examples/pingmock.yaml`](examples/pingmock.yaml)：

```yaml
seed: 42           # 每次运行的随机丢包和抖动都相同
default: 1ms       # 没有匹配规则的目标使用这个规则

rules:
  10.99.0.1: 20ms
  10.99.0.2: delay=120ms,jitter=15ms,loss=2%
  10.99.0.3: 30ms for 10 pings; timeout for 5s; 30ms   # 断开一会儿，然后恢复
  10.99.0.4:                                           # 一直时好时坏
    phases:
      - spec: delay=40ms,pattern=....x
        for: 20s
      - spec: {delay: 400ms, jitter: 100ms}
        for: 10s
    repeat: true
  10.99.0.5: delay=50ms,unreachable=admin
  10.99.0.6: delay=25ms,ttl=128                        # 像 Windows 主机那样回复
  fd99::1: 30ms                                        # IPv6 地址不需要加引号
  8.8.8.8: timeout
```

```bash
sudo ./pingmock -config examples/pingmock.yaml
```

每条规则可以写成和 `-rule` 相同的文本、一个毫秒数，或者一个映射：包含 `delay`、`jitter`、`loss`（`0.05` 或 `5%`）、`pattern`、`unreachable` 和 `ttl`，或者包含 `phases` 和 `repeat`。配置文件还可以设置 `dev`、`addr`、`addr6`、`api` 和 `history`。

出错时会提示所在的行号；未知的字段会直接报错，拼写错误在启动时就能发现。早期版本使用的 JSON 配置文件无需修改即可继续使用，因为 JSON 本身就是合法的 YAML。

命令行参数会覆盖配置文件，所以一个配置文件可以用于多个测试：`-config base.yaml -rule 10.99.0.1=500ms` 只替换这一条规则。

### 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-rule` | | `目标=规则`，可重复 |
| `-default` | `0ms` | 没有匹配规则时使用的规则 |
| `-config` | | YAML 配置文件 |
| `-seed` | 随机 | `loss` 和 `jitter` 的随机种子，相同的种子会复现相同的运行结果 |
| `-addr` | `10.99.0.254/24` | 虚拟网卡的 IPv4 地址和网段。网段内**除这个地址本身外**的地址都是模拟主机 |
| `-addr6` | `fd99::fe/64` | 虚拟网卡的 IPv6 地址和网段，设为空字符串则关闭 IPv6 |
| `-dev` | `pingmock0` | 虚拟网卡名称 |
| `-api` | `127.0.0.1:9100` | 控制 API 的监听地址，设为空字符串则关闭 |
| `-history` | `1000` | `GET /history` 保留的最近请求数，`0` 表示关闭 |
| `-v` | `false` | 打印每个请求的处理日志 |
| `-version` | | 打印版本号并退出 |

## 不用 root 运行：pingmock exec

```bash
pingmock exec [参数] -- 命令 [参数...]
```

会在一个**独立的网络命名空间**里运行命令，pingmock 在其中模拟 ping，参数和前面完全相同：

```bash
pingmock exec -rule 10.99.0.1=50ms -rule 8.8.8.8=unreachable -- ./my-app --check-latency
```

- **不需要 root。** 普通用户运行时，pingmock 还会创建一个用户命名空间，在其中设置网络；以 root 运行时，只创建网络命名空间。
- **不影响宿主机网络。** 模拟 `8.8.8.8` 只对这个命令生效；多次运行可以同时进行，各自拥有独立的规则、网卡和 API 端口。
- **准备就绪后才启动命令。** 命令开始运行时，ping 已经可以被回复，API 也已经在监听。命令可以从环境变量 `$PINGMOCK_API` 获取 API 地址。
- **以命令的退出码退出**，方便用在脚本和 CI 里：返回命令的退出码；命令被信号 `N` 杀死时返回 `128+N`；命令不存在时返回 `127`。`SIGTERM`、`SIGHUP` 和 `SIGQUIT` 会转发给命令。
- **不留残留进程。** 命令退出时，它在后台启动的进程也会被一并停止。

在独立网络里，命令只能访问模拟的目标和 `127.0.0.1` 上的 API，没有其他网络访问。所以要先编译好程序再放进去运行：需要下载依赖的 `go run`（或 `npm install`、`pip install` 等）会因为 `network is unreachable` 而失败。

### 系统要求

普通用户使用 `pingmock exec` 需要系统支持非特权用户命名空间，并且 `/dev/net/tun` 允许所有人打开（`crw-rw-rw-`），大多数发行版默认如此。如果条件不满足，pingmock 会说明缺少什么以及如何解决。

**Ubuntu 23.10 及以后的版本**会通过 AppArmor 限制非特权用户命名空间。建议不要为所有程序解除这个限制，而是用 [`contrib/apparmor/pingmock`](contrib/apparmor/pingmock) 中的配置只为 pingmock 放行，Ubuntu 对其他需要这项功能的程序也是这样处理的。如果 pingmock 没有安装在 `/usr/local/bin/pingmock`，先修改配置中的路径，然后：

```bash
sudo cp contrib/apparmor/pingmock /etc/apparmor.d/pingmock
sudo apparmor_parser -r /etc/apparmor.d/pingmock
```

在 GitHub Actions 的 Ubuntu 机器上，在运行测试前加一个步骤做同样的事。[`examples/github-actions`](examples/github-actions/) 是一份可以直接复制的配置，包括安装 pingmock 和这份 AppArmor 配置。

## Docker

镜像 `ghcr.io/nanmuyao/pingmock` 需要 `NET_ADMIN` 权限和 TUN 设备。与它共享网络的容器发出的 ping 都会被模拟，并且可以通过 `127.0.0.1:9100` 访问 API：

```bash
docker run -d --name pingmock --cap-add NET_ADMIN --device /dev/net/tun \
  ghcr.io/nanmuyao/pingmock -rule 10.99.0.1=50ms
docker run --rm --network container:pingmock alpine ping -c 3 10.99.0.1
```

使用 Docker Compose 时，用 `network_mode: service:pingmock` 让被测程序加入 pingmock 的网络。[`examples/docker-compose.yml`](examples/docker-compose.yml) 是一个完整的示例，其中包含健康检查，确保 pingmock 就绪后才启动被测程序：

```bash
docker compose -f examples/docker-compose.yml up --abort-on-container-exit --exit-code-from app
```

[`examples/compose-demo`](examples/compose-demo/) 更进一步：一个被测服务实时响应从宿主机修改的规则，以及用 `docker compose run` 运行的测试。

不带参数时，镜像中的 API 监听所有地址，可以用 `-p 9100:9100` 映射出来。Docker 默认在容器里禁用 IPv6，所以除非为容器启用了 IPv6，pingmock 只会使用 IPv4。

## 运行时控制 API

测试过程中可以通过 HTTP 随时修改规则，修改立即生效。请求体可以是和 `-rule` 相同的文本格式，也可以是 JSON。

```bash
# 查看种子、规则和每个目标的统计（请求数、回复数、丢弃数、不可达数、当前阶段）
curl localhost:9100/rules

# 设置或修改规则
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,pattern=.x'
curl -X PUT localhost:9100/rules/10.99.0.1 -d '50ms for 3 pings; timeout'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# 删除规则（回到默认值）
curl -X DELETE localhost:9100/rules/10.99.0.1

# 修改默认规则
curl -X PUT localhost:9100/default -d timeout

# 清零统计
curl -X DELETE localhost:9100/stats

# 最近的请求，按时间从早到晚排列；可以按地址或网段过滤，只保留最后 N 条
curl 'localhost:9100/history?target=10.99.0.1&limit=20'

# 清空历史
curl -X DELETE localhost:9100/history

# 从头开始：规则和默认规则恢复成命令行和配置文件设定的样子，统计和历史清空，
# 每个目标的场景和随机序列重新开始
curl -X POST localhost:9100/reset

# 这是哪个版本的 pingmock：{"version": "v0.6.0"}
curl localhost:9100/version
```

每个测试开始时先 `POST /reset`，不管前面的测试改过什么，它每次都从同样的状态出发。

每条历史记录的格式如下。`result` 是 `reply`、`drop` 或 `unreachable`；`code` 只在不可达时出现；`delay` 在有响应时出现；`phase` 从 1 开始计数：

```json
{"time": "2026-10-17T11:16:10.113Z", "src": "10.99.0.254", "dst": "10.99.0.1",
 "id": 4242, "seq": 3, "result": "unreachable", "code": "host", "reply": false,
 "delay": "40ms", "rule": "10.99.0.1/32", "phase": 2}
```

## 在自动化测试中使用

### 经过测试的工具和库

CI 会在 Ubuntu 24.04 和 26.04 上，用 [`test/e2e-compat.sh`](test/e2e-compat.sh) 测试下面这些工具和库：延迟、确定性丢包、目标不可达和劫持真实地址，结果都和规则一致。

| 工具或库 | 套接字 |
|---|---|
| iputils 的 `ping`（Ubuntu 24.04 上是 20240117，26.04 上是 20250605） | 原始套接字 |
| [fping](https://fping.org) | 原始套接字 |
| [pro-bing](https://github.com/prometheus-community/pro-bing)（Go） | 原始套接字，以及免 root 的 ping 套接字 |
| [icmplib](https://github.com/ValentinBELYN/icmplib)（Python） | 原始套接字，以及免 root 的 ping 套接字 |

traceroute 和 mtr 目前还不能使用，参见[限制](#限制)。

最简单的方式是让测试本身运行在 `pingmock exec` 下：不需要 root，不需要启动和清理的代码，每次运行都相互隔离。建议固定种子，让随机的丢包和抖动每次都一样。

### Go

用 `go test -exec` 让测试程序运行在 pingmock 里，在测试中通过 [`client`](https://pkg.go.dev/github.com/nanmuyao/pingmock/client) 包控制 pingmock：

```bash
go test -exec 'pingmock exec -seed 42 -rule 10.99.0.1=20ms -rule 10.99.0.2=200ms --' ./...
```

```go
import "github.com/nanmuyao/pingmock/client"

func TestPicksFastestServer(t *testing.T) {
	pm, err := client.FromEnv()
	if err != nil {
		t.Skip(err) // 没有运行在 pingmock exec 下
	}
	ctx := context.Background()

	if got := pickFastest("10.99.0.1", "10.99.0.2"); got != "10.99.0.1" {
		t.Fatalf("picked %s", got)
	}

	// 网络状况变化：原来快的线路变慢了
	if err := pm.SetRule(ctx, "10.99.0.1", "500ms"); err != nil {
		t.Fatal(err)
	}
	if got := pickFastest("10.99.0.1", "10.99.0.2"); got != "10.99.0.2" {
		t.Fatalf("picked %s after the slowdown", got)
	}

	// 重试逻辑：前两个 ping 丢失，之后线路恢复
	pm.ClearHistory(ctx)
	pm.SetRule(ctx, "10.99.0.3", "timeout for 2 pings; 30ms")
	checkReachable("10.99.0.3")
	events, _ := pm.History(ctx, "10.99.0.3", 0)
	if len(events) != 3 || events[2].Result != "reply" {
		t.Errorf("history = %+v", events)
	}
}
```

客户端还可以读取统计数据（`Stats`）、删除规则、修改默认规则、把一切恢复成启动时的样子（`Reset`，适合放在每个测试的开头），详见它的[文档](https://pkg.go.dev/github.com/nanmuyao/pingmock/client)。[`examples/go-demo`](examples/go-demo/) 是一个可以直接参考的完整示例：一个按延迟挑选服务器的程序，以及针对超时、不可达、丢包、变慢和断开等情况的测试。

### 其他语言

让测试命令运行在 `pingmock exec` 下，通过 `$PINGMOCK_API` 调用[控制 API](#运行时控制-api)。以 pytest 为例：

```bash
pingmock exec -seed 42 -rule 10.99.0.1=20ms -rule 10.99.0.2=200ms -- pytest
```

[`examples/python-demo`](examples/python-demo/) 是一个完整的示例，其中的 pytest fixture 可以直接复制使用。

```python
import os, requests

API = os.environ["PINGMOCK_API"]

def test_picks_fastest_server():
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # 网络状况变化：原来快的线路变慢了
    requests.put(f"{API}/rules/10.99.0.1", data="500ms").raise_for_status()
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

def test_retries():
    # 前两个 ping 丢失，之后线路恢复
    requests.delete(f"{API}/history")
    requests.put(f"{API}/rules/10.99.0.3", data="timeout for 2 pings; 30ms")
    assert my_app.check_reachable("10.99.0.3")
    events = requests.get(f"{API}/history", params={"target": "10.99.0.3"}).json()["events"]
    assert [e["result"] for e in events] == ["drop", "drop", "reply"]
```

如果测试无法运行在 `pingmock exec` 下，可以改为在测试前启动 `sudo pingmock`，或者使用[容器镜像](#docker)。

### 本仓库的测试

单元测试不需要 root；[`test/`](test/) 中的端到端测试大多需要：

```bash
make test         # go vet 和带竞态检测的单元测试
make e2e          # 编译 pingmock，并用 sudo 运行 test/e2e.sh
make e2e-exec     # pingmock exec，分别以普通用户和 root 运行
make e2e-compat   # 用 fping、pro-bing 和 icmplib 测试 pingmock（需要安装 fping 和 icmplib）
make e2e-docker   # 容器镜像和 Compose 示例
```

[一个 ping 的往返之旅](docs/packet-path.zh-CN.md)讲解了 ping 请求如何经过内核到达 pingmock 再返回，[数据包详解](docs/packets.zh-CN.md)逐字节讲解了请求和应答的内容，[代码导读](docs/architecture.zh-CN.md)介绍了代码的组织方式和阅读顺序，[CONTRIBUTING.md](CONTRIBUTING.md) 说明了如何参与贡献。

## 限制

- 只模拟 ping（ICMP 和 ICMPv6 Echo）。发往模拟地址的 TCP、UDP 流量不会得到响应。
- traceroute 和 mtr 看不到路径：pingmock 不理会 TTL，所以模拟的目标看起来就在一跳之外；traceroute 默认发送的 UDP 探测得不到回应，所以永远到不了目标。路径模拟已在计划中。
- 延迟的精度在 1ms 以内（实测设定 50ms 时为 50.4ms），额外的零点几毫秒来自内核和调度开销。
- 不支持分片的请求（例如 `ping -s 2000`）和带扩展头的 IPv6 请求，这类请求会被忽略。
- 只支持 Linux。普通用户使用 `pingmock exec` 需要系统支持非特权用户命名空间（参见[系统要求](#系统要求)）。

## 许可证

[MIT](LICENSE)
