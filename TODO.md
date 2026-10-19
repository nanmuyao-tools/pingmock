# TODO

已发布 v0.5.2（见 [Releases](https://github.com/nanmuyao/pingmock/releases)）：v0.2–v0.4 的规划全部完成，v0.5 改用了 YAML 配置文件；v0.5.1 没有改变任何行为，加入了代码结构重构、3 份技术文档（[往返之旅](docs/packet-path.zh-CN.md)、[数据包详解](docs/packets.zh-CN.md)、[代码导读](docs/architecture.zh-CN.md)）、5 个[完整示例](examples/README.zh-CN.md)，以及在 Ubuntu 26.04 上的测试；v0.5.2 修复了 root 模式会替换本机所在网络路由的 bug。

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

**暂缓**：先专注项目本身，见下面的[技术路线](#技术路线)。另外，查过三个社区的规定：Hacker News、Reddit r/golang 和 V2EX 都禁止 AI 生成的帖子或回复，所以帖子和回复都要由作者本人来写；AI 可以准备素材、核对事实、帮作者弄懂读者的技术问题。

每个社区先读一遍发帖规则；发帖后作者本人在评论区回答问题。

- [ ] **V2EX**：「分享创造」节点，用中文介绍
- [ ] **Reddit r/golang**：英文，强调 Go 实现、依赖极少、`go test -exec` 的用法
- [ ] **Hacker News**：标题用 `Show HN: Pingmock – mock ping latency, loss and unreachable errors on Linux` 这样的格式
- [ ] （可选）掘金、Go 中文社区、Twitter / X

发帖之后：把收到的需求记在下面的"候选方向"里，按**有多少人需要**排优先级。

## 学习进度

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

## 已修复：root 模式会替换本机所在网络的路由

- [x] 用 root 模式对**本机网卡所在的网络**设置规则时（比如本机是 `192.168.20.5/24`，规则写 `192.168.20.0/24`），v0.5.1 及之前的版本用 `ip route replace` 替换掉了这个网络原有的路由，退出后也不会恢复，本机就连不上那个网络了。`pingmock exec` 不受影响
- [x] 修复：添加路由前先查询是否已有完全相同网段的路由；有的话拆成两个更精确的半网段（再冲突就继续拆），原有的路由保持不动，pingmock 被强制终止也不会留下问题；单个地址已有自己的路由时拒绝启动，并建议使用 `pingmock exec`。单元测试覆盖拆分逻辑，端到端测试覆盖 IPv4 和 IPv6，并确认 v0.5.1 会在这项测试上失败
- [x] 发布 v0.5.2：已发布，用发布页下载的程序重复 VLAN 实验，运行中和退出后原有的路由都保留；示例中的版本号已更新为 v0.5.2

## 技术路线

这条路线是怎样得出的、依据了哪些证据，记录在[技术路线是怎样得出的](docs/planning.zh-CN.md)中。下一次规划时，按那份文档的方法重做一遍。

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
| ③ 运维人员 | 监控告警规则（宕机多久告警、丢几个包算故障）、线路切换脚本。市场调研中发现：Zabbix 用户只能临时改小阈值来测试告警；Uptime Kuma 曾因 bug 每秒都在 ping。他们不写代码，需要 Docker 和命令行就能用 |
| ④ 网络诊断工具的开发者 | traceroute / mtr 类工具对路径的处理（Trippy #1552、#1554） |

每一版都能独立发布、独立带来价值。每一版都按这个流程：

1. 用户可见的写法先出设计方案，和维护者确认
2. 动手之前，用实验验证技术前提
3. 实现：单元测试、端到端测试、兼容性测试，并确认测试能发现问题
4. 中英文文档、示例同步更新
5. 发布，用发布页下载的程序再验证一遍

### 已完成，尚未发布（将作为 v0.6.0 发布）

v0.5.2 之后，原计划中 v0.6（测试体验）、v0.7（域名）、v0.8（路径）的功能都已实现。

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

- [ ] Trippy 的 28 个场景文件作为验收清单：Trippy 是 Rust 程序，需要单独安装
- [ ] 自动生成中间节点的简化写法（比如只给跳数和总延迟）：有先例（traceroute-haiku），但还没有找到需求，等用户反馈

## 下一步

### 1. 发布 v0.6.0

- [ ] 发布前对比 v0.5.2 和当前代码中用户可见的行为（帮助信息、API、配置文件、种子序列），在更新说明中如实写出新增和变化；确认没有不兼容的改动
- [ ] 新功能较多（`/reset`、`/version`、TTL、`-host`、按域名的规则、DNS 服务、路径），每一项在 README 中都有中英文说明，示例都能运行
- [ ] 在 Ubuntu 24.04 和 26.04 的 CI 上通过（包括本地无法运行的 IPv6 路径检查）；用发布页下载的程序再验证一遍
- [ ] 发布后更新示例中的版本号

### 2. v0.6 剩下的三项

- [ ] Python 客户端包：把 [python-demo](examples/python-demo/conftest.py) 的客户端整理成正式的包，只用标准库，附带 pytest fixture，发布到 PyPI
- [ ] 新示例：用 pingmock 测试 **Prometheus 的 ICMP 告警规则**（blackbox exporter 是最常用的 ping 监控），Docker Compose 即可运行。服务用户 ③，运维人员不写代码
- [ ] 把市场调研的结果补充到 [docs/planning](docs/planning.zh-CN.md) 的证据部分：七条技术路线和代表项目、运维人员这类用户、绑定网卡的限制、WireMock 是更贴切的类比、gVisor 的用户态 TCP 协议栈、Testcontainers 渠道受技术路线限制

### 3. 开发环境：在 Mac 上用 Docker 开发

维护者用 Mac，pingmock 只能在 Linux 上运行。Mac 上的 Docker Desktop 在 Linux 虚拟机中运行容器，所以可以在容器里开发。Claude Code 官方文档中有开发容器的说明（命令在容器里执行，文件修改实时出现在本地仓库），参考配置以非 root 用户运行，并加了 `NET_ADMIN` 权限，这也正是 pingmock 需要的。

- [ ] 增加 `.devcontainer/`：Go、iproute2、ping、fping、Python、shellcheck 和 Claude Code；以非 root 用户运行，容器内可以用 `sudo` 跑端到端测试；`NET_ADMIN` 和 `/dev/net/tun`
- [ ] 先验证：免 root 的测试（`make e2e-exec`）需要创建用户命名空间，Docker 默认的 seccomp 策略可能阻止，需要找出最小的权限（还是只能 `--privileged`）；IPv6 默认关闭，相关检查会跳过
- [ ] CI 增加一个任务：构建这个开发容器，在里面运行 `make test`、`make e2e`、`make e2e-exec`，保证它一直可用。Docker 相关的测试（`make e2e-docker`）继续只在 CI 中运行
- [ ] 中英文的使用说明：在 Mac 上怎样打开、哪些测试在本地跑、哪些交给 CI；维护者在 Mac 上实际验证一次 Docker Desktop 的差异
- [ ] 增加 `CLAUDE.md`：写入项目的工作约定（四个核心价值、证据优先、中英文文档同步、依赖要发布超过两周、验证后再下结论、不提交来源不明的文件），本地的 Claude Code 会读取它

另一个选择是 OrbStack、Lima 等工具提供的 Linux 虚拟机：和 CI 更接近，免 root 测试、IPv6、Docker 都不需要额外处理，但更重。

### 4. 多协议

**名字保持 pingmock，后续支持更多的协议，这是已经确定的方向。** 每个协议都必须保持四个核心价值（见本节开头）；设计做不到其中某一点，就要重新考虑。

现在的基础：`exec` 中已经有一个 DNS 服务（只回答模拟的域名、转发其他的）；路径模拟已经能解析 UDP 和 TCP SYN、回复 SYN-ACK。

| 步骤 | 内容 |
|---|---|
| 1. 设计（**需要维护者确认**） | 多协议的规则写法：协议、地址或域名、端口怎样表达；现有的 ping 规则和路径写法**保持不变**；YAML、API、客户端、历史和统计的对应写法。逐个检查是否做到四个核心价值 |
| 2. DNS 故障 | DNS 成为第一个被完整模拟的协议：按域名设置延迟、NXDOMAIN、SERVFAIL、超时，同样支持场景和可复现的随机。需要先决定 root 模式下怎样做到透明（`exec` 中已有自己的 DNS 服务） |
| 3. TCP 连接 | 先调研（tcping、健康检查、`nc -z` 等怎样用 TCP 测延迟、检查端口），再实验（只回 SYN-ACK、RST 或不回应时各种客户端的表现，以及连接建立后客户端发数据时怎样处理），最后实现：按地址和端口延迟后接受、拒绝、超时。复用路径模拟中的 SYN-ACK |
| 4. v1.0 之前的两个决定 | 是否支持**连接建立后的数据**（比如 HTTP 的回应）：需要用户态 TCP 协议栈，gVisor 有现成的 Go 实现，但依赖很重，先做技术验证，再决定放进 pingmock 还是作为单独的可选组件；v1.0 的**兼容性承诺**：规则写法、API、配置文件、客户端在 v1.0 之后保持兼容，所以多协议的写法要在 v1.0 之前稳定下来 |

README 只描述已经实现的功能：等第一个新协议（DNS 故障）发布时，再把定位更新为"从 ping 起步、支持多种协议的网络回应模拟工具"。

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

### 和外部项目的互动

由作者本人进行；先在 issue 中问维护者是否欢迎，不直接提交依赖 pingmock 的代码。

| 时机 | 机会 |
|---|---|
| v0.6.0 发布之后 | [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70)：贡献修复，附带用 pingmock 测试超时行为的用例（[pinglib-demo](examples/pinglib-demo/) 已经演示了这类测试） |
| v0.6.0 发布之后 | [Trippy #1552](https://github.com/fujiapple852/trippy/issues/1552)：作者想要一个独立的模拟工具；路径模拟已经实现，可以介绍 pingmock 作为 Linux 上的选择 |

## 维护

- [ ] 删除用于推送的 Fine-grained token（**2026-11-16 过期**）：它出现在过去的对话记录里，切换到新的工作方式后就应该删除；以后需要推送时，每个工作阶段新建一个有效期很短的 token
- [ ] 每次发布后更新示例中的版本号（见第 2 项）
