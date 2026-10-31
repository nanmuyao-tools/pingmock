# 一个 ping 的往返之旅

[English](packet-path.md) | 中文

这份文档跟随一个 ping 请求，从 `ping` 程序出发，经过内核到达 ghostwire，再带着应答回到 `ping`。理解了这个过程，就理解了 ghostwire 为什么**不需要修改被测软件**就能控制它看到的延迟。

如果地址、数据包、TCP 或 DNS 对你来说还比较陌生，请先读[网络基础](basics.zh-CN.md)，那里配着示意图讲解了这些概念。

下面以默认设置为例：ghostwire 的虚拟网卡叫 `ghostwire0`，地址是 `10.99.0.254/24`，有一条规则 `10.99.0.1=50ms`，然后有程序执行了 `ping 10.99.0.1`。

## 全景图

```
去程（请求）
  ping ──sendto("10.99.0.1")──► 内核
                                 │ ① 路由查询
                                 │    先查 local 表：10.99.0.1 不是本机地址
                                 │    再查 main 表：最长前缀匹配 → 10.99.0.0/24 dev ghostwire0 src 10.99.0.254
                                 │ ② 封装 IP 头：目标 10.99.0.1，源地址取路由结果里的 10.99.0.254
                                 ▼
                           ghostwire0（TUN 驱动）──► 放入队列 ──► ghostwire 的 Read() 读到

ghostwire 内部
  按规则决定怎么回应 → 构造应答 → 等待 50ms

回程（应答）
  ghostwire Write() ──► ghostwire0 ──► 内核当作"从 ghostwire0 收到一个包"
                                      │ ③ 反向路径检查：回复 10.99.0.1 也走 ghostwire0，通过
                                      │ ④ 目标 10.99.0.254 在 local 表里，是本机地址，收下
                                      │ ⑤ 是 ICMP 应答，交给 ping 的套接字
                                      ▼
                                    ping recvmsg() 收到应答，计算延迟：50ms
```

`ping` 自始至终只和内核打交道，完全不知道 ghostwire 的存在。是内核按照路由表把请求交给了 ghostwire，又把 ghostwire 写回的应答当成网络上来的包交给了 `ping`。

## 准备：ghostwire 启动时做了什么

ghostwire 启动时对系统做的事情，本质上就是**用代码创建一块 TUN 网卡，再替你做几条 `ip` 命令会做的事**。

### 相当于哪些命令

ghostwire 不运行任何程序：它通过 netlink 把这些修改交给内核，`ip` 程序自己用的也是这个接口。写成效果相同的 `ip` 命令，以 `ghostwire -rule 10.99.0.1=10ms -rule 8.8.8.8=20ms` 为例：

```bash
ip addr add 10.99.0.254/24 dev ghostwire0          # ① 给网卡设置地址和网段
ip link set ghostwire0 up                          # ② 启用网卡
ip -6 addr add fd99::fe/64 dev ghostwire0 nodad    # ③ 设置 IPv6 地址
ip -o -4 route show exact 8.8.8.8/32 table main  # ④ 宿主机是否已经有这个地址的路由？
ip route replace 8.8.8.8/32 dev ghostwire0         # ⑤ 为网段之外的真实地址添加路由
```

