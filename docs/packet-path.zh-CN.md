# 一个 ping 的往返之旅

[English](packet-path.md) | 中文

这份文档跟随一个 ping 请求，从 `ping` 程序出发，经过内核到达 pingmock，再带着应答回到 `ping`。理解了这个过程，就理解了 pingmock 为什么**不需要修改被测软件**就能控制它看到的延迟。

下面以默认设置为例：pingmock 的虚拟网卡叫 `pingmock0`，地址是 `10.99.0.254/24`，有一条规则 `10.99.0.1=50ms`，然后有程序执行了 `ping 10.99.0.1`。

## 全景图

```
去程（请求）
  ping ──sendto("10.99.0.1")──► 内核
                                 │ ① 路由查询
                                 │    先查 local 表：10.99.0.1 不是本机地址
                                 │    再查 main 表：最长前缀匹配 → 10.99.0.0/24 dev pingmock0 src 10.99.0.254
                                 │ ② 封装 IP 头：目标 10.99.0.1，源地址取路由结果里的 10.99.0.254
                                 ▼
                           pingmock0（TUN 驱动）──► 放入队列 ──► pingmock 的 Read() 读到

pingmock 内部
  按规则决定怎么回应 → 构造应答 → 等待 50ms

回程（应答）
  pingmock 的 Write() ──► pingmock0 ──► 内核当作"从 pingmock0 收到一个包"
                                         │ ③ 反向路径检查：回复 10.99.0.1 也走 pingmock0，通过
                                         │ ④ 目标 10.99.0.254 在 local 表里，是本机地址，收下
                                         │ ⑤ 是 ICMP 应答，交给 ping 的套接字
                                         ▼
                                       ping 的 recvmsg() 收到应答，计算延迟：50ms
```

`ping` 自始至终只和内核打交道，完全不知道 pingmock 的存在。是内核按照路由表把请求交给了 pingmock，又把 pingmock 写回的应答当成网络上来的包交给了 `ping`。

## 准备：pingmock 启动时做了什么

pingmock 启动时只对系统做了两件事，代码在 [`tun.Open`](../internal/tun/tun_linux.go) 和 [`configureDevice`](../internal/app/instance.go)：

1. **创建虚拟网卡 `pingmock0`**：打开 `/dev/net/tun`，用 `ioctl(TUNSETIFF)` 让内核创建一块 TUN 网卡，并绑定到这个打开的文件上。之后 pingmock 读写这个文件，就等于在这块网卡上收发包。
2. **给网卡设置地址并启用**：执行 `ip addr add 10.99.0.254/24 dev pingmock0` 和 `ip link set pingmock0 up`。地址来自 `-addr` 参数（或配置文件里的 `addr`）。

第 2 步之后，**内核自动**在两张表里各登记了一条记录：

| 表 | 记录 | 含义 |
|---|---|---|
| local 表 | `local 10.99.0.254 dev pingmock0` | 10.99.0.254 是本机自己的地址 |
| main 表 | `10.99.0.0/24 dev pingmock0 src 10.99.0.254` | 10.99.0.0～10.99.0.255 都从 pingmock0 发出 |

pingmock 并没有直接修改路由表。`/24` 表示地址的前 24 位（`10.99.0`）是网段，内核据此推断"这个网段的地址都在 pingmock0 那一侧"，自动生成了 main 表里的路由。

## 去程：请求怎样到达 pingmock

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
| `10.99.0.0/24 dev pingmock0` | 包含 | **/24，更精确，选中** |

查询结果是 `10.99.0.1 dev pingmock0 src 10.99.0.254`：从 pingmock0 发出，源地址用 10.99.0.254。

### 第 ② 步：封装 IP 头

内核给 64 字节的 ICMP 内容加上 20 字节的 IP 头：目标地址 10.99.0.1，**源地址取路由查询结果里的 10.99.0.254**。必须先查路由才能填源地址，因为从不同网卡发出，源地址就不同。这样就得到了一个 84 字节的 IP 包。

### 第 ③ 步：交给 TUN 驱动，pingmock 读到

内核调用 pingmock0 的驱动程序发送这个包。真实网卡的驱动会把包变成电信号发到网线上；**TUN 驱动则把包放进一个队列**，这个队列连着 pingmock 打开的 `/dev/net/tun` 文件。

pingmock 的主循环 [`Responder.Serve`](../internal/responder/responder.go) 一直在读这个文件，于是读到了这 84 字节。对内核来说它"发送"了一个包，网卡的发送计数（`tx_packets`）加 1；对 pingmock 来说它"读到"了一个包。

## pingmock 内部

读到包之后，[`Responder.handle`](../internal/responder/responder.go) 依次：

