# TODO

已发布 v0.14.0（见 [Releases](https://github.com/nanmuyao/pingmock/releases)）：v0.9.0 TCP 连接、v0.10.0 `after=`、v0.11.0 DNS 故障、v0.12.0 连接上发送数据、v0.13.0 UDP 规则、v0.14.0 冻结前的整理，详见下面的「多协议」。更早的：v0.2–v0.4 的规划全部完成，v0.5 改用了 YAML 配置文件；v0.5.1 没有改变任何行为，加入了代码结构重构、3 份技术文档（[往返之旅](docs/packet-path.zh-CN.md)、[数据包详解](docs/packets.zh-CN.md)、[代码导读](docs/architecture.zh-CN.md)）、5 个[完整示例](examples/README.zh-CN.md)，以及在 Ubuntu 26.04 上的测试；v0.5.2 修复了 root 模式会替换本机所在网络路由的 bug。

接下来的重点不是继续加功能，而是**让真实用户用起来**，再根据反馈决定方向。

## v0.5 的收尾（已完成，推广暂缓）

接下来的工作见[技术路线](#技术路线)。

### 1. 升级 GitHub Actions 的版本

- [x] CI 出现了 Node.js 20 弃用警告：`actions/checkout@v4`、`actions/setup-go@v5` 等使用的 Node.js 20 已被 GitHub 弃用，将来可能停止支持
- [x] 升级 `.github/workflows/` 中的全部 action（包括 `setup-python`、`docker/*`），以及 [`examples/github-actions/pingmock-tests.yml`](examples/github-actions/pingmock-tests.yml)：用户复制这份示例时会遇到同样的问题
- [x] 只选择发布超过两周的版本；升级后确认 CI 的三个任务都通过，且不再出现弃用警告
- [x] 在 Ubuntu 26.04 上测试：`ubuntu-latest` 在 2026-10-19 至 2026-11-19 之间从 24.04 迁移到 26.04。CI 的三个任务改为在 `ubuntu-24.04` 和 `ubuntu-26.04` 上各运行一遍，全部通过；新增的 Environment 步骤显示 26.04 上 AppArmor 依然限制用户命名空间，所以 AppArmor 配置在 26.04 上同样必要且有效。发布流程按原计划继续固定在 24.04
- [x] 26.04 上的 iputils 是 20250605，会对校验和错误的回显应答标出 `(BAD CHECKSUM!)`（20240117 的 bug 在 20240905 修复），已更新[数据包详解](docs/packets.zh-CN.md#9-校验和算错会怎样)第 9 节

### 2. 发布 v0.5.1

- [x] 没有功能变化，但积累了不少改进（见上），发布后用户在 Releases 页面才能看到：已发布，对比 v0.5.0 和 v0.5.1 的构建，帮助信息、错误提示、退出码和 API 返回的数据都相同，更新说明中如实写明
- [x] 升级 action 之后必须跑一次发布流程，才能确认发布流程依然正常，所以放在第 1 项之后：发布流程的两个任务都在 Ubuntu 24.04 上成功；文件校验、两种架构的镜像、`go install` 都验证过
- [x] 发布后按 [CONTRIBUTING](CONTRIBUTING.md#releasing) 更新示例中的版本号：`examples/github-actions/pingmock-tests.yml` 的 `PINGMOCK_VERSION`，`examples/go-demo/go.mod` 的 `require`，都已改为 v0.5.1

### 3. README 开头加一段演示动画

- [x] 用真实的命令输出生成（[asciinema](https://asciinema.org) 或 SVG），展示一条 `pingmock exec` 命令怎样改变程序的选择，例如 [go-demo](examples/go-demo/) 的 `fastest` 程序在不同规则下选出不同的服务器：[`docs/demo/record.sh`](docs/demo/record.sh) 录制，结果在 [`docs/demo/demo.svg`](docs/demo/demo.svg)
- [x] 重录演示：原来的录像在没有 IPv6 的容器里录制，开头有一行 IPv6 不可用的提示，版本号也是临时版本号。已在 GitHub 的机器上（有 IPv6）基于 v0.5.1 标签重录，用一个只存在于临时分支的工作流完成，录完后删除了这个分支
- [x] 写一段 30 秒能看懂的介绍：解决什么问题 → 一个最短的示例 → 和 `tc netem` 的区别（按目标地址设置、运行中可修改、不需要 root）
- [x] README 开头加一条 Docker 一键演示：不用安装任何东西、不用克隆仓库，一条 `docker run` 命令就能看到延迟变化和不可达。CI 从中英文 README 中提取这条命令，用本次构建的镜像和已发布的镜像，在 Ubuntu 24.04 和 26.04 上各运行一遍

### 4. 准备收集反馈的渠道（要在发帖之前完成）

- [x] 开启 GitHub Discussions，用来回答使用问题、讨论想法（issue 模板的「Question or idea to discuss」链接指向它）
- [x] 添加 issue 模板：bug 报告（版本、系统、复现步骤）和功能建议（使用场景）

### 5. 起草推广帖子，由作者本人发布

暂缓，移到[最低优先级](#最低优先级)。

## 已修复：root 模式会替换本机所在网络的路由

- [x] 用 root 模式对**本机网卡所在的网络**设置规则时（比如本机是 `192.168.20.5/24`，规则写 `192.168.20.0/24`），v0.5.1 及之前的版本用 `ip route replace` 替换掉了这个网络原有的路由，退出后也不会恢复，本机就连不上那个网络了。`pingmock exec` 不受影响
- [x] 修复：添加路由前先查询是否已有完全相同网段的路由；有的话拆成两个更精确的半网段（再冲突就继续拆），原有的路由保持不动，pingmock 被强制终止也不会留下问题；单个地址已有自己的路由时拒绝启动，并建议使用 `pingmock exec`。单元测试覆盖拆分逻辑，端到端测试覆盖 IPv4 和 IPv6，并确认 v0.5.1 会在这项测试上失败
- [x] 发布 v0.5.2：已发布，用发布页下载的程序重复 VLAN 实验，运行中和退出后原有的路由都保留；示例中的版本号已更新为 v0.5.2

## 技术路线

路线的概览和竞品对比见[技术路线图](docs/roadmap.zh-CN.md)。这条路线是怎样得出的、依据了哪些证据，记录在[技术路线是怎样得出的](docs/planning.zh-CN.md)中。下一次规划时，按那份文档的方法重做一遍。

**四个核心价值，每个协议都要保持**：

| 价值 | 含义 |
|---|---|
| 透明 | 被测程序不需要修改代码或配置，照常访问原来的地址、域名或端口 |
| 按目标 | 每个目标（地址、网段，以及其他协议的域名、端口）可以有自己的网络状况 |
| 可复现 | 同样的设置，每次运行的结果都一样（`-seed`、确定性的 `pattern`、场景、`POST /reset`） |
| 可控、可观察 | 测试中可以通过 API 改变网络状况，事后能从历史和统计中看到程序实际做了什么 |

pingmock 将来支持的每一个协议都必须同时做到这四点。**新协议的设计如果做不到其中某一点，就要重新考虑。**（定义和例子见 [CONTRIBUTING](CONTRIBUTING.md#design-principles)）

**另一条原则**：测试应用，而不是测试协议。**用户**按人数从多到少：

| 用户 | 他们要测什么 |
|---|---|
| ① 应用开发者 | 程序在网络变慢、断开、不可达时的表现。pingmock 的核心 |
| ② ping 库的开发者 | 超时、不可达、丢包、TTL 等行为（pro-bing 的测试依赖 `www.google.com`） |
| ③ 运维人员 | 监控告警规则（宕机多久告警、丢几个包算故障）、线路切换脚本。他们测试告警和切换的办法是关机、拔线或加防火墙规则（Zabbix 工单 ZBX-19712）；Zabbix 和 Prometheus 能测规则的逻辑，缺的是端到端的测试。注意：原先写在这里的「Zabbix 用户只能改小阈值」「Uptime Kuma 每秒 ping」经核对不成立或找不到依据，见 [docs/planning 第二轮](docs/planning.zh-CN.md#第二轮2026-年-10-月-20-日)。他们最常用的切换工具 mwan3 把 ping 绑定到网卡，目前大概率无法模拟（见下一步 §2） |
| ④ 网络诊断工具的开发者 | traceroute / mtr 类工具对路径的处理（Trippy #1552、#1554） |

每一版都能独立发布、独立带来价值。每一版都按这个流程：

1. 用户可见的写法先出设计方案，和维护者确认
2. 动手之前，用实验验证技术前提
3. 实现：单元测试、端到端测试、兼容性测试，并确认测试能发现问题
4. 中英文文档、示例同步更新
5. 发布，用发布页下载的程序再验证一遍

### 已完成，已作为 v0.8.0 发布

v0.5.2 之后，原计划中 v0.6（测试体验）、v0.7（域名）、v0.8（路径）的功能都已实现，一起作为 **v0.8.0** 发布（没有单独的 v0.6.0 和 v0.7.0）。

**兼容性测试进 CI**

- [x] 把 fping、pro-bing、icmplib 的测试固定下来（`test/e2e-compat.sh`），在 Ubuntu 24.04 和 26.04 上运行，保护后面对 `icmp`、`engine` 的修改：20 项检查（3 个工具、两种套接字、4 种情况），变异测试确认能发现问题；README 新增了经过测试的工具和库，并在限制中写明 traceroute 和 mtr 目前不能使用
- [x] 测试用的程序库固定版本，放在独立的目录里，不给主模块增加依赖：`test/compat` 是独立的模块，pro-bing v0.9.1、icmplib 3.0.4

**测试体验**

- [x] `POST /reset`：规则、默认规则、统计和历史都恢复到启动时（命令行和配置文件）的样子，让每个测试从干净的状态开始。每个目标的场景位置和随机序列也重新开始，所以同一 seed 下 reset 之后的结果和刚启动时完全一样（有测试验证）
- [x] `GET /version`：返回 pingmock 的版本，方便测试确认用的是哪个版本
- [x] Go 客户端增加 `Reset` 和 `Version`；python-demo 的 fixture 改用 `/reset`：去掉了保存、逐条恢复规则的代码，改为每个测试开始前 reset
- [x] 自定义回复的 TTL（比如 `ttl=128`）：测试程序对 TTL 的处理。证据：pro-bing #116（记录收到的 TTL）、#27（设置 TTL），go-ping #192（TTL 和预期不符）。已实现：规则多了一项 `ttl=1..255`，文本、YAML 映射和 API 的 JSON 都支持，每个阶段可以不同；回显应答和不可达消息的 TTL / hop limit 都用它；不写时仍是 64，行为和以前完全一样。单元测试覆盖解析、JSON、决策和数据包，端到端测试确认 `ping` 显示 `ttl=128`
- [x] 定位说明：README 中说明 pingmock、`tc netem`、Toxiproxy 各适合什么场景（给真实流量加延迟用 netem，TCP 层的故障用 Toxiproxy，测试依赖 ping 的程序用 pingmock）；突出配置文件中声明故障、可复现的随机、按网段设置，这是 Toxiproxy 用户呼声最高的需求（#281、#447、#274、#273）。已加在中英文 README「原理」之后的「什么时候用它，什么时候用别的」一节：一张三行的表，加一段说明两者的局限和 pingmock 借鉴了什么
- [x] 新示例：测试 ping 库的超时和不可达行为，服务用户 ②，场景参考 pro-bing #70（超时没有返回错误、不可达的主机让 `Run()` 一直阻塞）。已加 [`examples/pinglib-demo`](examples/pinglib-demo/)：原样测试 pro-bing v0.9.1，配置文件里五台主机各代表一种行为（回复、不回应、不可达、隔一个丢一个、TTL 128），五个测试验证了：超时通过统计体现而不是错误；不可达在库看来和丢包一样，pingmock 的历史能看出区别；`Count` 数的是回复，有丢包时会等到超时；TTL 原样到达。CI 在 exec 下免 root 运行（原始套接字在用户命名空间里可用）
- [x] README 的限制中写明：绑定网卡的 ping（`ping -I eth0`）不会被模拟（已实验验证：绑定真实网卡时直接从那块网卡发出；绑定源地址时照常被模拟），因为 README 现在明确承诺了"透明"

**域名：`pingmock exec` 中的 `-host`、按域名的规则和内置的 DNS 服务**

- [x] 前提已验证：普通用户可以在用户命名空间 + 挂载命名空间中覆盖 `/etc/hosts`；glibc、Go（纯 Go 解析器）、Python 都读到新的内容；宿主机的文件不受影响
- [x] `exec` 增加挂载命名空间，实现 `-host`：给了 `-host` 时父进程多加 `CLONE_NEWNS`，子进程先把挂载设为私有，再把「pingmock 的条目 + 系统的 `/etc/hosts`」写进一个临时文件，bind mount 到 `/etc/hosts` 上，然后删除临时文件（挂载让它继续存在，不留痕迹）。配置文件支持 `hosts:` 映射，`-host` 按名字覆盖。root 和普通用户都验证过；端到端测试检查 `getent`、按名字 ping 的延迟、系统文件的 md5 不变、`/tmp` 没有残留
- [x] root 模式下继续推荐改 `/etc/hosts` 的做法，在 README 中写清楚：`sudo pingmock -host` 直接拒绝并提示改 `/etc/hosts`；README 的「域名：-host」一节写明
- [x] 回答 `-host` 和按域名写的规则，自动分配模拟地址：`-rule api.example.com=80ms` 和配置文件 `rules:` 里的域名键都可以；地址从 `-addr` 网段的高端往下分配（`.253`、`.252`……，跳过已被明确规则或 `-host` 占用的地址），按域名排序所以每次一样；`-host` 指定的地址优先。`internal/dns` 是一个不依赖第三方库的小 DNS 服务（只处理单个问题的 UDP 查询，A/AAAA/PTR），监听在命令网络里的 `127.0.0.1:53`；`resolv.conf` 保留宿主机的 `search`/`options`，只换 `nameserver`；`nsswitch.conf` 里有 `resolve` 时同样替换成 `files dns`
- [x] 为模拟的地址提供反向解析（v0.8 中 traceroute 显示中间节点的名字）：PTR 回答该地址的第一个名字，`getent hosts 10.99.0.253` 得到域名
- [x] 不认识的域名，通过仍在宿主机网络中的父进程转发给真实的 DNS 服务器：父子进程之间一对 Unix 域套接字（子进程的 fd 3），子进程把原始查询按长度前缀转发，父进程按宿主机 `resolv.conf` 的顺序逐个询问（每个 2 秒），回答原样传回；都失败时回 SERVFAIL。用桩解析器验证过：模拟的域名从不转发，未知域名得到真实解析器的回答（含 NXDOMAIN）；端到端测试在能解析 `github.com` 的机器上检查它在 `exec` 里也能解析
- [x] 动手之前先逐项验证（来自 slirp4netns 用户踩过的坑）：`/etc/resolv.conf` 是符号链接（包括多层）；DNS 服务器是 `127.0.0.53`（systemd-resolved，在 `exec` 的独立网络中访问不到）；多个 DNS 服务器；搜索域；IPv6 的 AAAA 记录；父进程转发的可行性；默认网段和用户网络重叠。**已用原型验证**（一个 Go 程序：子进程在 `exec` 里跑 DNS 服务并替换 `resolv.conf`，父进程留在宿主机网络里转发；宿主机的 DNS 用一个只认识 `real.example` 的桩程序模拟，监听在 `127.0.0.53`）：

**路径：traceroute 和 mtr**

设计方案的几个待决定的问题，在实现中都按方案落实了：每一跳写成 `地址@规则`，用 `>` 连接，`*` 表示不回应；目标和每一跳都自动加路由（严格的反向路径检查下的必要条件）；只有设置了路径的目标才理会 TTL、回应 UDP 和 TCP 探测，其他目标的行为不变。调研中补充的几点也都实现了：

- [x] TCP 探测：终点回 SYN-ACK，traceroute 的 TCP 模式能确认到达终点
- [x] 两种丢包：`loss=` 是只是不回应（ICMP 限速，只影响这一跳），`cut=` 是链路丢包（经过这一跳的包都丢失，之后的跳和终点都受影响）。依据 Trippy #1554
- [x] IPv6：TTL 超时用 ICMPv6 类型 3、代码 0，端口不可达用类型 1、代码 4；引用原包不超过 1280 字节
- [x] 规则层：解析路径的写法，配置文件和 API 中的路径：`rule.Path`/`rule.Hop`，`ParsePath`；每跳 `delay`、`jitter`、`loss`（这一跳不回应）、`cut`（链路丢包，之后的跳和终点都收不到）、`ttl`；`-path` 参数、配置文件 `paths:`（以 `*` 开头要加引号，错误信息会提示）、`PUT/DELETE /paths/{target}`、Go 客户端 `SetPath`/`DeletePath`；目标可以是域名（`exec` 下，和规则共用地址分配）
- [x] 决策层：按 TTL 选择中间节点或终点，中间节点的随机数和丢包同样可以用 `-seed` 复现：`RuleSet.DecideProbe`，经过的每跳先按 `cut` 判断是否丢失，TTL 耗尽的那跳按 `loss` 判断是否回应，超过路径长度则由终点原有规则决定——回显请求照旧，UDP 探测回 Port Unreachable，TCP SYN 回 SYN-ACK；没有路径的目标行为完全不变（不理会 TTL，忽略 UDP/TCP）。`/reset` 也恢复路径
- [x] 数据包：构造 ICMP Time Exceeded、Port Unreachable（IPv4 和 IPv6），引用原包的方式和现有的不可达消息相同：`icmp.ParseProbe` 统一解析回显请求、UDP、TCP SYN 并读出 TTL；`BuildTimeExceeded`、`BuildPortUnreachable`、`BuildSynAck`（IPv4 和 IPv6，TCP 校验和含伪头部）；原有的不可达消息改用同一个构造函数
- [x] 路由：为中间节点的地址添加路由；先验证 IPv6：启动和 API 设置路径时为目标和每一跳加 /32 或 /128 路由。IPv6 在本容器无法运行，端到端测试里 IPv6 路径的 traceroute 检查放在 CI（`REQUIRE_IPV6=1`）
- [x] 历史和统计：记录中间节点的回复（结果为 time-exceeded、port-unreachable，以及第几跳）：事件新增 `probe`（udp/tcp）、`ttl`、`hop`、`from`；统计新增 `hops`（由中间节点回应的探测数）
- [x] traceroute、mtr 加入兼容性测试；中英文文档：`test/e2e.sh` 用 traceroute 的 UDP/ICMP/TCP 三种模式和 mtr 检查路径、第二跳的延迟、`cut` 的效果、历史记录，IPv6 路径在 CI 检查；`test/e2e-exec.sh` 在免 root 的 `exec` 里跑 traceroute；CI 安装 traceroute 和 mtr-tiny。README 新增「路径：traceroute 和 mtr」一节，限制一节和「经过测试的工具」同步更新。本容器里实测：traceroute 三种模式和 mtr 都正确显示四跳和终点（`*` 那一跳显示为 `*`/`???`），第二跳延迟 13～18ms（15ms±3ms 抖动）

原型实验验证过的前提（实现时都用到了）：

用一个 Python 原型（TUN 网卡上假装有两个中间节点）验证过：

| 前提 | 结果 |
|---|---|
| pingmock 读到的 TTL 是程序设置的原始值 | 成立：traceroute 发出的 TTL 1～5，原型都按原值看到 |
| traceroute -I、traceroute（UDP）、mtr 能看到伪造的中间节点和终点 | 成立：三种都正确显示了两个中间节点和终点 |
| 严格的反向路径检查（`rp_filter=1`）下依然可用 | **只有为中间节点的地址添加指向 TUN 网卡的路由时才成立**。不加路由时，中间节点全部显示为 `*`，内核计数 `IPReversePathFilter` 显示丢弃了这些回复 |

IPv6（ICMPv6 Time Exceeded、Port Unreachable、hop limit）还没有验证。

**还没有做的**：

- Trippy 的场景验收、自动生成中间节点的简化写法：移到[最低优先级](#最低优先级)

## 下一步

### 1. 发布 v0.8.0（已完成）

- [x] 发布前对比 v0.5.2 和当前代码中用户可见的行为：错误提示、退出码、种子序列都相同，帮助信息、`GET /rules`、历史事件只有新增；对比中发现普通 ping 的历史事件多了 `"from": ""`（`omitempty` 对结构体类型不起作用），发布前修正，并加了测试
- [x] 更新说明以另一个对话起草的 v0.7.0 草稿为基础，补充了路径模拟，如实写出 API 新增的字段；说明中的链接都检查过
- [x] 在 Ubuntu 24.04 和 26.04 的 CI 上通过，包括本地无法运行的 IPv6 路径检查；用发布页下载的程序验证了 traceroute、mtr、域名和种子序列
- [x] 发布后更新了示例中的版本号（github-actions、go-demo、pinglib-demo）；用完的草稿 `notes.txt` 由另一个对话删除，并加入了 `.gitignore`（发布说明由标签携带，不放在仓库里）

### 2. v0.6 剩下的一项，以及调研带出的一项实验

Python 客户端包和 Prometheus 告警规则的示例移到了[最低优先级](#最低优先级)：**所有示例都放到最后做**，技术项优先。

- [x] 把市场调研的结果补充到 [docs/planning](docs/planning.zh-CN.md) 的证据部分：原先的调研材料不在仓库里，由新的 [`pingmock-planning`](.claude/agents/pingmock-planning.md) agent 重新调研，结论逐条打开来源核对后写入「第二轮：2026 年 10 月 20 日」。运维人员的两条依据不成立或找不到；「七条技术路线」没有来源，按项目分组是七到八条，新增 LD_PRELOAD 拦截；绑定网卡的限制成立，而且 mwan3 用 LD_PRELOAD 绑定。WireMock 的类比、gVisor 的依赖代价、Testcontainers 的限制只是部分有依据，还没核对，没有写入，等做技术选型时再查
- [x] **新**：实验验证 mwan3 式的探测能不能被模拟（2026-10-20，开发容器，规则 `8.8.8.8=30ms`）：

  | 情况 | 结果 |
  |---|---|
  | root 模式，`ping -I eth0` 绑定真实网卡 | 绕过 pingmock，真实延迟 57～70ms、ttl=63 |
  | `exec` 里绑定不存在的网卡 `wan` | 失败：`SO_BINDTODEVICE wan: No such device` |
  | `exec -dev wan`，pingmock 的网卡起名为 `wan`，再绑定它 | **被模拟**，30.8～32.0ms |
  | 同上，加 `SO_MARK` 和源地址绑定（mwan3 包装器的做法） | 被模拟，31.5ms |

  结论：在 `exec` 里，只要网卡名字对得上，绑定网卡的探测今天就能模拟。多 WAN 切换还需要**多块网卡、规则按网卡区分**（同一个 8.8.8.8，经 wan 30ms、经 wanb 超时），mwan3 本身依赖 ubus、uci，能否在普通 Linux 上直接测它要另外验证。**多网卡暂缓**（维护者决定，2026-10-20），先看多协议方向

### 3. 开发环境：在 Mac 上用 Docker 开发（已完成）

维护者用 Mac，pingmock 只能在 Linux 上运行。Mac 上的 Docker Desktop 在 Linux 虚拟机中运行容器，所以可以在容器里开发。Claude Code 官方文档中有开发容器的说明（命令在容器里执行，文件修改实时出现在本地仓库），参考配置以非 root 用户运行，并加了 `NET_ADMIN` 权限，这也正是 pingmock 需要的。

- [x] 增加 [`.devcontainer/`](.devcontainer/)：`Dockerfile`（`golang:1.27.0-bookworm`；iproute2、ping、curl、traceroute、mtr、fping、带 icmplib 和 pytest 的 Python、shellcheck、git；Claude Code 用原生安装脚本装给普通用户）和 `devcontainer.json`。以普通用户 `dev` 运行，免密码 `sudo`；Go 的模块缓存和构建缓存放在两个卷里，重建容器不用重新下载；登录 shell 的 PATH 里补上 Go，因为 Debian 的 `/etc/profile` 会覆盖镜像设置的 PATH，否则 VS Code 和 devcontainer CLI 的终端里找不到 `go`
- [x] 先验证（在这台 Mac 上，Docker Desktop 4.68、Engine 29.3.1）：

  | 前提 | 结果 |
  |---|---|
  | 只给 `NET_ADMIN` 和 `/dev/net/tun` | root 模式能跑；`pingmock exec` 免 root 和 root 都失败：Docker 默认的 seccomp 策略不允许创建命名空间（`clone` 带命名空间标志要容器有 `CAP_SYS_ADMIN`），连 root 也不行 |
  | 再关掉 seccomp | 免 root 的 `exec` 能跑；root 的 `exec` 仍失败，容器里的 root 没有 `CAP_SYS_ADMIN`，创建网络命名空间本身需要它 |
  | 改为再加 `SYS_ADMIN`，seccomp 保持默认 | 全部能跑。**这就是最小权限**，不需要 `--privileged`，也不用关 seccomp |
  | Linux 主机上的 AppArmor（CI 发现） | Mac 上测不到：Ubuntu 上 Docker 默认的 AppArmor 配置 `docker-default` 禁止 `mount`，免 root 的 `exec` 设置私有 `/etc/hosts` 时报 `making mounts private: permission denied`。再加 `--security-opt apparmor=unconfined`；Mac 上没有 AppArmor，加了没影响 |
  | IPv6 | Docker Desktop 的容器里是开着的，e2e 的 IPv6 检查也跑了（46 项全过）；Linux 上的 Docker 默认关闭，相关检查会跳过 |
  | 和 Linux 的差别 | Docker Desktop 的解析器对任何域名都回一个虚构地址（`fdfe:dcba:9876::/48`），「未知域名解析不到」的检查改为在宿主机也能解析该域名时跳过；`/proc/sys` 只读，兼容性检查改为只在需要时写 `ping_group_range`（容器本来就允许所有组）；虚拟机里的时间抖动让容差 ±3ms、±5ms 的检查各失败过一次，都发生在我同时构建镜像的时候，单独跑全过 |
  | 各项用时 | `make test` 约 15 秒，e2e 25 秒，e2e-exec 23 秒（两种模式），e2e-compat 第一次 51 秒、有缓存后 31 秒。时间都花在等超时和等 mtr 上 |

  顺带发现：容器里的 root 没有 `CAP_SYS_ADMIN` 时，`pingmock exec` 走 root 路径直接失败。可以改成没有这个能力时就像普通用户一样走用户命名空间；暂不做，等有人遇到
- [x] CI 增加 `devcontainer` 任务（Ubuntu 24.04 和 26.04）：构建开发容器，在里面以 `dev` 用户运行 `make lint`、`make test`、`make e2e`、`make e2e-exec`、`make e2e-compat`。容器用户取 runner 的 uid，才能写挂载进来的仓库；先在 runner 上关掉 Ubuntu 对非特权用户命名空间的 AppArmor 限制，因为容器里用不上宿主机的 profile，Mac 上也没有这个限制（`test` 任务继续覆盖 profile）。第一次在 CI 上运行时两个系统都失败在免 root 的 `exec`（Ubuntu 上 Docker 默认的 AppArmor 配置禁止 mount，见上表），加上 `--security-opt apparmor=unconfined` 后 [CI 全部通过](https://github.com/nanmuyao/pingmock/actions/runs/39371647091)：devcontainer 任务在 24.04 和 26.04 上都跑完了五项检查
- [x] 中英文使用说明：[`.devcontainer/README.md`](.devcontainer/README.md)（[中文](.devcontainer/README.zh-CN.md)）：怎样打开（VS Code、devcontainer CLI、纯 Docker）、哪些检查在容器里跑、哪些交给 CI、需要哪些权限、和 Linux 的差别、各项用时。在这台 Mac 上验证了纯 Docker 和 devcontainer CLI 0.89.0 两种方式；CLI 0.80.0 在 Docker Desktop 4.68 上启动容器后一直卡在等 `docker events`，文档里写明要用新版。VS Code 没有装，「Reopen in Container」没有试
- [x] 增加 [`CLAUDE.md`](CLAUDE.md)：四个核心价值、测试应用而不是协议、证据优先、验证后再下结论、依赖要发布超过两周、中英文文档同步、seed 序列是行为的一部分、不提交来源不明的文件、TODO 保持最新、提交信息的格式，以及在哪里跑哪些测试

另一个选择是 OrbStack、Lima 等工具提供的 Linux 虚拟机：和 CI 更接近，免 root 测试、IPv6、Docker 都不需要额外处理，但更重。

### 4. 多协议

**名字保持 pingmock，后续支持更多的协议，这是已经确定的方向。** 每个协议都必须保持四个核心价值（见本节开头）；设计做不到其中某一点，就要重新考虑。

现在的基础：`exec` 中已经有一个 DNS 服务（只回答模拟的域名、转发其他的）；路径模拟已经能解析 UDP 和 TCP SYN、回复 SYN-ACK。

**顺序改为 TCP 优先（维护者决定，2026-10-20）。** 依据：planning agent 的多协议调研（技术选型 + 设计方案），关键结论人工核对过，见 [docs/planning 第三轮](docs/planning.zh-CN.md#第三轮多协议2026-年-10-月-20-日)。TCP 有人自己搭「黑洞」测连接超时的直接证据，同一个机制在 root 和 `exec` 下都能用；DNS 故障没有找到需求证据，root 模式也难以透明。

| 版本 | 内容 |
|---|---|
| v0.9.0 TCP 连接（**已发布**，2026-10-20） | `-tcp 地址[:端口]=规则`、配置文件 `tcp:`、`PUT/DELETE /tcp/{目标}`、Go 客户端 `SetTCP`/`DeleteTCP`、历史和统计。延迟、抖动、丢包、`timeout`、`refuse`（回 RST）、`unreachable`、pattern 的 `r`、`ttl`、场景。规则只管连接是否、何时建立；建立之后不发数据（和现在的路径模拟一样） |
| v0.10.0 连接建立后的行为（**已发布**，2026-10-20） | `after=close\|reset\|hang`，需要解析非 SYN 的报文、回应客户端的 FIN；设计见下 |
| v0.11.0 DNS 故障（**已发布**，2026-10-20） | 只在 `exec` 下（root 模式先拒绝，和 `-host` 一样）；设计见下 |
| v1.0 之前 | 先把常用协议支持完整，兼容性承诺放在最后（维护者决定，2026-10-20）；路线见下「通往 v1.0」 |

**v0.9.0 的实现**（2026-10-20，[已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.9.0)，CI 在 Ubuntu 24.04 和 26.04 上全部通过，含 Go 和 Python 的 TCP 兼容性检查；从发布页下载的 arm64 二进制在开发容器里验证了接受、拒绝和 ping）：`rule`（`TCPScenario`、`TCPTarget`、`refuse`、pattern 的 `r`，ping 的解析路径不变，变异测试确认 ping 的七个入口都拒绝 `refuse`）、`icmp`（`BuildRST`）、`engine`（TCP 规则表、按地址和端口的状态与随机流、重传 SYN 的缓存、`/reset`）、`api`、`config`、`app`、`client`，中英文 README 新增「TCP 连接」一节。开发容器里单元测试、lint、root 和 `exec`（免 root 与 root）的端到端测试都通过，含 IPv6；重传缓存关掉时，端到端测试在第一次连接 1060ms 处连上而失败，说明测试能发现问题。兼容性测试已加入 Go `net.Dial` 和 Python `socket`（把拒绝换成 SYN-ACK 时 Go 的检查失败，说明能发现问题）；README 开头的定位改为「从 ping 起步，以及 TCP 连接」，对比工具一节以「透明」为首

**实现 v0.9.0 时采用的默认选择**（都是用户会写进配置的语法），维护者 2026-10-20 确认按现在的来：

- [x] 每个协议单独的参数、配置段和 API 资源（`-tcp`、`tcp:`、`/tcp/`），不在 `-rule` 里加前缀
- [x] IPv6 目标必须加方括号：`[fd99::1]:443`。因为 `fd99::1:443` 本身是合法的 IPv6 地址，不加括号会被悄悄理解成「这个地址的所有端口」
- [x] 重传的 SYN 不算新请求：同一次连接尝试（源地址、源端口、初始序号相同）在 2 分钟内得到同样的回应，否则 `timeout for 2 requests` 会取决于内核的重传时间
- [x] 多条规则匹配时，先比网段谁更精确，再比有端口的优先于不带端口的
- [x] TCP 规则的场景单位显示为 `requests`（也接受 `pings`、`connections`）

**`after=` 的设计**（planning agent 第四轮调研和原型实验，报告不在仓库里；维护者 2026-10-20 确认按推荐来）：

依据：Toxiproxy [#238](https://github.com/Shopify/toxiproxy/issues/238) 要的是「connection reset」故障（Java 的 `Connection reset`），后来成了 `reset_peer`；[#195](https://github.com/Shopify/toxiproxy/issues/195) 要连接先正常传一段时间数据再断开，这要等「连接建立后的数据」那一步。这些用户都靠本地监听或改连代理解决，pingmock 的优势和 v0.9.0 一样：写死的地址和真实地址也能用、按端口、seed、历史、免 root。开发容器里用 Python 在 TUN 上做的原型（IPv4 和 IPv6；Python socket、`http.client`、curl、Go `net/http`、bash `/dev/tcp`，各 40 次）：

| 做法 | 结果 |
|---|---|
| 收到握手的最后一个 ACK 就发 FIN（close） | 40/40 一致：EOF、`RemoteDisconnected`、curl `(52) Empty reply`、Go `EOF`，先读的客户端也一样 |
| 收到最后一个 ACK 就发 RST | **不可复现**：和 `connect()` 赛跑，Python 38/40 在 connect 报 `ECONNRESET`，curl 39/40 报连接失败 |
| 收到客户端第一个数据或 FIN 才发 RST | 先写的客户端 40/40 一致；先读的客户端（SSH、SMTP、MySQL）不发数据，只会超时 |
| SYN-ACK 延迟 1500ms，SYN 被重传 | 每次连接尝试只动作一次，10/10 |
| 真实服务器 `close()` 之后再收到数据 | 回 RST，客户端第二次写报 `BrokenPipeError`，3/3 |

- [x] 写法：`after=close|reset|hang`，默认 `hang`（v0.9.0 的行为），JSON 里是 `"after"`；不加新的结果词，pattern 不加字母
- [x] 时机：`close` 在握手的最后一个 ACK 上发 FIN|ACK；`reset` 在客户端第一个数据或 FIN 上发 RST
- [x] 动作前的延迟先不做，保留 `after=值:时长` 的写法
- [x] `hang` 保持 v0.9.0 的行为（什么都不确认）。原型发现它和真实的挂起服务器不同（真实服务器确认数据和 FIN），要改得先在 veth 上和真实服务器对比，并用会缩小的窗口
- [x] 历史每次动作多一条事件（`closed`、`reset`），统计新增 `closed`、`reset`；`accepted` 照样计数
- [x] `close` 之后再收到数据回 RST，和真实服务器一样
- [x] 实现（2026-10-20）：`icmp` 解析 SYN 以外的 TCP 报文（RST 除外）、`BuildSegment`；`engine` 在 SYN 时就记下 `after`（中途换阶段不影响），按连接的地址和端口找回，检查对方确认的是我们的 SYN-ACK 或 FIN，每个连接只动作一次；`close` 之后客户端的 FIN 回 ACK、数据回 RST；没有 `after=` 的连接照旧什么都不回；ping 规则拒绝 `after`，和 `refuse`、`timeout`（`loss=100%`）、`unreachable`、没有 `.` 的 pattern 同用也拒绝；不从随机流取数（单元测试比较了有无 `after=close` 时 20 次的延迟和丢包，一致）。统计新增 `closed`、`reset`，历史每次动作一条事件
- [x] 实现时发现并修正：零延迟的回应原先各用一个定时器写出，FIN 和紧随其后的 RST 可能颠倒，root 模式下 curl 5 次里有 2 次得到 reset 而不是空回复。改为零延迟的回应在读循环里按顺序写出；单元测试在旧写法下 20 次全部失败
- [x] 边界：空闲 10 分钟的连接被忘掉，之后不再回应，写进 README
- [x] 验证（开发容器）：单元测试和 lint；root 的 e2e（IPv4 和 IPv6：curl 5 次全是 52 或 56，`close` 之后再写得到 `EPIPE`，不写就关闭的客户端没有留下半关闭的连接，统计和历史）；`exec` 免 root 和 root（域名目标）；兼容性测试中 Go `net.Dial` 和 Python `socket` 先写后读，`close` 读到 EOF、`reset` 读到 `ECONNRESET`。变异：在最后一个 ACK 上就 reset、不检查确认号、新的尝试不覆盖旧连接、动作取自最后一个阶段，单元测试都能发现；不回应客户端的 FIN，e2e 的半关闭检查能发现（起初的检查漏了 CLOSING 状态，没发现，已改为除 TIME-WAIT 外的所有状态）
- [x] [CI](https://github.com/nanmuyao/pingmock/actions/runs/39416311154) 在 Ubuntu 24.04 和 26.04 上全部通过，日志里 after= 的检查都跑了；同一 seed 下 v0.9.0 发布版和新版本的 ping 与 TCP 结果、延迟完全相同；[v0.10.0 已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.10.0)，下载的 arm64 二进制在开发容器里 close、reset、refuse 分别得到 curl 52、56、7
- 还没验证 Java、Node.js 客户端：移到「最低优先级」（维护者决定，2026-10-20）

**DNS 故障的前提验证**（planning agent 第五轮，开发容器里实验：glibc 2.36、Go 1.27.0 纯 Go 与 cgo 解析器、curl、Python；报告不在仓库里，2026-10-20）：

| 前提 | 结果 |
|---|---|
| 各种故障客户端怎样报 | NXDOMAIN 立即「找不到」；SERVFAIL、REFUSED 立即重试后报临时错误；不回应花 `timeout × attempts`（默认 10 秒）；延迟超过超时，glibc 仍接受（重试沿用同一 ID），Go 纯 Go 解析器 10 秒后 `i/o timeout`。`exec` 保留宿主机 resolv.conf 的 `search` 和 `options` |
| 计数 | A 和 AAAA 总是同时发；每次重试都是新查询；search 域会带出 `NAME.<search>` 的查询。**按（域名, 类型）计数时五种客户端结果一致**，按域名计数取决于哪个查询先到，按「一次解析」在服务端看不到 |
| `/etc/hosts` 优先 | 私有 hosts 里的域名，glibc 和 Go 都不发 DNS 查询：有 `-dns` 规则的域名要从 hosts 里拿掉 |
| nscd | **v0.10.0 的 bug**：宿主机运行 nscd 时，`exec` 里 getent、Python、cgo 拿到宿主机的地址，`-host` 不起作用。我复现了（getent 得到 Docker Desktop 的虚构地址，停掉 nscd 后正确）。已修复：在命令的挂载命名空间里用 `/dev/null` 盖住 nscd 的套接字；root 和免 root 都验证过，e2e 检查在去掉修复时失败。CI 和开发容器装上 nscd，检查自己启动它 |
| DNS over TCP | 所有客户端在收到截断的回复后改用 TCP，`options use-vc` 只用 TCP；`exec` 里 127.0.0.1:53 没有 TCP 监听，宿主机给出截断回复的域名在 `exec` 里解析失败（用模拟上游验证，真实上游没测） |
| 并发 | 在 `Serve` 里睡眠会挡住其他查询（2 秒的延迟让无关域名晚了约 1.9 秒）；转发也是串行的，宿主机不回应时客户端每次查询 2 秒后得到 SERVFAIL |

需求：两个小项目（probeboard-api #74、network-doctor #368、#394）在 2026 年 9、10 月自己做了按域名注入错误的办法，反应数 0 到 1，证据弱。

- [x] nscd 修复（见上表）
- [x] `exec` 的 DNS 服务监听 TCP，TCP 查询也经 TCP 转发；每个查询各自处理，中继给查询加标签，可以同时转发多个；宿主机不回应时也不回应（中继等 5 秒，glibc 的默认超时，多个上游平分），不再 2 秒后回 SERVFAIL；上游直接拒绝（没有监听、不可达）时立即回 SERVFAIL，和没有 pingmock 时一样立即失败（advisor 发现的回归：改之前 exec 里一次查询等 20 秒，改之后 32ms，exec 外 0ms）。单元测试：慢查询不挡 UDP 和同一 TCP 连接上的其他查询、中继乱序返回、TCP 转发 3000 字节的回复；e2e：`RES_OPTIONS=use-vc` 下模拟的域名和转发的域名都能解析。变异：串行处理、串行中继、不监听 TCP 都会失败（不监听 TCP 时 glibc 读 TCP 不设超时会一直等，检查加了 `timeout 10`）
- [x] `-dns` 实现（2026-10-20）。语法维护者 2026-10-20 确认按推荐来：
  - `-dns NAME=RULE`、`dns:`、`PUT/DELETE /dns/{name}`，root 模式拒绝（和 `-host` 一样）
  - 故障先做 `nxdomain`、`servfail`，加上 `timeout`/`loss=`、延迟，pattern 的 `n`、`s`
  - 按（域名, 类型）计数，重试也算；README 按结果说明一次解析会发几个查询
  - 只针对某一类型的规则（`NAME/AAAA`）先不做，写法保留
  - search 域带出的查询照常转发，在文档中说明（透明优先）
  - 不做通配；PTR 不受影响
  - 有 `-dns` 规则的域名不写进私有 `/etc/hosts`；`-dns` 本身也要打开 `exec` 的域名设置
  - 实现：`rule`（协议种类代替原来的 TCP 开关，`nxdomain`、`servfail`、pattern 的 `n`、`s`，DNS 规则拒绝 `unreachable`、`ttl`，ping 和 TCP 规则拒绝 DNS 设置）、`engine`（按名字的规则表，按（域名, 类型）的状态和随机流，`dns`/`dns_stats`，`/reset`）、`dns`（规则在查表和转发之前决定，故障不转发，延迟在转发之后）、`app`（`-dns`、`dns:`，root 拒绝，有规则的域名从私有 hosts 里拿掉、连系统 hosts 里的同名条目一起）、`api`（`PUT/DELETE /dns/{name}`，命令 hosts 里的域名和没有 DNS 服务的 exec 返回 400 并给出办法；`GET /history?target=域名`）、`client`（`SetDNS`、`DeleteDNS`、`DNSStats`；顺带补上 v0.10.0 漏掉的 `TCPStats.Closed`、`Reset`）
  - 验证（开发容器，root 和免 root）：nxdomain → `EAI_NONAME`、servfail → `EAI_AGAIN`、timeout（`timeout:1 attempts:1`）→ `EAI_AGAIN`、延迟 60ms 实测 62ms、`nxdomain for 2 requests` 让两次解析失败后第三次解析出地址、没有规则的域名照旧、有规则的域名不在 hosts 里、统计按类型计数、历史、API 设置和删除。变异：有规则的域名留在 hosts 里时延迟检查失败
  - [x] CI：第一次推送在 Ubuntu 上失败，servfail 得到 EAI_NONAME：runner 的 resolv.conf 有 search 行，glibc 在 SERVFAIL 后去查 `NAME.<search>`，宿主机回 NXDOMAIN 并被报告。检查改为查带结尾点的域名，README 说明 search 域的影响。之后 [CI](https://github.com/nanmuyao/pingmock/actions/runs/39431813519) 全部通过，13 项 DNS 检查在 4 个任务里免 root 和 root 各跑一次；同一 seed 下和 v0.10.0 发布版结果一致；[v0.11.0 已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.11.0)，下载的 arm64 二进制在开发容器里 nxdomain、servfail、-host 分别得到 -2、-3、10.99.0.5

README 只描述已经实现的功能：定位已随 TCP 连接更新为「从 ping 起步」的网络回应模拟。

### 通往 v1.0

**维护者决定（2026-10-20）：先把常用协议支持完整，兼容性承诺最后做。** 依据：planning agent 第六轮调研（技术选型和实验，开发容器里内核 6.12.76；报告不在仓库里），关键结论人工核对过：TLS 握手超时用真实的 pingmock 实测（Python `ssl` 报 "The handshake operation timed out"，curl 退出码 28）；Toxiproxy [#195](https://github.com/Shopify/toxiproxy/issues/195) 2026-10-07 关闭未合并，[#50](https://github.com/Shopify/toxiproxy/issues/50)「Protocol aware toxics」2015 年起开着、9 个 👍；README 的 `GET /version` 示例还写着 v0.6.0。

| 协议或行为 | 结论 |
|---|---|
| TLS 握手超时 | **现在就能做**：TCP 规则不写 `after=`（即 `hang`），连接建立后不回 ServerHello。Go 自己的 `TestTransportTLSHandshakeTimeout` 也是手工搭这种情况。只缺 README 的说明 |
| TCP 握手后的数据 | **手写的子集够用，不需要依赖**。Python 原型在 TUN 上：连接后发欢迎语 10/10、HTTP 503 10/10、TLS 告警 10/10（openssl 报 alert number 40）、发满 1000 字节后客户端再写得到 `ConnectionResetError` 10/10、延迟 1.5 秒回复 3/3、逐字节慢发 1/1。需求证据只有间接的：Toxiproxy 已有 `limit_data`、`slicer`、`reset_peer`，没有找到有人要「不用代理」的数据故障 |
| gVisor netstack | **不用**：新版要把 `go` 行从 1.22 拉到 1.26.3、依赖图 127 个模块；保持低版本只能用 17 个月前的版本（wireguard-go 的做法，94 个模块）；最小程序约 4.8MB，普通 `net` 程序 1.7MB |
| 通用 UDP | 不回应现在就是；端口不可达只在设了路径时顺带出现（有路径时连接的 UDP socket 得到 `ConnectionRefusedError`，没有时 `TimeoutError`）。`-udp`（refuse、timeout、unreachable）成本低，需求证据弱 |
| 带内容的 UDP 回复（NTP、SNMP、QUIC） | 不做：等于模拟服务 |
| QUIC 失败回退到 TCP | 不做：pingmock 接管整个地址，没法只让 UDP 失败而 TCP 通向真实服务器 |
| A/AAAA 以外的 DNS 记录 | 等需求：模拟的域名查 MX、SRV、TXT、HTTPS、CNAME 回「没有记录」（验证过），这本身是正确的回答 |
| ICMP Packet Too Big、root 模式 DNS、多网卡 | 等需求：没有新的证据 |

外部 issue 当天复查：pingmock 仍然没有 issue 和 PR；Toxiproxy #447、#610，pro-bing #70、#195，Trippy #1551、#1552、#1554 都没有变化。

路线（工作量是估计）：

- [x] **0. 面向新手的文档**（2026-10-21，维护者要求「补充一些基础的网络知识，最好画点示意图，包结构之类的」）：新增 [docs/basics.md](docs/basics.md)（[中文](docs/basics.zh-CN.md)），讲地址、网段、端口，分层的数据包，ping、超时和不可达、TCP 握手和各种失败、traceroute 的 TTL、DNS 解析，以及 pingmock 怎样插到中间，配 6 张 Mermaid 图（用 mermaid-cli 11.4.2 渲染检查过）和 IPv4、ICMP、TCP、DNS 的包结构图，最后是术语表。README 开头改成先用大白话说它解决什么问题，新增「第一步」：5 条命令（延迟、不可达和超时、运行中改规则、TCP 拒绝和超时、DNS 不存在），输出都是开发容器里实际跑出来的。其他文档开头链接到网络基础
- [ ] **1. 文档**（半天，不打 tag）：README 写 TLS 握手超时的做法（已完成，2026-10-21）、UDP 现在得到什么，「什么时候用别的」里加上 QUIC 回退；「不做」加上带内容的 UDP 回复和 QUIC 回退
- [x] **2. v0.12：TCP 握手后的数据**（2026-10-23 实现，未发布）。维护者确认按推荐的语法：`send="..."` 握手后立即发，`reply="..."` 收到第一段数据后发，`limit=N` 收满 N 字节后按 `after=` 结束（必须配 `after=close` 或 `after=reset`，advisor 指出「收满后不再确认」没有被批准过），`wait=` 推迟数据；字节是 Go 的双引号字符串，最多 16 KiB；`after=hang` 保持沉默（维护者决定）。实现：`rule`（引号里的 `,` `;` ` for ` 不再被当成分隔符）、`icmp`（带数据的报文，每段最多 536 字节）、`engine`（`talk`：确认客户端数据，重传的数据再确认一次，关闭之后的新数据回 RST；握手后第一个报文就带数据时先当握手完成、再当数据）、`responder`（每个连接一条队列：确认立即写出，数据、FIN、RST 按顺序，`wait=` 期间的确认从未发出的数据开始编号）；历史新增 `sent` 事件和 `bytes`
  - 验证（开发容器）：Python 读到 SSH 欢迎语；curl 和 Go `net/http` 拿到 503 和响应体；`limit=1000,after=reset` 让 Python 得到 `ConnectionResetError`；3000 字节分 6 段送达，校验和一致；`wait=1s` 实测 1001ms，期间客户端重传 0 个报文；IPv6 也拿到 503；兼容性测试中 Go 和 Python 都读到 `reply=` 的数据。变异：去掉每个连接的队列，`wait=` 之后的 RST 抢在回复之前（5 次都失败）；`wait=` 期间不立即确认，客户端重传 1 次，e2e 失败。同一 seed 下和 v0.11.0 发布版结果一致
  - [x] [CI](https://github.com/nanmuyao/pingmock/actions/runs/39897333613) 在 Ubuntu 24.04 和 26.04 上全部通过，日志里 send=、reply=、limit=、wait= 和「等待期间不重传」的检查都跑了；[v0.12.0 已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.12.0)，下载的 arm64 二进制在开发容器里读到欢迎语、拿到 503 和响应体
- [x] **3. v0.13：`-udp`**（2026-10-23 实现，未发布）。维护者确认语法：目标和 `-tcp` 一样，`udp:`、`PUT/DELETE /udp/{目标}`、`SetUDP`/`DeleteUDP`/`UDPStats`；`refuse`（ICMP 端口不可达）、`unreachable[=net|admin]`、`timeout`/`loss=`、pattern 的 `x`、`u`、`r`、错误的 `delay` 和 `ttl`；每个数据报计一次；只写延迟、没有失败方式的规则报错；有路径时各跳先回应，终点由 UDP 规则决定；exec 的 DNS 服务不受影响
  - 验证（开发容器）：Python 已连接的 UDP socket：`refuse` 得到 `ECONNREFUSED`，`unreachable=admin` 得到 `EHOSTUNREACH`（IPv6 是 `EACCES`），`timeout for 1 request; refuse` 先超时再拒绝，没有规则的端口超时，IPv6 的 refuse，exec 里按域名的规则；统计按数据报计数。**发现**：普通的 `unreachable`（主机不可达）Linux 不报告给没有 `IP_RECVERR` 的 UDP socket，程序只看到超时，README 写明了
  - [x] [CI](https://github.com/nanmuyao/pingmock/actions/runs/39913858988) 在 Ubuntu 24.04 和 26.04 上全部通过，9 项 UDP 检查在 4 个任务里都跑了；[v0.13.0 已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.13.0)，下载的 arm64 二进制在开发容器里 refuse 得到 `ECONNREFUSED`，`unreachable=admin` 得到 `EHOSTUNREACH`
- [x] **4. v0.14：冻结前的整理**（2026-10-23，已发布）。维护者决定：DNS 事件的 `src`、`dst` 留空，`id` 放查询的 DNS ID；`pingmock exec` 自己失败时退出码改为 125（和 docker run、env、timeout 一样），126、127 不变；其余不一致保持现状，写进兼容性承诺：各协议的结果用词（`reply`、`syn-ack`、`answer`）和统计字段（`replied`、`accepted`、`answered`）、规则标签（ping 是网段或 `default`，其他带协议前缀）、统计键的格式、阶段单位宽松接受、`reply` 布尔字段、路径一跳的回应 `phase` 为 0、配置文件的未知键报错
  - 修正：`GET /rules` 里路径的 `>` 被转义成 `\u003e`（`Path.MarshalJSON` 用了会转义 HTML 的 `json.Marshal`），测试在去掉修正时失败
  - Go 自带的 fuzz：`FuzzParseScenario`（ping、TCP、UDP、DNS 四种规则，解析成功的必须原样往返）、`FuzzParsePathAndTargets`、`FuzzParseProbe`（报文解析和据此构造的所有回应）、`FuzzAnswer`（DNS 服务），各跑 2 分钟，没有发现问题
  - musl（Alpine 3.20，下载的 v0.13.0 二进制）：按（域名, 类型）计数成立，`nxdomain for 2 requests` 让两次解析失败、第三次解析出地址，和 glibc 一样；**但 musl 收到 SERVFAIL 会立即重问**，一次解析每种类型问了 6 次，所以 `servfail for 2 requests` 在 musl 下一次都不失败。README 写明了
  - [x] [CI](https://github.com/nanmuyao/pingmock/actions/runs/39917348427) 在 Ubuntu 24.04 和 26.04 上全部通过，退出码 125 和 DNS 事件字段的检查在 4 个任务里都跑了；[v0.14.0 已发布](https://github.com/nanmuyao/pingmock/releases/tag/v0.14.0)，下载的 arm64 二进制（SHA256SUMS 校验通过）在开发容器里：规则写错时退出码是 125，命令自己的退出码 3 原样返回，DNS 事件的 `src`、`dst` 为空，`id` 是 DNS ID
- [ ] **5. v1.0：兼容性承诺**（1 天），中英文

**需要维护者决定**（括号里是 agent 的推荐，还没有确认）：

- [ ] pingmock 可以发送数据吗？README 现在写「pingmock 不模拟任何服务」（推荐：可以，只发固定的字节，不匹配客户端发来的内容）
- [ ] 数据的语法（推荐：`send=` 握手后立即发，`reply=` 收到客户端第一段数据后发，`limit=N` 发满 N 字节后断开，`wait=` 回复前等待；规则文本里字节用 Go 的带引号字符串，因为逗号和分号是分隔符，YAML 和 JSON 里是普通字符串；上限 16 KiB；之后怎样仍由 `after=` 决定；历史新增 `sent` 事件，带 `bytes`；逐字节慢发等需求）
- [ ] `after=hang` 要不要像真实服务器那样确认数据、窗口逐渐变小？这会改变默认行为，v1.0 之后就是不兼容的改动，要在 v0.12 决定（推荐：保持现在的沉默，除非先和真实的挂起服务器对比过）
- [ ] 没有用户证据也做 `-udp` 吗？（推荐：做，排在数据之后，成本低，让 UDP 也能区分拒绝和超时）
- [ ] v1.0 冻结哪些（推荐：参数及默认值、规则文本、YAML 键和 JSON、API 路径、方法和状态码、事件字段和结果用词、`client` 包（只增不改）、seed 序列、退出码、`PINGMOCK_API`、默认地址；不冻结 `-v` 的日志、错误信息、键的顺序、`internal/`；弃用的写法至少保留一个小版本并给出警告，v2 才去掉；配置文件的未知键仍然报错，所以要写明旧版本会拒绝新版本的配置文件）

**冻结前要修正的不一致**（agent 列出，未逐条核对）：DNS 事件的 `src`/`dst` 是 127.0.0.1；结果用词 DNS 是 `answer`、ping 是 `reply`；规则标签、目标和统计键的格式各协议不同；路径事件的 `phase` 是 0；每种协议都接受所有的阶段单位；`GET /rules` 把 `>` 转义；示例还写着 v0.6.0；`pingmock exec` 自己的退出码 1、2 和被测命令的退出码分不开（验证过）。

没有做：GitHub Discussions 没查；需求搜索没有找到切题的 issue，几条搜索摘要（Micrometer #3563、Tor 的工单、QUIC 回退的指南）没有打开；IPv6 的数据、超过一个报文段的回复、Java/Node/Go 客户端对数据原型、gVisor 在 pingmock 里运行都没有测。Java、Node 客户端，Python 客户端包，macOS，示例，仍按原来的优先级。

### 之后：等需求再决定

| 方向 | 说明 |
|---|---|
| `exec` 的外网访问 | 检测到系统装有 pasta 或 slirp4netns 时，借助它们为 `exec` 提供外网，解决不能下载依赖、不能访问真实服务的问题 |
| 更多异常行为 | 重复回复（`DUP!`）、损坏的回复：没有找到明确的需求 |
| Testcontainers 模块 | 只适用于被测代码运行在容器里的情况：pingmock 的容器影响不了宿主机上的测试进程 |
| 用 netlink 替代 `ip` 命令 | 去掉运行时对 iproute2 的依赖；fakert 用的 vishvananda/netlink 可以参考 |
| 分片重组、其他系统（macOS 等） | 使用场景少，或者工作量大（`exec` 依赖 Linux 命名空间） |

### 不做

| 方向 | 原因 |
|---|---|
| 通用的流量整形 | 需求最大，但 netem、Toxiproxy 等已经很成熟，做了会偏离定位：pingmock 伪造回应，不改变真实的网络 |
| 向 Toxiproxy 贡献 ping 支持 | 维护者认为三层代理需要 TUN、等于重写，而且他们看重跨平台（#39） |
| 带内容的 UDP 回复（NTP、SNMP、QUIC） | 等于模拟服务，偏离定位（第六轮调研） |
| QUIC 失败回退到 TCP | pingmock 接管整个地址，没法只让 UDP 失败、TCP 通向真实服务器（第六轮调研） |

### 和外部项目的互动

由作者本人进行；先在 issue 中问维护者是否欢迎，不直接提交依赖 pingmock 的代码。

| 时机 | 机会 |
|---|---|
| v0.8.0 发布之后 | [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70)：贡献修复，附带用 pingmock 测试超时行为的用例（[pinglib-demo](examples/pinglib-demo/) 已经演示了这类测试） |
| v0.8.0 发布之后 | [Trippy #1552](https://github.com/fujiapple852/trippy/issues/1552)：作者想要一个独立的模拟工具；路径模拟已经实现，可以介绍 pingmock 作为 Linux 上的选择 |

## 维护

- [ ] 删除用于推送的 Fine-grained token（**2026-11-16 过期**）：它出现在过去的对话记录里，切换到新的工作方式后就应该删除；以后需要推送时，每个工作阶段新建一个有效期很短的 token
- [ ] 每次发布后更新示例中的版本号（见第 2 项）

## 最低优先级

以下几项都不急，其他事情做完、或者有空闲时再做。

### 功能

- [ ] 新示例：用 pingmock 测试 **Prometheus 的 ICMP 告警规则**（blackbox exporter 是最常用的 ping 监控），Docker Compose 即可运行。服务用户 ③，运维人员不写代码（原属 v0.6；Compose 文件和 Prometheus、blackbox 的配置已起草，暂存在本机、未入库）
- [ ] Python 客户端包：把 [python-demo](examples/python-demo/conftest.py) 的客户端整理成正式的包，只用标准库，附带 pytest fixture，发布到 PyPI（原属 v0.6）
- [ ] Trippy 的 28 个场景文件作为验收清单：Trippy 是 Rust 程序，需要单独安装（原属 v0.8）
- [ ] 自动生成中间节点的简化写法（比如只给跳数和总延迟）：有先例（traceroute-haiku），但还没有找到需求，等用户反馈（原属 v0.8）
- [ ] 示例里用上 TCP 和 DNS 规则（文档审查发现，2026-10-21）：README 开头就有 `-tcp`、`-dns`，但没有一个示例用到。候选：go-demo 加一个连接超时后切换的测试，python-demo 加一个连接被拒绝或 NXDOMAIN 的测试；python-demo 的 fixture 也补上 `set_tcp`、`set_dns`
- [ ] 用 Java、Node.js 客户端验证 TCP 规则（含 `after=`）：现在验证过的是 curl、bash `/dev/tcp`、Go `net`、Python `socket`

### 推广帖子，由作者本人发布

**暂缓**：先专注项目本身，见上面的[技术路线](#技术路线)。另外，查过三个社区的规定：Hacker News、Reddit r/golang 和 V2EX 都禁止 AI 生成的帖子或回复，所以帖子和回复都要由作者本人来写；AI 可以准备素材、核对事实、帮作者弄懂读者的技术问题。

每个社区先读一遍发帖规则；发帖后作者本人在评论区回答问题。

- [ ] **V2EX**：「分享创造」节点，用中文介绍
- [ ] **Reddit r/golang**：英文，强调 Go 实现、依赖极少、`go test -exec` 的用法
- [ ] **Hacker News**：标题用 `Show HN: Pingmock – mock ping latency, loss and unreachable errors on Linux` 这样的格式
- [ ] （可选）掘金、Go 中文社区、Twitter / X

发帖之后：把收到的需求记在上面的[之后：等需求再决定](#之后等需求再决定)里，按**有多少人需要**排优先级。

### 学习进度

和任务没有先后关系，可以穿插进行，或者在等待反馈期间进行。

| 课 | 主题 | 状态 |
|---|---|---|
| 1 | 整体原理：TUN 网卡、路由，ping 请求怎样到达 pingmock | ✅ 已完成，整理为[一个 ping 的往返之旅](docs/packet-path.zh-CN.md) |
| 2 | 数据包：每个字节的含义、pingmock 的检查、校验和、分片 | ✅ 已完成，整理为[数据包详解](docs/packets.zh-CN.md) |
| 3 | 规则与决策：`internal/rule` 和 `internal/engine`，怎样决定回复、丢弃还是不可达 | 下一课。从课后思考题开始：规则存在哪里、怎样找到；`-seed` 怎样让随机结果可以复现 |
| 4 | 主循环：`internal/responder`，延迟是怎么做到的，为什么一个慢回复不会挡住其他请求 | |
| 5 | 启动与配置：`internal/app`、`internal/config`、`internal/api` 怎样组装起来 | |
| 6 | 不用 root：Linux 的用户、网络、PID 命名空间，`exec_linux.go` | |
| 7 | 工程化：测试、CI、发布和 `examples/` 中的示例 | |

每一课都动手做实验，结论都经过验证；有价值的内容整理进 `docs/`。