| 命令 | 代码位置 | 说明 |
|---|---|---|
| ① ② | [`configureDevice`](../internal/app/instance.go) | 地址来自 `-addr` 参数（或配置文件里的 `addr`），默认 `10.99.0.254/24` |
| ③ | [`configureDevice`](../internal/app/instance.go) | 地址来自 `-addr6`，默认 `fd99::fe/64`。如果内核没有启用 IPv6，这条命令会失败，ghostwire 打印提示后只用 IPv4 继续运行 |
| ④ ⑤ | [`Router.Ensure`](../internal/tun/tun_linux.go) | 只为**网段之外**的目标执行。10.99.0.1 本来就在 10.99.0.0/24 里，第 ① 条命令之后内核已经自动生成了路由，所以没有为它执行。④ 查询宿主机是否已经有这个网段本身的路由：有的话，ghostwire 改为把它拆成两半来路由（`/24` 拆成两个 `/25`），保留宿主机原有的路由；如果是单个地址则拒绝（见[模拟真实地址](../README.zh-CN.md#模拟真实地址)）。路径上的每一跳也这样添加路由 |

每条命令对应一条 netlink 消息，代码在 [`netlink_linux.go`](../internal/tun/netlink_linux.go)：① `AddAddr`，② `LinkUp`，③ 还是 `AddAddr`，④ `mainRoutes`，列出路由后挑出这个网段本身的，⑤ `replaceRoute`。规则不再需要某条路由时，`deleteRoute` 删掉它，相当于 `ip route del`。想亲眼看到这些修改，可以在 ghostwire 启动时，在另一个终端里运行 `ip monitor`。

用 `ghostwire exec` 运行时，开头会多一条：

```bash
ip link set lo up                                 # 打开回环网卡
```

因为 exec 模式会创建一个全新的网络命名空间，里面连回环网卡 `lo` 默认都是关闭的，而控制 API 监听在 `127.0.0.1:9100`，需要回环网卡才能访问。普通模式下系统的 `lo` 早已开启，不需要这一步。代码在 [`runExecChild`](../internal/app/exec_linux.go)。

### 创建网卡：用代码，而不是 `ip` 命令

上面的命令里没有创建网卡这一步。ghostwire 是在 [`tun.Open`](../internal/tun/tun_linux.go) 里直接用代码创建的：

1. 打开 `/dev/net/tun`，这是内核提供的 TUN 入口
2. 调用 `ioctl(TUNSETIFF)`，告诉内核"创建一块叫 ghostwire0 的 TUN 网卡，绑定到我刚打开的这个文件上"

之后读写这个文件，就等于在这块网卡上收发包。也可以用 `ip tuntap add dev ghostwire0 mode tun` 创建 TUN 网卡，两者的区别在于网卡的生命周期：

| | `ip tuntap add` | ghostwire 的 `ioctl` |
|---|---|---|
| 网卡什么时候消失 | 一直存在，直到手动删除 | **打开它的文件关闭时**，也就是 ghostwire 退出时 |
| `ip -d link` 显示 | `persist on` | `persist off` |
| 谁在另一端读写 | 没有程序时，网卡处于 `NO-CARRIER`（没插网线）状态 | ghostwire 进程 |

ghostwire 选择后一种，所以**无论怎样退出，网卡和它的地址、路由都会被内核自动清理**，不会留在系统里。

### 内核自动生成的路由

第 ① 条命令之后，**内核自动**在两张表里各登记了一条记录：

| 表 | 记录 | 含义 |
|---|---|---|
| local 表 | `local 10.99.0.254 dev ghostwire0` | 10.99.0.254 是本机自己的地址 |
| main 表 | `10.99.0.0/24 dev ghostwire0 src 10.99.0.254` | 10.99.0.0～10.99.0.255 都从 ghostwire0 发出 |

ghostwire 并没有直接修改路由表。`10.99.0.254/24` 同时说明了两件事：网卡自己的地址是 10.99.0.254，所在网段是前 24 位（`10.99.0`）相同的地址。内核据此推断"这个网段的地址都在 ghostwire0 那一侧"，自动生成了 main 表里的路由。

### 自己动手：搭一个一样的环境

不用 ghostwire，只用 `ip` 命令就能搭出几乎一样的环境，并看到路由是在哪一步出现的（这里用 `10.97.0.x`，避免和 ghostwire 混淆）：

```bash
sudo ip tuntap add dev demo0 mode tun          # 1. 创建 TUN 网卡
ip route get 10.97.0.1                         #    → 还走默认路由 eth0：光有网卡，内核不知道该把哪些地址交给它

sudo ip link set demo0 up                      # 2. 启用网卡
ip route get 10.97.0.1                         #    → 依然走 eth0

sudo ip addr add 10.97.0.254/24 dev demo0      # 3. 设置地址和网段
ip route show | grep 10.97                     #    → 出现 10.97.0.0/24 dev demo0 proto kernel（内核自动添加）
ip route get 10.97.0.1                         #    → 改走 demo0

sudo ip addr del 10.97.0.254/24 dev demo0      # 4. 删掉地址
ip route get 10.97.0.1                         #    → 路由随之消失，又走回 eth0

sudo ip tuntap del dev demo0 mode tun          # 清理
```

可以看到，**路由是在第 3 步设置地址时出现的**，而不是创建或启用网卡时。这个环境和 ghostwire 搭出来的唯一区别是：网卡另一端没有程序读写数据包，所以 ping 10.97.0.1 会收不到应答。只要有一个程序打开这块网卡、读出请求、对调地址后写回，ping 就能收到应答，ghostwire 做的正是这件事。

## 去程：请求怎样到达 ghostwire

### 第 0 步：ping 把请求交给内核

`ping` 打开一个 ICMP 套接字，然后调用 `sendto`，交给内核的只有两样东西：**64 字节的 ICMP 内容**（8 字节 ICMP 头 + 56 字节数据，数据开头是发送时间），以及**目标地址 10.99.0.1**。它没有指定走哪块网卡，也不负责 IP 头，这些都由内核决定。

### 第 ① 步：路由查询

内核要决定这个包怎么走。路由规则按顺序查阅，可以用 `ip rule` 看到：

```
0:      from all lookup local     ← 先查
32766:  from all lookup main      ← 再查
```

- **local 表**：目标是本机自己的地址吗？10.99.0.1 不是，继续。
- **main 表**：找出所有包含 10.99.0.1 的路由，选**网段最精确**的那条，这叫"最长前缀匹配"：

| 路由 | 包含 10.99.0.1 吗 | 精确程度 |
|---|---|---|
| `default`（即 0.0.0.0/0） | 包含 | /0，包含所有地址 |
| `10.99.0.0/24 dev ghostwire0` | 包含 | **/24，更精确，选中** |

查询结果是 `10.99.0.1 dev ghostwire0 src 10.99.0.254`：从 ghostwire0 发出，源地址用 10.99.0.254。

### 第 ② 步：封装 IP 头

内核给 64 字节的 ICMP 内容加上 20 字节的 IP 头：目标地址 10.99.0.1，**源地址取路由查询结果里的 10.99.0.254**。必须先查路由才能填源地址，因为从不同网卡发出，源地址就不同。这样就得到了一个 84 字节的 IP 包。

### 第 ③ 步：交给 TUN 驱动，ghostwire 读到

内核调用 ghostwire0 的驱动程序发送这个包。真实网卡的驱动会把包变成电信号发到网线上；**TUN 驱动则把包放进一个队列**，这个队列连着 ghostwire 打开的 `/dev/net/tun` 文件。

ghostwire 的主循环 [`Responder.Serve`](../internal/responder/responder.go) 一直在读这个文件，于是读到了这 84 字节。对内核来说它"发送"了一个包，网卡的发送计数（`tx_packets`）加 1；对 ghostwire 来说它"读到"了一个包。

## ghostwire 内部

读到包之后，[`Responder.handle`](../internal/responder/responder.go) 依次：

1. 用 [`ParseProbe`](../internal/icmp/probe.go) 确认这是 ping 请求，取出目标地址 10.99.0.1
2. 用 [`RuleSet.DecideProbe`](../internal/engine/path.go)（没有路径的目标交给 [`RuleSet.Decide`](../internal/engine/ruleset.go)）按规则决定：回复、丢弃还是返回不可达，延迟多久。这里是"50ms 后回复"
3. 用 [`BuildEchoReply`](../internal/icmp/icmp.go) 构造应答：**对调源地址和目标地址**，ICMP 类型从 8（请求）改成 0（应答），重新计算校验和，**其余内容原样保留**，包括数据里 ping 写入的发送时间

然后 `Serve` 用 `time.AfterFunc` 在 50ms 后把应答写回网卡。如果规则决定丢弃，就什么都不写。详见[代码导读](architecture.zh-CN.md)；请求和应答里每个字节的含义，参见[数据包详解](packets.zh-CN.md)。

## 回程：应答怎样回到 ping

### 第 ④ 步：内核收到一个包

ghostwire 调用 `Write` 把应答写进文件，TUN 驱动就把它当作"ghostwire0 从网络上收到了一个包"交给内核，网卡的接收计数（`rx_packets`）加 1。从内核的角度看，这和真实网卡收到一个包没有区别。

### 第 ⑤ 步：反向路径检查

很多 Linux 系统会做**反向路径检查**（`rp_filter`）：收到一个源地址为 10.99.0.1 的包时，内核会想"如果我要回复 10.99.0.1，会从哪块网卡出去？"，如果和包进来的网卡不一致，可能会把它当作伪造的包丢掉。

查一下就知道：回复 10.99.0.1 走的正是 ghostwire0，和应答进来的网卡一致，所以检查一定通过，即使设置成最严格的模式也一样。劫持真实地址（见下文）时也是如此，因为 ghostwire 为它添加的路由同样指向 ghostwire0。

### 第 ⑥ 步：确认是给本机的

应答的目标地址是 10.99.0.254。内核查 local 表，发现这是本机自己的地址，于是收下它，而不是继续转发。

### 第 ⑦ 步：交给 ping

内核看到这是一个 ICMP 应答，就把它交给 ping 的套接字。机器上可能同时有多个 ping 在运行，ping 会通过 ICMP 头里的**标识**（每个 ping 进程不同）认出属于自己的应答，再通过**序号**对应到具体的那一次请求。

最后，ping 用当前时间减去应答数据里带回来的**发送时间**，得到延迟。因为 ghostwire 等了 50ms 才写回，ping 算出的就是 50ms。ghostwire 不需要修改时间戳，只需要**晚一点写回**。

## 两个延伸问题

### 为什么 ping 网卡自己的地址 10.99.0.254 不会被模拟？

因为第 ① 步的 local 表就拦下了它：10.99.0.254 是本机地址，内核通过回环网卡 `lo` 自己回复，包根本不会到达 ghostwire0。所以网段内**除了网卡自己的地址**，其他地址都是模拟主机。

### ghostwire 怎样模拟 8.8.8.8 这样的真实地址？

8.8.8.8 不在 10.99.0.0/24 网段里，正常会走默认路由发到真实网络上。为它设置规则时，[`Router.Ensure`](../internal/tun/tun_linux.go) 会执行：

```bash
ip route replace 8.8.8.8/32 dev ghostwire0
```

`/32` 只包含 8.8.8.8 这一个地址，是最精确的路由，在第 ① 步的最长前缀匹配中压过了 `default`，于是发往 8.8.8.8 的包也被送到了 ghostwire0。相邻的 8.8.4.4 不受影响。通过 API 删掉需要这条路由的最后一条规则或路径时，[`Router.Sync`](../internal/tun/tun_linux.go) 执行 `ip route del 8.8.8.8/32 dev ghostwire0`，8.8.8.8 重新走默认路由；它只撤 ghostwire 自己加的路由。ghostwire 退出时网卡被删除，它的路由也随之消失。

## IPv6

IPv6 的过程完全相同，只是：网卡地址是 `fd99::fe/64`，内核同样自动生成 `fd99::/64 dev ghostwire0` 的路由；ping 请求和应答的类型分别是 ICMPv6 的 128 和 129。设置 IPv6 地址时 ghostwire 加了 `nodad` 选项，跳过"重复地址检测"（DAD），否则地址要等检测完成（Linux 默认约 1 秒）才能使用。

## 自己动手验证

在 ghostwire 运行时，执行下面的命令就能看到上面的每一步：

| 想看什么 | 命令 | 应该看到 |
|---|---|---|
| 网卡和地址 | `ip addr show ghostwire0` | `inet 10.99.0.254/24` |
| 内核自动生成的路由 | `ip route show` | `10.99.0.0/24 dev ghostwire0 proto kernel` |
| 路由规则的查阅顺序 | `ip rule` | `0: lookup local` 在 `32766: lookup main` 之前 |
| 第 ① 步的路由查询结果 | `ip route get 10.99.0.1` | `dev ghostwire0 src 10.99.0.254` |
| 本机地址不走 ghostwire0 | `ip route get 10.99.0.254` | `local 10.99.0.254 dev lo` |
| ping 交给内核的内容 | `strace -e sendto ping -c 1 10.99.0.1` | 64 字节，以 `\10\0` 开头（类型 8） |
| 去程和回程的包数 | `cat /sys/class/net/ghostwire0/statistics/{tx,rx}_packets` | 每回复一个请求，发送和接收各加 1；被丢弃的请求只加发送 |
| 劫持真实地址的路由 | `-rule 8.8.8.8=30ms` 时 `ip route show` | `8.8.8.8 dev ghostwire0` |
| ghostwire 执行的命令 | `sudo strace -f -e trace=execve ghostwire -rule 8.8.8.8=30ms` | `ip addr add`、`ip link set ... up`、`ip -6 addr add`、`ip route show exact`、`ip route replace` |