1. 用 [`ParseEchoRequest`](../internal/icmp/icmp.go) 确认这是 ping 请求，取出目标地址 10.99.0.1
2. 用 [`RuleSet.Decide`](../internal/engine/ruleset.go) 按规则决定：回复、丢弃还是返回不可达，延迟多久。这里是"50ms 后回复"
3. 用 [`BuildEchoReply`](../internal/icmp/icmp.go) 构造应答：**对调源地址和目标地址**，ICMP 类型从 8（请求）改成 0（应答），重新计算校验和，**其余内容原样保留**，包括数据里 ping 写入的发送时间

然后 `Serve` 用 `time.AfterFunc` 在 50ms 后把应答写回网卡。如果规则决定丢弃，就什么都不写。详见[代码导读](architecture.zh-CN.md)。

## 回程：应答怎样回到 ping

### 第 ④ 步：内核收到一个包

pingmock 调用 `Write` 把应答写进文件，TUN 驱动就把它当作"pingmock0 从网络上收到了一个包"交给内核，网卡的接收计数（`rx_packets`）加 1。从内核的角度看，这和真实网卡收到一个包没有区别。

### 第 ⑤ 步：反向路径检查

很多 Linux 系统会做**反向路径检查**（`rp_filter`）：收到一个源地址为 10.99.0.1 的包时，内核会想"如果我要回复 10.99.0.1，会从哪块网卡出去？"，如果和包进来的网卡不一致，可能会把它当作伪造的包丢掉。

查一下就知道：回复 10.99.0.1 走的正是 pingmock0，和应答进来的网卡一致，所以检查一定通过，即使设置成最严格的模式也一样。劫持真实地址（见下文）时也是如此，因为 pingmock 为它添加的路由同样指向 pingmock0。

### 第 ⑥ 步：确认是给本机的

应答的目标地址是 10.99.0.254。内核查 local 表，发现这是本机自己的地址，于是收下它，而不是继续转发。

### 第 ⑦ 步：交给 ping

内核看到这是一个 ICMP 应答，就把它交给 ping 的套接字。机器上可能同时有多个 ping 在运行，ping 会通过 ICMP 头里的**标识**（每个 ping 进程不同）认出属于自己的应答，再通过**序号**对应到具体的那一次请求。

最后，ping 用当前时间减去应答数据里带回来的**发送时间**，得到延迟。因为 pingmock 等了 50ms 才写回，ping 算出的就是 50ms。pingmock 不需要修改时间戳，只需要**晚一点写回**。

## 两个延伸问题

### 为什么 ping 网卡自己的地址 10.99.0.254 不会被模拟？

因为第 ① 步的 local 表就拦下了它：10.99.0.254 是本机地址，内核通过回环网卡 `lo` 自己回复，包根本不会到达 pingmock0。所以网段内**除了网卡自己的地址**，其他地址都是模拟主机。

### pingmock 怎样模拟 8.8.8.8 这样的真实地址？

8.8.8.8 不在 10.99.0.0/24 网段里，正常会走默认路由发到真实网络上。为它设置规则时，[`Router.Ensure`](../internal/tun/tun_linux.go) 会执行：

```bash
ip route replace 8.8.8.8/32 dev pingmock0
```

`/32` 只包含 8.8.8.8 这一个地址，是最精确的路由，在第 ① 步的最长前缀匹配中压过了 `default`，于是发往 8.8.8.8 的包也被送到了 pingmock0。相邻的 8.8.4.4 不受影响。pingmock 退出时网卡被删除，这条路由也随之消失。

## IPv6

IPv6 的过程完全相同，只是：网卡地址是 `fd99::fe/64`，内核同样自动生成 `fd99::/64 dev pingmock0` 的路由；ping 请求和应答的类型分别是 ICMPv6 的 128 和 129。设置 IPv6 地址时 pingmock 加了 `nodad` 选项，跳过"重复地址检测"（DAD），否则地址要等检测完成（Linux 默认约 1 秒）才能使用。

## 自己动手验证

在 pingmock 运行时，执行下面的命令就能看到上面的每一步：

| 想看什么 | 命令 | 应该看到 |
|---|---|---|
| 网卡和地址 | `ip addr show pingmock0` | `inet 10.99.0.254/24` |
| 内核自动生成的路由 | `ip route show` | `10.99.0.0/24 dev pingmock0 proto kernel` |
| 路由规则的查阅顺序 | `ip rule` | `0: lookup local` 在 `32766: lookup main` 之前 |
| 第 ① 步的路由查询结果 | `ip route get 10.99.0.1` | `dev pingmock0 src 10.99.0.254` |
| 本机地址不走 pingmock0 | `ip route get 10.99.0.254` | `local 10.99.0.254 dev lo` |
| ping 交给内核的内容 | `strace -e sendto ping -c 1 10.99.0.1` | 64 字节，以 `\10\0` 开头（类型 8） |
| 去程和回程的包数 | `cat /sys/class/net/pingmock0/statistics/{tx,rx}_packets` | 每回复一个请求，发送和接收各加 1；被丢弃的请求只加发送 |
| 劫持真实地址的路由 | `-rule 8.8.8.8=30ms` 时 `ip route show` | `8.8.8.8 dev pingmock0` |
