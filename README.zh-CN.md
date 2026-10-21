# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

[English](README.md) | 中文

在 Linux 上模拟网络的回应，从 ping 起步：ICMP 和 ICMPv6 Echo、TCP 连接，以及 `pingmock exec` 下的 DNS。可以精确设定每个目标地址、端口和域名的**延迟、抖动、丢包率、不可达错误、拒绝连接和 DNS 故障**，并在运行中随时修改。

![一个 ping 三台服务器并选出最快者的程序，随着 pingmock 改变各台的延迟，每次选出不同的服务器](docs/demo/demo.svg)

用 ping 探测网络的软件会根据结果做出不同的行为：选最快的服务器、切换线路、判断网络是否可用、显示信号强度。这些行为很难测试，因为真实网络不会刚好处在测试需要的状态。pingmock 让它们可以稳定复现：被测程序照常调用 `ping`（或自己发送 ICMP），一行代码不用改，得到的却是你设定的结果。

最短的例子，不需要 root：

```bash
pingmock exec -rule 10.99.0.1=50ms -rule 10.99.0.2=unreachable -- ./my-app
```

不 ping 而是建立连接的程序，用法也一样：`pingmock exec -tcp 10.99.0.1:5432=timeout -tcp 10.99.0.2:5432=5ms -- ./my-app` 让第一个数据库从不回应、第二个 5ms 接受连接，见 [TCP 连接](#tcp-连接)。

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

### 什么时候用它，什么时候用别的

pingmock 用来测试**根据 ping 的结果或连接能否建立来做决定的程序**。它不会把真实流量变慢，也不碰 TCP 连接上传输的数据。下面三个工具各有各的用处：

| 你想做的事 | 用什么 |
|---|---|
| 测试一个会 ping 或建立连接的程序：选服务器、切换线路、判断网络是否可用、显示延迟、连接超时和被拒绝 | **pingmock** |
| 把某块网卡上的真实流量变慢、丢包，对所有协议都生效：压力测试、"在 3G 网络下程序的体验" | [`tc netem`](https://man7.org/linux/man-pages/man8/tc-netem.8.html)，内核自带的网络模拟器 |
| 让到某个服务的 TCP 连接上的数据变慢或出错：延迟、限速、传输中途重置，针对数据库或 API | [Toxiproxy](https://github.com/Shopify/toxiproxy)，带 API 和多语言客户端的 TCP 代理 |

netem 作用在网卡和队列上，一条规则往往会波及所有流量（包括你自己的 SSH），而且需要 root。Toxiproxy 夹在应用和某个 TCP 服务之间，应用得改成连代理。pingmock 是**透明**的：程序照常 ping、照常连接它原来的地址和端口，不需要改成连代理；`pingmock exec` **不需要 root**，也不会改动被测命令之外的任何东西。在此之上，它吸收了这两者面向测试的那一面：故障**按目标地址、网段和端口**声明在和测试放在一起的配置文件里，随机丢包和抖动用 seed **可以复现**，规则可以通过 API **在程序运行中修改**。

### 可以依赖的四点

pingmock 做的每一件事，都同时满足这四点：

| | 含义 | 怎样做到 |
|---|---|---|
| **透明** | 被测程序不需要修改代码或配置，照常 ping 它原来 ping 的地址 | TUN 网卡和路由拦截请求，连 `ping` 命令都能直接用 |
| **按目标** | 每个目标可以有自己的网络状况 | 规则按地址或网段设置 |
| **可复现** | 同样的设置，每次运行的结果都一样 | `-seed`、确定性的 `pattern`、按次数或时间的场景、`POST /reset` |
| **可控、可观察** | 测试进行中可以改变网络状况，事后能看到程序实际做了什么 | HTTP API 和 Go 客户端、请求历史和统计 |

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

每条规则可以写成和 `-rule` 相同的文本、一个毫秒数，或者一个映射：包含 `delay`、`jitter`、`loss`（`0.05` 或 `5%`）、`pattern`、`unreachable` 和 `ttl`，或者包含 `phases` 和 `repeat`。配置文件还可以设置 `dev`、`addr`、`addr6`、`api`、`history`、[`paths`](#路径traceroute-和-mtr)、[`tcp`](#tcp-连接)，以及供 `pingmock exec` 使用的 [`hosts`](#域名) 和 [`dns`](#dns-故障)；在 `pingmock exec` 下，规则的键也可以是域名。映射写法里，TCP 规则还可以用 `refuse` 和 `after`，DNS 规则可以用 `nxdomain` 和 `servfail`（`{delay: 5ms, after: reset}`、`{servfail: true}`）。

出错时会提示所在的行号；未知的字段会直接报错，拼写错误在启动时就能发现。早期版本使用的 JSON 配置文件无需修改即可继续使用，因为 JSON 本身就是合法的 YAML。

命令行参数会覆盖配置文件，所以一个配置文件可以用于多个测试：`-config base.yaml -rule 10.99.0.1=500ms` 只替换这一条规则。

### 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-rule` | | `目标=规则`，可重复；在 `pingmock exec` 下，目标可以是[域名](#域名) |
| `-default` | `0ms` | 没有匹配规则时使用的规则 |
| `-config` | | YAML 配置文件 |
| `-seed` | 随机 | `loss` 和 `jitter` 的随机种子，相同的种子会复现相同的运行结果 |
| `-addr` | `10.99.0.254/24` | 虚拟网卡的 IPv4 地址和网段。网段内**除这个地址本身外**的地址都是模拟主机 |
| `-addr6` | `fd99::fe/64` | 虚拟网卡的 IPv6 地址和网段，设为空字符串则关闭 IPv6 |
| `-dev` | `pingmock0` | 虚拟网卡名称 |
| `-api` | `127.0.0.1:9100` | 控制 API 的监听地址，设为空字符串则关闭 |
| `-history` | `1000` | `GET /history` 保留的最近请求数，`0` 表示关闭 |
| `-host` | | `域名=地址`，可重复，仅限 `pingmock exec`：命令把域名解析成这个地址，见[域名](#域名) |
| `-path` | | `目标=路径`，可重复：traceroute 和 mtr 看到的、到目标的路径，见[路径](#路径traceroute-和-mtr) |
| `-tcp` | | `目标=规则`，可重复：发往目标（`地址[:端口]`、`网段[:端口]`、`[IPv6]:端口`）的 TCP 连接怎样回应，见 [TCP 连接](#tcp-连接) |
| `-dns` | | `域名=规则`，可重复，只用于 `pingmock exec`：命令对该域名的 DNS 查询怎样回应，见 [DNS 故障](#dns-故障) |
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

### 域名

很多软件 ping 的是域名而不是地址。在 `pingmock exec` 下，规则可以直接写域名：

```bash
pingmock exec -rule api.example.com=80ms -rule backup.example.com=timeout -- ./my-app
```

命令会把 `api.example.com` 解析成一个模拟地址，ping 它时得到这条规则。地址从网段的高端往下分配（`10.99.0.253`、`10.99.0.252`……），每次运行都一样，启动日志会写明：`rule api.example.com (10.99.0.253): delay=80ms`。想自己指定地址，或者让域名解析成 IPv6 地址，用 `-host 域名=地址`（可重复）：

```bash
pingmock exec -host api.example.com=fd99::1 -rule api.example.com=80ms -- ./my-app
```

原理：在命令独有的挂载命名空间里，它得到**自己的 `/etc/hosts`**（系统的文件，前面加上模拟的域名）和一份**指向 pingmock 为它运行的小型 DNS 服务的 `resolv.conf`**。这个 DNS 服务回答模拟的域名，正反向都有（地址也能解析回域名），其余域名转发给宿主机的解析器，所以真实的域名照样能解析，只是它们的地址在独立网络里不可达。它同时支持 UDP 和 TCP（解析器遇到较大的回复会改用 TCP），每个查询各自处理；宿主机的解析器不回应时它也不回应，命令会像没有 pingmock 时一样超时；根本连不上宿主机的解析器时，命令立即得到失败。凡是解析域名的程序看到的都一样：`ping` 和其他用 C 库的程序、Go、Python……宿主机运行 nscd（域名服务缓存）时，命令连不到它，因为 nscd 会按宿主机的文件和解析器回答。系统的文件不会被改动，不留任何痕迹，也不需要 root。在配置文件里写法是：

```yaml
rules:
  api.example.com: 80ms
  backup.example.com: timeout
hosts:                            # 只在需要指定地址时写
  api.example.com: fd99::1
```

域名只用于 `pingmock exec`。用 `sudo pingmock` 为整台机器模拟时，请把域名加到机器的 `/etc/hosts` 里，规则写地址。

### DNS 故障

域名解析不了的时候，软件也会出问题：DNS 服务器出错、域名消失、解析一直没有回应。在 `pingmock exec` 下，**DNS 规则**决定命令对某个域名的查询怎样回应：

```bash
pingmock exec \
  -dns api.example.com=nxdomain \
  -dns 'backup.example.com=servfail for 2 requests; 10ms' \
  -dns github.com=delay=2s \
  -dns slow.example=timeout \
  -- ./my-app
```

规则的写法和 `-rule` 相同，含义如下：

| 设置 | 程序看到的 |
|---|---|
| `20ms`、`delay=`、`jitter=` | 延迟之后得到通常的回复：模拟的域名是模拟的地址，其他域名是宿主机解析器的回复 |
| `nxdomain` | 域名不存在：`getaddrinfo` 立即以 `EAI_NONAME`（"Name or service not known"）失败，Go 报 `no such host` |
| `servfail` | 服务器出错：解析器重试，然后以 `EAI_AGAIN`（"Temporary failure in name resolution"）失败 |
| `timeout`、`loss=30%` | 不回应：解析器等到超时（默认 5 秒）后重试，然后以 `EAI_AGAIN` 失败 |
| `pattern=.ns` | 按顺序轮流：`.` 回复、`x` 不回应、`n` NXDOMAIN、`s` SERVFAIL |

规则对模拟的域名（用 `-host` 或为域名写的规则）和真实的域名都有效，真实域名的故障直接由 pingmock 回答，不去问宿主机。有规则的域名不写进命令的 `/etc/hosts`，因为程序会先读 hosts 文件、不问 DNS。

场景按**查询**计数，每个域名的每种查询类型分别计。一次解析通常同时查 A 和 AAAA，遇到 SERVFAIL 或超时解析器还会重试，所以一个阶段覆盖几次解析取决于得到的回复：`nxdomain for 2 requests` 让两次解析失败，因为 NXDOMAIN 会被立即相信；`servfail for 2 requests` 只让一次解析失败，因为 C 库默认问两次（`resolv.conf` 里的 `attempts:2`）。统计按 `api.example.com/A`、`api.example.com/AAAA` 分开给出，可以看到实际发生了什么。解析器加上 `resolv.conf` 的 search 域去试的名字（`api.example.com.corp.example`）是别的域名，照常转发。C 库在一个域名失败后会去试这些名字，并报告最后一个回复：宿主机有 `search` 行时，`api.example.com` 的 SERVFAIL 或超时可能最终变成"找不到"，甚至变成 search 域给出的地址。要看到规则本身的故障，请在域名末尾加点（`api.example.com.`），或者在没有 search 域的机器上测试。

延迟接近解析器的超时时，不同解析器的表现不同：C 库在超时后已经重试的情况下仍会接受迟到的回复，Go 的解析器不会。

配置文件和 API 中的写法：

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

Go 客户端有 `SetDNS`、`DeleteDNS` 和 `DNSStats`。通过 API 可以为任何不在命令 `/etc/hosts` 里的域名设置规则；要在运行中改变模拟域名的回复，启动时就给它一条 DNS 规则（`-dns api.example.com=0ms`）。`GET /rules` 在 `dns` 中列出 DNS 规则，在 `dns_stats` 中给出统计：请求、回复、nxdomain、servfail、丢弃。对有规则的域名的每次查询都会记入历史，带有 `"probe": "dns"`、`name`、`qtype`，结果是 `answer`、`nxdomain`、`servfail` 或 `drop`；`GET /history?target=api.example.com` 返回这些记录。PTR 查询不受影响。随机性来自种子，按域名和查询类型各自独立。

DNS 规则只用于 `pingmock exec`：为整台机器模拟时，pingmock 无法插到程序和机器的解析器之间，所以 `sudo pingmock -dns` 会被拒绝。

## TCP 连接

很多检查网络的软件也会建立连接：连数据库、连 API、检测某个端口。**TCP 规则**决定发往某个地址和端口的连接请求怎样回应，程序不需要任何改动，就会遇到连接很慢、被拒绝，或者一直连不上：

```bash
pingmock exec \
  -tcp 10.99.0.1:5432=timeout \
  -tcp 10.99.0.2:5432=5ms \
  -tcp '10.99.0.3:443=refuse for 3 requests; 20ms' \
  -- ./my-app
```

这里第一个数据库从不回应，`connect` 会超时，程序应该切换到第二个，它 5ms 就接受连接。第三台服务器先拒绝三次连接（立即得到 `ECONNREFUSED`），之后接受。

目标写成 `地址:端口`、`网段:端口`，不写端口就是该地址或网段的所有端口。IPv6 和 URL 里一样加方括号：`[fd99::1]:443`、`[fd99::/64]:443`、`[fd99::1]`。不加括号时 `fd99::1:443` 本身就是一个地址，所以会被拒绝。在 `pingmock exec` 下，目标还可以是[域名](#域名)，比如 `db.example.com:5432`。

规则的写法和 `-rule` 相同，含义如下：

| 设置 | 程序看到的 |
|---|---|
| `20ms`、`delay=`、`jitter=` | 延迟之后接受连接：`connect` 用时就是这个延迟 |
| `refuse` | 立即拒绝连接（有延迟时在延迟之后）：TCP reset，`ECONNREFUSED` |
| `timeout`、`loss=30%` | 不回应：`connect` 超时，每次都超时或者 30% 的连接超时 |
| `unreachable[=net\|admin]` | `connect` 立即失败：`EHOSTUNREACH`（"No route to host"），`net` 则是 `ENETUNREACH`（"Network is unreachable"） |
| `pattern=..r` | 按顺序轮流：`.` 接受、`x` 不回应、`u` 不可达、`r` 拒绝 |
| `after=close` | 接受连接，然后立即关闭：读取得到文件结束（EOF），HTTP 客户端得到空回复（curl："Empty reply from server"） |
| `after=reset` | 接受连接，等程序第一次发送数据或关闭时重置：`ECONNRESET`（"Connection reset by peer"） |
| `after=hang` | 默认：接受连接并保持打开，不发送任何数据 |
| `ttl=` | 回应的 TTL |

场景按连接请求计数：`for 3 requests`（也可以写 `connections`）。一次连接请求只算一次，不管客户端的内核在等待期间重发了几次 SYN，所以 `timeout for 2 requests` 就是两次连接请求，和内核的重传时间无关。多条规则匹配时，先比网段谁更精确，再比有端口的优先于不带端口的。

和 ping 的规则一样，root 模式下为真实地址设置 TCP 规则，会把发往这个地址的**所有**流量交给 pingmock，见[模拟真实地址](#模拟真实地址)；`pingmock exec` 只影响它运行的命令。

接受的连接会保持打开，但不会发送任何数据：程序读取时会遇到读取超时，可以和连接超时区分开。pingmock 不模拟任何服务。没有 TCP 规则的端口和以前一样不回应。

同样的规则可以测试 **TLS 握手超时**：连接建立了，客户端发出 ClientHello，却一直等不到回应。`pingmock exec -tcp api.example.com:443=5ms -- ./my-app` 让发往这个域名的每个 HTTPS 请求都卡在握手：Python 的 `ssl` 报 "The handshake operation timed out"，curl 以退出码 28 放弃，Go 的 `http.Transport` 在 `TLSHandshakeTimeout` 之后失败。

用 `after=` 可以改为别的做法，测试程序怎样应对服务器断开它：`-tcp '10.99.0.1:5432=after=reset'` 让发往这个数据库的每次查询都失败于 "connection reset by peer"；`-tcp '10.99.0.1:443=5ms,after=close for 2 requests; 5ms'` 先给两次空回复，之后服务器恢复（接受连接并保持）。`after=reset` 会等程序发送数据：如果连接一建立就重置，重置有时会在 `connect` 还没返回时到达，让 `connect` 本身失败。因此先等服务器说话的程序（SSH、SMTP、MySQL 客户端）在 `after=reset` 下会遇到读取超时，在 `after=close` 下读到文件结束。关闭之后程序再写入的数据会得到重置，和关闭了 socket 的真实服务器一样；之后再写会以 `EPIPE` 失败。动作在接受连接时就决定了，所以即使程序发送数据之前阶段已经结束，动作也不会变。空闲 10 分钟的连接会被忘掉，之后不再回应。

配置文件（带方括号的键要加引号）和 API 中的写法：

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

Go 客户端有 `SetTCP`、`DeleteTCP` 和 `TCPStats`。`GET /rules` 在 `tcp` 中列出 TCP 规则，在 `tcp_stats` 中按地址和端口（`"10.99.0.1:5432"`）给出统计：请求、接受、拒绝、丢弃、不可达，以及接受的连接中被关闭和被重置的数目。连接请求的历史记录带有 `"probe": "tcp"` 和 `port`，结果是 `syn-ack`（接受）、`refused`、`drop` 或 `unreachable`；内核重发的 SYN 记为 `"repeat": true`，不重复计数。关闭或重置一个已接受的连接时，会多记一条结果为 `closed` 或 `reset` 的事件，带有接受它的规则和阶段。随机性来自种子，按地址和端口各自独立，和发往同一地址的 ping 互不影响，ping 在同一种子下的结果不变。

## 路径：traceroute 和 mtr

有些软件看的不只是终点，还有去往终点的路。可以给目标设置一条**路径**：从这里到它之间的每一台路由器，像真实路由器一样回应 TTL 在它那里耗尽的探测包。traceroute 和 mtr 就能看到每一跳，三种探测方式（UDP、ICMP、TCP）都能到达终点。

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

路径是用 `>` 连接的若干跳，每跳写成 `地址@设置`，或者用 `*` 表示一台从不回应的路由器。设置项有：

| 设置 | 含义 |
|---|---|
| `2ms`、`delay=2ms` | 到这一跳的往返延迟 |
| `jitter=3ms` | 延迟在 ±3ms 内均匀波动 |
| `loss=10%` | 这一跳对 10% 的、在它这里终止的探测包不回应，就像限制 ICMP 速率的路由器。其他都不受影响：mtr 只在这一行显示丢包 |
| `cut=5%` | 这一跳丢掉 5% 经过它的包：发给后面各跳和终点的探测包，还有发给终点的 ping。有问题的链路就是这样：从这一跳起都有丢包 |
| `ttl=250` | 这一跳回应的 TTL（默认 64） |

终点自己的规则仍然决定终点怎么回应：它的延迟和丢包像以前一样作用于 ping，也作用于 traceroute 的最后一行。设置了路径的目标还会用"端口不可达"回应 UDP 探测、用 SYN-ACK 回应 TCP 探测，traceroute 的 UDP 和 TCP 模式就是靠这个知道到达了终点；没有路径的目标仍然不理会 UDP 和 TCP。各跳的地址会被加上指向虚拟网卡的路由，这样在严格的反向路径检查下内核也接受它们的回应；在 `pingmock exec` 里这不会影响命令之外的任何东西。随机数来自 seed、按目标独立，所以 `loss` 和 `cut` 在 `-seed` 下可以精确复现。

配置文件里的写法（以 `*` 开头的路径要加引号，否则 YAML 会当成别名）和 API：

```yaml
paths:
  8.8.8.8: 192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms
```

```bash
curl -X PUT localhost:9100/paths/8.8.8.8 -d '192.168.1.1@2ms > * > 72.14.0.1@25ms'
curl -X DELETE localhost:9100/paths/8.8.8.8
```

Go 客户端有 `SetPath` 和 `DeletePath`。`GET /rules` 会列出路径；历史记录里每个探测包都带有 `ttl`、回应或丢掉它的 `hop`，以及回应来自哪个地址（`from`）；探测包的 `result` 是某一跳回的 `time-exceeded`，或者终点回的 `port-unreachable`、`syn-ack`。

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
# 以及 TCP 规则（tcp、tcp_stats）和 DNS 规则（dns、dns_stats）的同类信息
curl localhost:9100/rules

# 设置或修改规则
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,pattern=.x'
curl -X PUT localhost:9100/rules/10.99.0.1 -d '50ms for 3 pings; timeout'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# 删除规则（回到默认值）
curl -X DELETE localhost:9100/rules/10.99.0.1

# TCP 规则：拒绝发往某个端口的连接、重置已接受的连接，然后删除一条规则
curl -X PUT localhost:9100/tcp/10.99.0.1:5432 -d refuse
curl -X PUT localhost:9100/tcp/10.99.0.2:443 -d 5ms,after=reset
curl -X DELETE localhost:9100/tcp/10.99.0.1:5432

# DNS 规则（pingmock exec 下）：域名解析不了，然后恢复
curl -X PUT localhost:9100/dns/api.example.com -d nxdomain
curl -X DELETE localhost:9100/dns/api.example.com

# 修改默认规则
curl -X PUT localhost:9100/default -d timeout

# 清零统计
curl -X DELETE localhost:9100/stats

# 最近的请求，按时间从早到晚排列；可以按地址或网段（DNS 查询按域名）过滤，只保留最后 N 条
curl 'localhost:9100/history?target=10.99.0.1&limit=20'

# 清空历史
curl -X DELETE localhost:9100/history

# 从头开始：规则和默认规则恢复成命令行和配置文件设定的样子，统计和历史清空，
# 每个目标的场景和随机序列重新开始
curl -X POST localhost:9100/reset

# 这是哪个版本的 pingmock：{"version": "v0.11.0"}
curl localhost:9100/version
```

每个测试开始时先 `POST /reset`，不管前面的测试改过什么，它每次都从同样的状态出发。

每条历史记录的格式如下。`result` 是 `reply`、`drop` 或 `unreachable`；`code` 只在不可达时出现；`delay` 在有响应时出现；`phase` 从 1 开始计数。连接请求、DNS 查询和 traceroute、mtr 的探测还有其他字段，见 [TCP 连接](#tcp-连接)、[DNS 故障](#dns-故障)和[路径](#路径traceroute-和-mtr)：

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

设置了[路径](#路径traceroute-和-mtr)的目标，traceroute 和 mtr 能看到各跳，UDP、ICMP、TCP 三种模式都可以，CI 都有检查。没有路径的目标看起来就在一跳之外，而且只回应 ping。

对于 [TCP 规则](#tcp-连接)，同一个脚本用 Go（`net.Dial`）和 Python（`socket`）的标准库建立连接，检查它们分别看到：延迟之后连上、随后读取超时，`ECONNREFUSED`，连接超时，`EHOSTUNREACH`。curl 和 bash 的 `/dev/tcp` 在端到端测试里检查。

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

客户端还可以读取统计数据（`Stats`）、删除规则、修改默认规则、设置 TCP 和 DNS 规则（`SetTCP`、`SetDNS`，以及 `TCPStats`、`DNSStats`）、把一切恢复成启动时的样子（`Reset`，适合放在每个测试的开头），详见它的[文档](https://pkg.go.dev/github.com/nanmuyao/pingmock/client)。[`examples/go-demo`](examples/go-demo/) 是一个可以直接参考的完整示例：一个按延迟挑选服务器的程序，以及针对超时、不可达、丢包、变慢和断开等情况的测试。

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

在 Mac 上，或者没有 Linux 的地方，用仓库里的[开发容器](.devcontainer/README.zh-CN.md)：除了 `make e2e-docker`，其余都能在里面跑。

[一个 ping 的往返之旅](docs/packet-path.zh-CN.md)讲解了 ping 请求如何经过内核到达 pingmock 再返回，[数据包详解](docs/packets.zh-CN.md)逐字节讲解了请求和应答的内容，[代码导读](docs/architecture.zh-CN.md)介绍了代码的组织方式和阅读顺序，[CONTRIBUTING.md](CONTRIBUTING.md) 说明了如何参与贡献。

## 限制

- 模拟 ping（ICMP 和 ICMPv6 Echo），以及发往有 [TCP 规则](#tcp-连接)的端口的 TCP 连接：接受、拒绝或不回应，接受的连接还可以关闭或重置。在 `pingmock exec` 下，对有 [DNS 规则](#dns-故障)的域名的 DNS 查询按规则回应。接受的连接上不交换数据：pingmock 不模拟任何服务。其他 TCP 流量和 UDP 不会得到响应，设置了路径的目标收到的 traceroute、mtr 探测除外。
- **绑定网卡**的 ping 不会被模拟：`ping -I eth0 8.8.8.8` 无论路由怎样都会从 `eth0` 发出，因此绕过了 pingmock。OpenWrt 的 mwan3 等多线路工具就是这样检测线路的。绑定源**地址**（`ping -I 192.0.2.2 8.8.8.8`）则照常被模拟。
- 没有[路径](#路径traceroute-和-mtr)的目标，在 traceroute 和 mtr 看来就在一跳之外，而且只回应 ping：发给它的 UDP 和 TCP 探测得不到任何回应。有路径的目标会用表示"已到达"的回应答复 UDP 和 TCP 探测，但不会交换任何数据：pingmock 不模拟任何服务。
- 延迟的精度在 1ms 以内（实测设定 50ms 时为 50.4ms），额外的零点几毫秒来自内核和调度开销。
- 不支持分片的请求（例如 `ping -s 2000`）和带扩展头的 IPv6 请求，这类请求会被忽略。
- 只支持 Linux。普通用户使用 `pingmock exec` 需要系统支持非特权用户命名空间（参见[系统要求](#系统要求)）。

## 许可证

[MIT](LICENSE)
