# TODO

已发布 v0.5.2（见 [Releases](https://github.com/nanmuyao/pingmock/releases)）：v0.2–v0.4 的规划全部完成，v0.5 改用了 YAML 配置文件；v0.5.1 没有改变任何行为，加入了代码结构重构、3 份技术文档（[往返之旅](docs/packet-path.zh-CN.md)、[数据包详解](docs/packets.zh-CN.md)、[代码导读](docs/architecture.zh-CN.md)）、5 个[完整示例](examples/README.zh-CN.md)，以及在 Ubuntu 26.04 上的测试；v0.5.2 修复了 root 模式会替换本机所在网络路由的 bug。

接下来的重点不是继续加功能，而是**让真实用户用起来**，再根据反馈决定方向。

## v0.5 的收尾（已完成，推广暂缓）

接下来的工作见[技术方向](#技术方向v06)。

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

**暂缓**：先专注项目本身，见下面的[技术方向](#技术方向v06)。另外，查过三个社区的规定：Hacker News、Reddit r/golang 和 V2EX 都禁止 AI 生成的帖子或回复，所以帖子和回复都要由作者本人来写；AI 可以准备素材、核对事实、帮作者弄懂读者的技术问题。

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

## 技术方向（v0.6）

推广暂缓，先专注项目本身。方向来自两次调研。

### 调研结论

**兼容性测试**：在 pingmock 下运行常见的 ping 工具和程序库：

| 工具 / 库 | 结果 |
|---|---|
| 系统 `ping`（iputils）、fping、Go pro-bing、Python icmplib（原始套接字和免 root 的 ping 套接字） | 正常：延迟、确定性丢包、不可达、劫持真实地址都正确 |
| traceroute（默认 UDP 模式） | 每一跳都是 `*`，到不了终点：pingmock 不回应 UDP 探测 |
| traceroute -I、mtr | 只显示一跳：pingmock 不理会 TTL，无法模拟一条有中间节点的路径 |

**网上的需求**（2026-10 搜集，来自公开讨论，样本有限，没有量化数据）：

| 需求 | 证据 | 和 pingmock 的关系 |
|---|---|---|
| 只给某个目标加延迟 | 需求最大，几乎都用 `tc netem`，而且很容易出错：照着示例只想延迟一个地址，结果所有出站流量都被延迟；不加过滤会拖慢自己的 SSH；只能作用于出站方向 | 这些人想延迟的通常是**所有流量**，属于流量整形，是 netem、Toxiproxy 的领域。pingmock 不做，但可以在文档里说清楚定位 |
| 测试调用 ping 的代码 | Hawkular 的 Pinger 测试 ping 真实网站，网站宕机测试就失败；有 Python 用户用 pytest-mock 替换 `icmplib.async_ping`，因为替换位置不对而不生效 | **pingmock 的核心**，已经解决得很好 |
| 测试网络诊断工具 | [Trippy #759](https://github.com/fujiapple852/trippy/issues/759)（traceroute/mtr 类工具，8000 star）：最重要的集成测试方案是用 TUN 设备模拟网络，回应 ICMP、UDP 和 TCP 探测。现有的路径伪造工具（[fakert](https://github.com/sehaas/fakert)、traceroute-haiku、rdnstun）都是娱乐性质，没有延迟和丢包控制 | 需要**路径模拟**；面向测试的路径模拟是空白 |
| 故障注入工具 | [Toxiproxy](https://github.com/Shopify/toxiproxy)（12000 star）：后台进程 + HTTP API + 10 种语言的客户端，只支持 TCP | 和 pingmock 互补，设计上值得借鉴 |

**Toxiproxy**：维护者在 [#39](https://github.com/Shopify/toxiproxy/issues/39) 中说，三层代理需要折腾 TUN/TAP，等于大规模重写；他们看重跨平台；原则是"测试应用，而不是测试 TCP"。所以向它贡献 ping 支持不可行，pingmock 保持独立、和它互补。值得借鉴的是：`POST /reset`、`GET /version`、多语言客户端、README 里说清楚"为什么需要这个工具"，以及"测试应用，而不是测试协议"这个原则。

### 已完成：兼容性测试进 CI

- [x] 把 fping、pro-bing、icmplib 的测试固定下来（`test/e2e-compat.sh`），在 Ubuntu 24.04 和 26.04 上运行，保护后面对 `icmp`、`engine` 的修改：20 项检查（3 个工具、两种套接字、4 种情况），变异测试确认能发现问题；README 新增了经过测试的工具和库，并在限制中写明 traceroute 和 mtr 目前不能使用
- [x] 测试用的程序库固定版本，放在独立的目录里，不给主模块增加依赖：`test/compat` 是独立的模块，pro-bing v0.9.1、icmplib 3.0.4

### 1. 测试体验：`POST /reset` 和 `GET /version`

借鉴 Toxiproxy。工作量小，直接改善每个用户写测试的体验。

- [ ] `POST /reset`：规则、默认规则、统计和历史都恢复到启动时（命令行和配置文件）的样子，让每个测试从干净的状态开始。现在 [python-demo](examples/python-demo/conftest.py) 的 fixture 要先保存、再逐条恢复规则，正是因为缺少这个接口
- [ ] `GET /version`：返回 pingmock 的版本，方便测试确认用的是哪个版本
- [ ] Go 客户端增加 `Reset` 和 `Version`；python-demo 的 fixture 改用 `/reset`，变得更简单

### 2. 域名支持：`pingmock exec` 中按域名模拟

很多软件 ping 的是域名。实验（`ping www.baidu.com`）发现：`pingmock exec` 中域名解析会失败（访问不到 DNS 服务器）；改 `/etc/hosts` 能解决，但它是宿主机上的同一个文件，违背了 `exec` 不影响宿主机的初衷。

- [ ] 设想：`pingmock exec -host www.baidu.com=10.99.0.1 -- CMD`，在独立的挂载命名空间里只为被测程序替换 `/etc/hosts`；进一步可以直接写 `-rule www.baidu.com=80ms`，自动分配模拟地址
- [ ] 先验证：普通用户能否在用户命名空间里替换 `/etc/hosts`；glibc、Go、Python 等解析域名时是否都会读它；IPv6（AAAA）怎么处理
- [ ] root 模式下继续推荐 `/etc/hosts` 的做法，在 README 中写清楚

### 3. Python 客户端包

借鉴 Toxiproxy 的多语言客户端。

- [ ] 把 [python-demo](examples/python-demo/conftest.py) 中的客户端整理成正式的包，发布到 PyPI，附带 pytest fixture
- [ ] 只用标准库，和 Go 客户端提供相同的操作

### 4. 定位说明

- [ ] README 中说明 pingmock、`tc netem`、Toxiproxy 各适合什么场景：给真实流量加延迟用 netem，TCP 层的故障用 Toxiproxy，测试依赖 ping 的程序用 pingmock

### 5. 路径模拟：traceroute 和 mtr

面向网络工具的开发者，人群比前几项窄，但有真实需求（Trippy #759）。

**状态：设计方案待确认，还没有决定是否这样做、什么时候做。** 下面是方案和已经验证过的技术前提。

#### 原理

traceroute 和 mtr 发出一系列探测包，TTL 依次为 1、2、3……。路由器把 TTL 减到 0 时丢掉这个包，回一条 ICMP Time Exceeded，源地址是路由器自己。到达终点时，ICMP 模式（`traceroute -I`、mtr）收到回显应答；UDP 模式（traceroute 默认）的探测发往没人监听的端口，终点回 ICMP Port Unreachable。pingmock 要扮演路径上的每个路由器。

#### 已验证的前提

用一个 Python 原型（TUN 网卡上假装有两个中间节点）验证过：

| 前提 | 结果 |
|---|---|
| pingmock 读到的 TTL 是程序设置的原始值 | 成立：traceroute 发出的 TTL 1～5，原型都按原值看到 |
| traceroute -I、traceroute（UDP）、mtr 能看到伪造的中间节点和终点 | 成立：三种都正确显示了两个中间节点和终点 |
| 严格的反向路径检查（`rp_filter=1`）下依然可用 | **只有为中间节点的地址添加指向 TUN 网卡的路由时才成立**。不加路由时，中间节点全部显示为 `*`，内核计数 `IPReversePathFilter` 显示丢弃了这些回复 |

IPv6（ICMPv6 Time Exceeded、Port Unreachable、hop limit）还没有验证。

#### 设计方案

为目标设置了路径后：

| 探测包的 TTL | pingmock 的回应 |
|---|---|
| 不超过路径的长度 | 对应的中间节点按自己的延迟回复 Time Exceeded；`*` 节点不回复 |
| 超过路径的长度，ICMP 回显请求 | 按目标原有的规则回复，和现在一样 |
| 超过路径的长度，UDP 探测 | 回复 Port Unreachable，延迟、丢包按目标原有的规则 |

没有设置路径的目标，行为完全不变：不理会 TTL，忽略 UDP。

写法：每一跳是 `地址@规则`，用 `>` 连接，`*` 表示不回应的节点：

```bash
pingmock -rule 8.8.8.8=30ms \
  -path '8.8.8.8=192.168.1.1@2ms > 10.0.0.1@delay=15ms,jitter=3ms > * > 72.14.0.1@25ms'
```

```yaml
paths:
  8.8.8.8: 192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms
```

运行中修改：`PUT /paths/{target}`、`DELETE /paths/{target}`，Go 客户端增加 `SetPath`、`DeletePath`。预期 `traceroute -n 8.8.8.8` 显示 192.168.1.1（2ms）、10.0.0.1（15ms 左右）、`*`、72.14.0.1（25ms），最后是 8.8.8.8（30ms）。

#### 待决定的问题

- [ ] **写法**：`地址@规则`，`>` 连接，`*` 不回应。每一跳的规则支持延迟、抖动和丢包，是否还需要其他设置？YAML 中以 `*` 开头的值需要加引号（`*` 在 YAML 里表示引用）
- [ ] **中间节点的地址自动加路由**：这是严格模式下可用的必要条件。副作用是发往这些地址的流量也交给 pingmock，和劫持真实地址的效果一样；在 `pingmock exec` 里不影响宿主机
- [ ] **只有设置了路径的目标才回应 UDP**：如果对所有目标都回 Port Unreachable，现有用户的程序会从"UDP 请求超时"变成"立即被拒绝"，所以做成需要主动开启
- [ ] 是否也需要一种更简单的写法，比如只给出跳数和总延迟，自动生成中间节点？
- [ ] 不在范围内：TCP 模式的 traceroute（`traceroute -T`）

#### 实现时要做的

- [ ] 规则层：解析路径的写法，配置文件和 API 中的路径
- [ ] 决策层：按 TTL 选择中间节点或终点，中间节点的随机数和丢包同样可以用 `-seed` 复现
- [ ] 数据包：构造 ICMP Time Exceeded、Port Unreachable（IPv4 和 IPv6），引用原包的方式和现有的不可达消息相同
- [ ] 路由：为中间节点的地址添加路由；先验证 IPv6
- [ ] 历史和统计：记录中间节点的回复（结果为 time-exceeded、port-unreachable，以及第几跳）
- [ ] traceroute、mtr 加入兼容性测试；中英文文档

### 暂缓和不做

| 方向 | 说明 | 决定 |
|---|---|---|
| 更多异常行为 | 重复回复（`DUP!`）、损坏的回复、自定义 TTL | 暂缓：调研中没有找到明确的需求；按"测试应用，而不是测试协议"的原则，放在后面 |
| TCP 探测、TCP 连接延迟模拟 | Trippy 提到了 TCP 探测；但 TCP 层面已有 Toxiproxy | 暂缓，放在路径模拟之后再看 |
| 通用的流量整形 | 给所有流量加延迟，需求最大 | **不做**：netem、Toxiproxy 等已经很成熟，做了会偏离定位 |
| 向 Toxiproxy 贡献 ping 支持 | | **不做**：维护者认为需要 TUN、等于重写，而且他们看重跨平台 |
| 用 netlink 替代 `ip` 命令 | 去掉运行时对 iproute2 的依赖 | 暂缓：代码量增加不少，目前没有用户因此遇到问题 |
| 分片重组 | 支持 `ping -s 2000` 这类超过 MTU 的请求 | 暂缓：使用场景很少 |
| 其他系统（macOS 等） | | 暂缓：`exec` 依赖 Linux 命名空间，工作量大 |

## 维护

- [ ] 删除用于推送的 Fine-grained token（**2026-11-16 过期**）：它出现在过去的对话记录里，切换到新的工作方式后就应该删除；以后需要推送时，每个工作阶段新建一个有效期很短的 token
- [ ] 每次发布后更新示例中的版本号（见第 2 项）
