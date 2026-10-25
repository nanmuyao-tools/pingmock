# 技术路线是怎样得出的

[English](planning.md) | 中文

这份文档记录 pingmock 规划技术路线的方法，以及 2026 年 10 月各轮规划用到的证据和来源。目的是让下一次规划可以照着同样的步骤重做，并且每个方向都能追溯到它的依据。规划的结果概括在[技术路线图](roadmap.zh-CN.md)里，逐步的进度见 [TODO.md](../TODO.md#技术路线)。

## 原则

- **四个核心价值，每个协议都要保持。** pingmock 做的每一件事都要同时做到：**透明**（被测程序不需要修改代码或配置）、**按目标**（每个目标可以有自己的网络状况）、**可复现**（同样的设置每次结果都一样）、**可控、可观察**（测试中可以改变网络状况，事后能看到程序实际做了什么）。pingmock 将来支持的每一个协议都必须同时做到这四点；**新协议的设计如果做不到其中某一点，就要重新考虑。** 定义和例子见 [CONTRIBUTING](../CONTRIBUTING.md#design-principles)。
- **测试应用，而不是测试协议。** 这是 Toxiproxy 维护者的说法。pingmock 帮人测试依赖 ping、连接和域名解析的程序，而不是研究这些协议本身。一个功能如果不能帮用户测试自己的程序，就往后放。
- **证据优先。** 每个方向都要有可以追溯的证据：一个 issue、一次实验、一份兼容性测试的结果。没有找到证据的方向，即使听起来有用，也往后放。
- **先验证前提，再设计；先确认写法，再实现。** 技术前提用实验确认；用户会写进配置文件的写法，先出设计方案和维护者确认，因为发布后很难再改。
- **按用户从多到少排序。** 先服务人数最多的用户，再逐步扩展到更窄的人群。

## 方法

一共七步。前五步收集证据，第六步验证，第七步综合。

### 1. 兼容性测试：用真实的工具找缺口

在 pingmock 下运行常见的 ping 工具和程序库，看哪些正常、哪些不正常。这是最直接的证据，因为它不依赖任何人的说法。

- 工具的选择：系统的 `ping`、批量 ping 工具、每种主流语言最常用的 ping 库，以及相邻的工具（traceroute、mtr）
- 程序库要测两种方式：需要 root 的原始套接字，和免 root 的 ping 套接字，内核对它们的处理不同
- 固定下来的部分放进 CI：[`test/e2e-compat.sh`](../test/e2e-compat.sh)

### 2. 搜集网上的需求

搜索人们在问什么、在抱怨什么。搜索的角度：

- 怎样做 X（"怎样只给一个地址加延迟""怎样测试调用 ping 的代码"）
- 现有的工具有什么痛点
- 相邻的需求（伪造 traceroute 路径、按域名模拟）

要注意区分：**这个需求真的是 ping 吗？** 比如"只给一个地址加延迟"的人，想延迟的通常是所有流量，那是流量整形工具的领域。

### 3. 找参考项目，并区分技术路线

找做类似事情的项目，然后先判断它和 pingmock 的**技术路线**是否相同：

- **路线相同**（TUN 网卡、网络命名空间）：可以参考它的实现细节
- **路线不同**（比如 Toxiproxy 是 TCP 代理）：内部设计基本用不上，只借鉴产品层面的做法（API、客户端、文档）

### 4. 读参考项目的源码

带着具体的问题去读：它怎样构造某种回应？遇到某个问题怎样处理？同时留意它的 bug，避免重蹈覆辙（比如 fakert 引用原包时没有长度上限）。

### 5. 挖参考项目的 issue

这一步往往最有价值：

- 按点赞数排序，看用户最想要什么
- 按关键词筛选（test、simulat、dns、ttl……），找和 pingmock 相关的讨论
- **项目作者本人提出、还没有完成的 issue** 价值最高，它说明作者自己想要这个功能（比如 Trippy #1552、#1554）
- 看它**自己的测试**是怎样写的，比如 pro-bing 的测试依赖 `www.google.com`，就说明它缺一个可控的网络

### 6. 用实验验证前提

收集到的证据和设想，都要用实验确认：

- 用最小的原型验证技术上是否可行（比如用几十行 Python 在 TUN 网卡上伪造路径，看 traceroute 能否看到）
- 正反两面都要测：既要在"应该成功"的条件下成功，也要找出"会失败"的条件（比如严格的反向路径检查下，不加路由就会失败）
- 确认实验**真的运行了**：环境没准备好（模块缺失、命令有语法错误）时，结果是假的，不能当作结论

### 7. 综合成路线

- 把证据按**用户**分组，看每类用户需要什么
- 按用户从多到少，排成**每一版都能独立发布、独立带来价值**的版本
- 记录**不做**的方向和原因，避免以后重复讨论

## 工具和技巧

**GitHub API**（`curl` 加 `python3` 就够用）：

| 想知道 | 接口 |
|---|---|
| 项目的 star、活跃程度 | `GET /repos/{owner}/{repo}` |
| 全部 issue（再在本地按点赞排序、按关键词筛选） | `GET /repos/{owner}/{repo}/issues?state=all&per_page=100&page=N`（结果里也包含 PR，用 `pull_request` 字段区分） |
| issue 的正文和评论 | `GET /repos/{owner}/{repo}/issues/{n}` 和 `.../comments` |
| 源码 | `GET /repos/{owner}/{repo}/contents/{path}`，加上请求头 `Accept: application/vnd.github.raw` |
| 目录结构 | `GET /repos/{owner}/{repo}/git/trees/{branch}?recursive=1` |
| 某个修复从哪个版本开始包含 | `GET /repos/{owner}/{repo}/compare/{commit}...{tag}`，`status` 为 `ahead` 或 `identical` 时说明这个版本包含它 |

注意：只授权给自己仓库的细粒度 token 可以读取公开仓库，但**不能用搜索接口**；不带 token 的搜索接口每分钟只能调用 10 次。

**实验**：

- 网络命名空间和挂载命名空间：`unshare --user --map-root-user --net --mount`，普通用户就能运行
- TUN 网卡的原型：参见[数据包详解](packets.zh-CN.md#附录自己抓包)附录里的 Python 脚本
- 本地模拟后台进程时，进程不会像在容器里那样随着 1 号进程退出而被清理，要自己停掉

**和社区互动**：Hacker News、Reddit r/golang 和 V2EX 都禁止 AI 生成的帖子或回复。所以在外部项目的 issue 中介绍 pingmock、参与讨论，都应该由作者本人进行；AI 可以准备素材、核对事实。

## 第一轮：2026 年 10 月

### 兼容性测试

| 工具 / 库 | 结果 |
|---|---|
| 系统 `ping`（iputils）、fping、Go pro-bing、Python icmplib（原始套接字和 ping 套接字） | 正常：延迟、确定性丢包、不可达、劫持真实地址都正确 |
| traceroute（默认 UDP 模式） | 每一跳都是 `*`，到不了终点：pingmock 当时不回应 UDP 探测（v0.8.0 有路径之前） |
| traceroute -I、mtr | 只显示一跳：pingmock 当时不理会 TTL（v0.8.0 有路径之前） |
| `ping www.baidu.com`（实验） | root 模式下改 `/etc/hosts` 可行；`exec` 模式下域名解析失败 |

### 网上的需求

样本有限，没有量化数据。

| 需求 | 证据 | 结论 |
|---|---|---|
| 只给某个目标加延迟 | 几乎都用 `tc netem`，而且很容易出错：只想延迟一个地址，结果所有流量都被延迟；不加过滤会拖慢自己的 SSH | 通常是所有流量，属于流量整形，pingmock 不做 |
| 测试调用 ping 的代码 | Hawkular 的 Pinger 测试 ping 真实网站，网站宕机测试就失败；有人用 pytest-mock 替换 `icmplib.async_ping`，因为替换位置不对而不生效 | pingmock 的核心，已经解决 |
| 测试网络诊断工具 | Trippy #759：最重要的集成测试方案是用 TUN 设备模拟网络，回应 ICMP、UDP 和 TCP 探测 | 需要路径模拟 |

### 参考项目

| 项目 | 路线 | 学到的 |
|---|---|---|
| [Toxiproxy](https://github.com/Shopify/toxiproxy)（12000 star） | 不同：TCP 代理 | 借鉴 `POST /reset`、`GET /version`、多语言客户端、README 说清楚定位。维护者在 [#39](https://github.com/Shopify/toxiproxy/issues/39) 中认为三层代理需要 TUN、等于重写，而且他们看重跨平台，所以向它贡献 ping 支持不可行 |
| [Trippy](https://github.com/fujiapple852/trippy) 的模拟测试 | 相同：TUN | 路径模型和 pingmock 的设计几乎一样（按 TTL 的一系列跳，每跳回复或不回应）；终点对 ICMP 回 Echo Reply、UDP 回端口不可达、**TCP 只需回 SYN-ACK**；28 个场景文件可作为验收清单 |
| [fakert](https://github.com/sehaas/fakert)、[traceroute-haiku](https://github.com/benjojo/traceroute-haiku) | 相同：TUN + 路由 | IPv6 的 TTL 超时是 ICMPv6 类型 3/代码 0、端口不可达是类型 1/代码 4；fakert 引用整个原包、没有长度上限，要避免；traceroute-haiku 按规则自动生成中间节点，中间节点的名字来自反向 DNS |
| [slirp4netns](https://github.com/rootless-containers/slirp4netns)、pasta | 相同：命名空间 | 普通用户可以在用户命名空间 + 挂载命名空间里覆盖 `/etc/resolv.conf`（**已用 `/etc/hosts` 实验验证**：glibc、Go、Python 都能读到，宿主机不受影响）；内置 DNS 转发的思路；外网访问可以借助它们而不必自己实现 |

### 参考项目的 issue

| issue | 内容 | 对 pingmock 的意义 |
|---|---|---|
| [Trippy #1552](https://github.com/fujiapple852/trippy/issues/1552)（作者本人，未完成） | 把模拟器做成独立的命令行工具 | 印证路径模拟的方向 |
| [Trippy #1554](https://github.com/fujiapple852/trippy/issues/1554)（作者本人，未完成） | 某一跳丢包时，同一轮中之后的跳也要丢失 | 路径模拟要区分**链路丢包**（影响之后所有跳）和**只是不回应**（ICMP 限速，只影响这一跳） |
| slirp4netns #333、#225、#116、#347、#312、#280 | `/etc/resolv.conf` 是符号链接；只转发给第一个 DNS 服务器；不支持搜索域；默认网段和用户网络重叠 | 域名支持要验证的清单 |
| Toxiproxy #281、#447、#274、#273 | 呼声最高的：配置文件中声明故障、自定义随机种子、按子网设置故障 | pingmock 都已具备，应该在定位说明中突出 |
| [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70)、#111、#116、#27，go-ping #192 | 超时时没有返回错误、不可达的主机会让 `Run()` 一直阻塞；单元测试并发 ping 真实网站；记录和设置 TTL | ping 库的开发者是一类新用户；自定义回复 TTL 有了需求证据 |

### 实验

| 实验 | 结论 |
|---|---|
| Python 原型在 TUN 网卡上伪造两个中间节点 | traceroute（ICMP 和 UDP）、mtr 都能看到；pingmock 读到的 TTL 是原始值 |
| 严格的反向路径检查（`rp_filter=1`） | 不为中间节点的地址加路由时，它们的回复被内核丢弃；加了路由就正常 |
| 普通用户在命名空间中覆盖 `/etc/hosts` | 可行，各语言的解析都读到新的内容，宿主机不受影响 |
| 两个版本的 iputils 收到校验和错误的回显应答 | 20240117 没有提示，20240905 起标出 `(BAD CHECKSUM!)`（提交 bacf1b7） |

## 第二轮：2026 年 10 月 20 日

由 [`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent 做市场调研，下面每一行都再由人打开来源核对过。这一轮也核对了路线中关于运维人员、此前来自另一个对话而没有来源的几条说法。

### 核对的说法

| 说法 | 结论 | 依据 |
|---|---|---|
| Zabbix 用户只能临时改小阈值来测试 ping 告警 | **不成立** | Zabbix 自带测试触发器表达式的功能；它自己的工单 [ZBX-19712](https://support.zabbix.com/browse/ZBX-19712) 复现主机宕机的做法是 "Simulate "host down" situation (power off host, or add block firewall rule)" |
| Uptime Kuma 曾因 bug 每秒都在 ping | **找不到依据** | 唯一的来源 [#4837](https://github.com/louislam/uptime-kuma/issues/4837)「Uptime-Kuma causing ping floods」被协作者反驳（"In the timeframe, of 190s, that is reasonable"），2024-08-26 由 stale bot 关闭，没有修复 |
| 伪造网络回应有「七条技术路线」 | **没有来源** | 没有文档这样统计。按找到的项目分组是七到八条，其中 **LD_PRELOAD 拦截**套接字调用这一条，这份文档以前没有列出 |
| 绑定网卡的 ping 对运维工具很重要 | **成立，但细节要更正** | 现在的 mwan3（OpenWrt 的多 WAN 切换）已经不用 `ping -I`：[mwan3track](https://raw.githubusercontent.com/openwrt/packages/master/net/mwan3/files/usr/sbin/mwan3track) 运行 `WRAP $PING -n -c $count ...`，`WRAP` 设置 `DEVICE=$DEVICE SRCIP=$SRC_IP FWMARK=... LD_PRELOAD=/lib/mwan3/libwrap_mwan3_sockopt.so.1.0`；[sockopt_wrap.c](https://raw.githubusercontent.com/openwrt/packages/master/net/mwan3/src/sockopt_wrap.c) "intercepts syscalls to various networking functions"，设置 `SO_BINDTODEVICE` 和 `SO_MARK`。绑定到真实网卡的探测绕过 pingmock 的路由，所以 mwan3 现在大概率无法模拟（待实验验证） |

### 参考项目的 issue，再看一遍

| Issue | 变化 | 对 pingmock 的意义 |
|---|---|---|
| [Toxiproxy #739](https://github.com/Shopify/toxiproxy/pull/739) | 2026-06-29 合并："feat: add packet_loss toxic for simulating flaky network conditions" | Toxiproxy 也有了丢包，针对 TCP |
| [Toxiproxy #447](https://github.com/Shopify/toxiproxy/issues/447) | 2026-10-14：有贡献者提出 "an optional toxics array to each proxy in the existing config file"，另一位贡献者（不是核心维护者）表示欢迎 | 「在配置文件中声明故障」可能不再是和 Toxiproxy 的区别 |
| [Toxiproxy #610](https://github.com/Shopify/toxiproxy/pull/610) | 2024-12-27 起开放："Added support for setting seed for deterministic results." | 随机种子同理。两者任一合并时，要重新检查 README 中的对比 |
| [pro-bing #195](https://github.com/prometheus-community/pro-bing/pull/195) | 「Add ICMP time exceeded support」，"useful when you want to catch ICMP time exceeded messages, in order to implement traceroute-like tools"；2026-10-11 维护者要求修改 | 路径模拟可以在没有真实路由器的情况下测试它：服务用户 ② 的机会 |
| [Trippy #1551](https://github.com/fujiapple852/trippy/pull/1551) | 草稿「WIP: Extract simulator to trippy-sim crate」，"Closes #1552"，最后更新 2025-12-07 | Trippy 自己在做 #1552：将来是用户 ④ 的另一个选择 |
| [slirp4netns #361](https://github.com/rootless-containers/slirp4netns/issues/361) | 作者本人，2026-04-07："I'm considering deprecating slirp4netns in favor of it [gvisor-tap-vsock]" | `exec` 的外网访问应考虑 pasta 和 gvisor-tap-vsock，而不是 slirp4netns |
| Trippy #1552、#1554，pro-bing #70 | 没有变化 | |

pingmock 自己还没有 issue 或 pull request。Discussions 没有查（需要认证的 API）。

## 第三轮：多协议，2026 年 10 月 20 日

由 [`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent 为下一个协议做了市场调研、技术选型和设计方案。它建议先做 TCP 连接行为、再做 DNS 故障，已被采纳。下面几行由人核对过；报告的其余部分是 agent 的结论，没有核对。

| 证据 | 来源 | 意义 |
|---|---|---|
| CPython 专门维护一台主机来测连接超时 | [`Lib/test/test_timeout.py`](https://raw.githubusercontent.com/python/cpython/main/Lib/test/test_timeout.py)：`blackhole = resolve_address('pythontest.net', 56666)`，"Blackhole has been configured to silently drop any incoming packets." | 项目自己搭建、维护 TCP 黑洞 |
| LiteLLM 把超时测试指向不可路由的地址 | [PR #39266](https://github.com/BerriAI/litellm/pull/39266)，2026-09-02 起开放："test(local_testing): point the sub-ms timeout tests at an unroutable api_base" | 这种办法取决于测试所在的网络 |
| asyncpg 多主机切换与超时 | [#1155](https://github.com/MagicStack/asyncpg/issues/1155)，2024-05-18 起开放："Timeout configuration for multi-host config" | 主机之间切换，需要一台超时、一台正常应答 |
| pingmock 现在对 TCP 的回应 | 在开发容器里人工重跑的实验：有路径、规则 `200ms` 时，Python 的 `connect` 200.7ms 成功，随后读取超时；`timeout` 让连接超时；`unreachable` 立即失败，`No route to host` | 同一个机制，加上拒绝和按端口的规则，就能在两种模式下覆盖连接行为 |

反面的证据：这些项目都用不可路由的地址或 iptables 规则低成本地解决了问题；Kubernetes 的探针不在 `pingmock exec` 里运行。DNS 故障方面，agent 没有找到有开发者需要它，混沌工具也已经提供。

## 后面几轮：after=、DNS 和通往 v1.0，2026 年 10 月 20、21 日

同一个 agent 又做了三轮。标了*核对过*的行是人工打开或重跑过的，其余是 agent 报告的。

| 轮次 | 证据 | 来源 | 决定了什么 |
|---|---|---|---|
| `after=close\|reset`（v0.10.0） | 有人要「connection reset」故障，后来成了 Toxiproxy 的 `reset_peer`（*核对过*） | [Toxiproxy #238](https://github.com/Shopify/toxiproxy/issues/238) | 值得做，因为 pingmock 能在程序本来就用的地址上提供它 |
| | TUN 上的原型：在握手的最后一个 ACK 上发 FIN，40 次结果都一样；在那里发 RST 会和 `connect` 赛跑（40 次里 38 次让它失败）；在第一段数据上发 RST 可以复现 | 开发容器里的实验 | close 在最后一个 ACK 上，reset 在第一段数据上 |
| DNS 故障（v0.11.0） | 两个小项目在 2026 年 9、10 月自己做了按域名注入 DNS 错误的办法，各有 0 到 1 个反应 | probeboard-api #74，network-doctor #368、#394 | 证据弱：维护者决定做，只在 `pingmock exec` 下，那里本来就有 DNS 服务 |
| | 宿主机运行 nscd 时，`exec` 里 `-host` 的域名解析成了宿主机的地址（*核对过：复现，然后修复*） | 实验 | 对命令隐藏 nscd |
| | CI 机器上的 `search` 行让 SERVFAIL 最后变成了「找不到」（*核对过：CI 失败后复现*） | 2026-10-21 的 CI | 文档和检查改用末尾带点的域名 |
| 通往 v1.0 | 没有 `after=` 的 TCP 规则本来就能让 TLS 握手超时（*核对过*） | 实验：Python `ssl` 报 "The handshake operation timed out"，curl 退出码 28 | 写进文档，不需要新功能 |
| | Toxiproxy 在 2026-10-07 关闭了「一段时间后关闭连接」的请求，没有合并；「Protocol aware toxics」从 2015 年开着，9 个 👍（*核对过*） | [Toxiproxy #195](https://github.com/Shopify/toxiproxy/issues/195)、[#50](https://github.com/Shopify/toxiproxy/issues/50) | 对握手之后的数据只有间接的需求；计划手写一个子集，不用用户态协议栈 |

随后维护者定下了通往 v1.0 的顺序：先把常用协议做完整，兼容性承诺放在最后。计划见 [TODO.md](../TODO.md)。

## 第七轮：竞品和 v1.0 之后的方向，2026 年 10 月 23 日

[`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent 把 pingmock 和 34 个竞品按技术路线分组比较，并实验了绑定网卡的探测能否到达 pingmock。得出的路线见[技术路线图](roadmap.zh-CN.md)。标了*已核对*的行是人工打开来源或重新运行过的，其余是 agent 报告的。星数和日期来自 2026-10-23 的 GitHub API。

### 新的证据

| 证据 | 来源 | 意义 |
|---|---|---|
| "Without the DROP rule, Docker Desktop answers "host unreachable" for that IP right away, which hides the bug, so a firewall rule is the reliable way to reproduce it."*（已核对）* | [asyncpg #1155](https://github.com/MagicStack/asyncpg/issues/1155)，2026-10-15 的评论 | 模拟连接超时的土办法因机器而异；这正是 pingmock 的 `timeout` 和 `unreachable` |
| Toxiproxy v2.13.0（2026-10-14）包含了 `packet_loss`，API 加了 bearer token，没有认证且监听非本机地址时会警告*（已核对）* | 发布说明；compare API 显示这个 tag 在 [#739](https://github.com/Shopify/toxiproxy/pull/739) 的合并提交之后（`ahead`） | pingmock 的镜像在 `0.0.0.0:9100` 上监听，没有认证，而默认值到 v1.0 就冻结 |
| Toxiproxy 的 seed 和在配置文件里写故障仍未完成*（已核对）* | [#610](https://github.com/Shopify/toxiproxy/pull/610)、[#447](https://github.com/Shopify/toxiproxy/issues/447)（18 👍） | pingmock 的差异（可复现、写在文件里）仍然成立 |
| Toxiproxy 的作者 2015 年提过 DNS 故障，后来关掉了 | [Toxiproxy #17](https://github.com/Shopify/toxiproxy/issues/17)："A lot of software assumes fast DNS lookups. Let's challenge assumptions!" | 一个没有人在代理之外做出来的需求 |
| Trippy 的 TUN 模拟器从 2026-02-08 起支持 IPv6 | [Trippy #1741](https://github.com/fujiapple852/trippy/issues/1741) | 独立工具仍是草稿（[#1551](https://github.com/fujiapple852/trippy/pull/1551)） |
| 基于 tc 的工具没满足的需求是：每个目标或端口有多条独立的规则 | [comcast #42](https://github.com/tylertreat/comcast/issues/42)、Pumba #115 和 #130、[Chaos Mesh #2295](https://github.com/chaos-mesh/chaos-mesh/issues/2295) | pingmock 在 Linux 上已经有了 |
| macOS 上按目标过滤会失效 | [comcast #82](https://github.com/tylertreat/comcast/issues/82)、[#44](https://github.com/tylertreat/comcast/issues/44)、[#45](https://github.com/tylertreat/comcast/issues/45) | 反对移植到 macOS 的证据 |
| pingmock 自己：0 星，0 个 issue，0 个 pull request | `GET /repos/nanmuyao/pingmock` | 仍然没有用户反馈 |

### 按技术路线分的竞品

代理，程序要改成连接它：

| 项目 | 星数 | 说明 |
|---|---|---|
| [Shopify/toxiproxy](https://github.com/Shopify/toxiproxy) | 12,451 | TCP 代理；没有 seed；10 种语言的客户端；有 Testcontainers 模块；有 Windows 版 |
| [kffl/speedbump](https://github.com/kffl/speedbump) | 1,557 | TCP 延迟代理；2024 年起不活跃 |
| [brk0v/trixter](https://github.com/brk0v/trixter) | 128 | TCP 代理，2025 年新出；唯一有 seed 的代理（`--random-seed`） |
| [mefellows/muxy](https://github.com/mefellows/muxy) | 827 | L4–L7 代理；整形部分封装了 Comcast，需要 root；不活跃 |
| [h2non/toxy](https://github.com/h2non/toxy) | 2,712 | HTTP 代理；已归档 |
| [fetch-kit/chaos-proxy](https://github.com/fetch-kit/chaos-proxy) | 27 | HTTP 代理，2025–26 年新出 |
| [wiremock/wiremock](https://github.com/wiremock/wiremock) | 7,405 | 带故障和延迟的 HTTP mock；HTTP 的标准 |
| [SpectoLabs/hoverfly](https://github.com/SpectoLabs/hoverfly) | 2,527 | HTTP 模拟 |
| [mock-server/mockserver-monorepo](https://github.com/mock-server/mockserver-monorepo) | 4,996 | HTTP mock 和代理 |
| [mitmproxy/mitmproxy](https://github.com/mitmproxy/mitmproxy) | 46,206 | HTTP(S) 代理；透明模式没有查 |

封装 tc、netem 和 iptables 的工具，作用在真实网络上，需要 root：

| 项目 | 星数 | 说明 |
|---|---|---|
| [tylertreat/comcast](https://github.com/tylertreat/comcast) | 10,516 | 按地址、协议和端口；最后一个版本在 2015 年 |
| [thombashi/tcconfig](https://github.com/thombashi/tcconfig) | 854 | tc，可选 iptables |
| [sitespeedio/throttle](https://github.com/sitespeedio/throttle) | 385 | macOS 上用 pfctl，Linux 上用 tc；整张网卡 |
| [alexei-led/pumba](https://github.com/alexei-led/pumba) | 3,180 | 按容器用 netem 和 iptables；macOS 上在 Linux 虚拟机里运行 |
| [worstcase/blockade](https://github.com/worstcase/blockade) | 911 | 按容器；不活跃 |
| [jagt/clumsy](https://github.com/jagt/clumsy) | 6,274 | Windows，用 WinDivert |
| [Ednk-1312/Chaos](https://github.com/Ednk-1312/Chaos) | 5 | macOS，pf 和 dummynet，需要特权；有 seed |

Kubernetes 混沌平台：[Chaos Mesh](https://github.com/chaos-mesh/chaos-mesh)（7,948）、[Litmus](https://github.com/litmuschaos/litmus)（5,731）、[ChaosBlade](https://github.com/chaosblade-io/chaosblade)（6,469）、[krkn](https://github.com/krkn-chaos/krkn)（511）、[obzev0](https://github.com/lumbrjx/obzev0)（53，找到的唯一一个 eBPF 工具，故障仍经过代理）和 [Chaos Toolkit](https://github.com/chaostoolkit/chaostoolkit)（2,033，只做编排）。文档里都没有 seed。

带故障的 DNS 服务：[CoreDNS `erratic`](https://github.com/coredns/coredns/tree/master/plugin/erratic)，客户端要改成用它；[Pakomo](https://github.com/AlfWorks/Pakomo)（1），一个不需要 root、基于 TUN 的 Android 应用，在另一个系统上走了和 pingmock 相同的路线。

用 TUN 或原始套接字回 ICMP：[m-ou-se/pong](https://github.com/m-ou-se/pong)（150，找到的唯一一个伪造 ping 的工具，是个玩笑项目，不按目标）、[Trippy](https://github.com/fujiapple852/trippy) 的内部模拟器（8,073）、[fakert](https://github.com/sehaas/fakert)（3）、[traceroute-haiku](https://github.com/benjojo/traceroute-haiku)（47，已归档）、[cmd-nse-icmp-responder](https://github.com/networkservicemesh/cmd-nse-icmp-responder)（2，Network Service Mesh 内部用）和 [Mock-Net-Ping](https://github.com/mrmuskrat/Mock-Net-Ping)（1，Perl 库的 mock）。

进程内模拟网络，2025–26 年新出，有 seed，但被测代码要接入 `Dial`，只支持 Go：[netchaos](https://github.com/jpgomesr/netchaos)、[gosimnet](https://github.com/glycerine/gosimnet)、[godst](https://github.com/greatliontech/godst)。

参考项目，不是竞品：[slirp4netns](https://github.com/rootless-containers/slirp4netns) 和 [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock)，可以为 `exec` 提供外网；[Mahimahi](https://github.com/ravinet/mahimahi) 和 [netem-trace](https://github.com/stack-rs/netem-trace)，回放录下的延迟的先例。

各类用户最接近的竞品：应用开发者有 Toxiproxy、Go 的 netchaos，或者一条 iptables 规则；ping 库的开发者没有在维护的工具；运维人员有 Comcast、tcconfig 和 netem，Kubernetes 里有 Chaos Mesh；网络诊断工具的开发者有 Trippy 的模拟器，但它还不是独立的工具。

### 实验：绑定网卡的探测

第二轮留下一个前提待验证：绑定到真实网卡的探测会绕过 pingmock 的路由，mwan3 的封装就是用 `SO_BINDTODEVICE` 绑定的。iputils 的 `ping -I <网卡>` 用的是同一个调用（[`ping.c`](https://github.com/iputils/iputils/blob/master/ping/ping.c) 第 772 行）。开发容器，内核 6.12.76-linuxkit，规则 `-rule 10.99.0.1=100ms -seed 1`：

| 探测 | 结果 |
|---|---|
| `ping 10.99.0.1`、`ping -I pingmock0`、`ping -I <eth0 的地址>` | 约 101 ms 收到回应 |
| `ping -I eth0` | 100% 丢包；pingmock 没有记到请求*（已核对）* |
| 用 fwmark 选一张默认路由走 eth0 的路由表（mwan3 的路由方式） | 100% 丢包 |
| 同上，在那张表里加上 pingmock 的网段 | 收到回应 |
| `ping -I eth0`，用 `tc` mirred 把 eth0 发出的包重定向到 `pingmock0`（`u32`），回程同样重定向 | 3 个都收到回应，记到 3 个请求*（已核对）* |
| 两块 dummy 网卡 `wan1`、`wan2`，同样重定向 | 各自按规则回应；历史里能看到各自的源地址 |
| 同上，以普通用户在 `pingmock exec` 里做 | 可行 |

第一次用的是 `flower` 分类器，linuxkit 内核里没有，这是搭建失败，不算结果；上面的结果用的是 `u32`。pingmock 缺的是按网卡或源地址区分的规则：mwan3 经每条 WAN ping 同一个地址，「经 wan1 不通、经 wan2 通」现在写不出来。用 `LD_PRELOAD` 去掉绑定的做法被否决，因为它改变了程序的行为，而且是 C 库。没有验证：真实的 mwan3 封装、IPv6、经过重定向的 TCP 和 UDP、`rp_filter=1`。

### 没有覆盖到的

dnsmasq 的技巧、基于 pasta 的测试框架、在命名空间里运行 tc 和 netem 的测试辅助工具，以及 Discussions。issue 只看了每个项目按反应数排序的前 10 个，不是完整列表。

## exec 访问外网：技术选型，2026 年 10 月 23 日

在等用户提出的几项里，维护者先选了 `pingmock exec` 访问外网，因为它服务的应用开发者人数最多。[`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent 在容器里（Ubuntu 24.04 和 26.04、Alpine 3.20、Debian 12）做了 15 个实验比较各种机制，提出的设计由 [v0.15.0](https://github.com/nanmuyao/pingmock/releases/tag/v0.15.0) 实现。标了*已核对*的行人工重跑过。

| 机制 | 结果 | 结论 |
|---|---|---|
| [pasta](https://passt.top)，接到命令的命名空间上 | 模拟的目标仍按规则回应：100ms 的规则测得 100.4–101.4ms，同时 `https://github.com` 返回 200；`-T 5432` 能访问宿主机的 `127.0.0.1:5432`（*已核对*：Ubuntu 24.04，普通用户；不挂 pasta 时域名解析不了、端口连不上） | **选定**：唯一不改程序就能访问宿主机本机端口的 |
| slirp4netns | 普通用户和 root 都能用，但只能通过 10.0.2.2 访问宿主机本机；作者考虑弃用（[#361](https://github.com/rootless-containers/slirp4netns/issues/361)） | 不用 |
| gvisor-tap-vsock | 文档只有虚拟机模式 | 不用 |
| root 下的 veth 加 NAT | 打开转发后能用，但改了宿主机，命令结束后留下了 NAT 规则 | 不用 |
| pingmock 内部的中继 | 可行，但要 `nft` 程序，约 400–800 行，不支持 ICMP 和 UDP | 不用；外网流量要进历史只能走这条路 |

实验发现、设计采纳的几点：
- pasta 默认会把命令监听的端口暴露到宿主机上，包括没有认证的 API（在 Ubuntu 24.04 的 pasta 上*已核对*；Debian 12 的不会），所以 pingmock 传 `-t none -u none`，只转发指定的端口。
- pasta 必须在 pingmock 加路由之前接上，否则网段重叠时它的路由优先：子进程在管道上等。
- 以 root 运行时，pasta 会降权成 `nobody`，打不开命名空间；给命令一个把所有 ID 映射成自身的用户命名空间，再加 `--runas 0`，在 Ubuntu 24.04 和 26.04 上可行。
- 宿主机的解析器如果在 `127.0.0.53`（systemd-resolved 就是这样），命令访问不到，所以有外网时总是启动命令自己的 DNS 服务。
- Docker 默认的 seccomp 配置挡住 pasta 沙箱的 `pivot_root`，在 Docker Desktop 上即使给了 `SYS_ADMIN` 也一样（*已核对*）；Debian 12 的 pasta 在命名空间消失后不退出（*已核对*），所以由 pingmock 停掉它。
- GitHub 的 Ubuntu runner 本来就装了 pasta（*在 CI 里核对过*）。

需求证据：没有来自用户的。最接近的是 AI agent 沙箱想访问宿主机本机的服务（[anthropics/sandbox-runtime#165](https://github.com/anthropics/sandbox-runtime/issues/165)，21 个反应）。

## 第八轮：v0.15.0 之后做什么，2026 年 10 月 25 日

同一个 agent，约十分钟，做了两个实验。

- **反馈**：没有。pingmock 没有 issue、pull request 和星标，Discussions 页面没有帖子；跟踪的 14 个外部 issue 在 10 月 23 日之后都没有变化。
- **一个 bug**（*已核对：复现了*）。在 `pingmock exec -outside` 下，真实的 1.1.1.1 返回 301；加了 `refuse` 规则后 curl 退出码 7；`DELETE`（204）之后退出码 28，`1.1.1.1 dev pingmock0` 的路由还在。`POST /reset` 也一样。路由器只加不撤（`internal/tun/tun_linux.go`）。已在 [v0.15.1](https://github.com/nanmuyao/pingmock/releases/tag/v0.15.1) 修复：路由跟着规则走。
- **客户端的间接需求**，10 月 25 日各软件包仓库的数据：`toxiproxy-python` 上个月下载 25,613 次，`toxiproxy-node-client` 413,313 次，`@testcontainers/toxiproxy` 约 322,000 次。这些是竞品客户端的用户，不是 pingmock 的。

| 候选 | 用户 | 证据 | 结论 |
|---|---|---|---|
| 路由跟着规则走 | 所有通过 API 模拟真实地址的人 | 上面的 bug | **已做**，v0.15.1 |
| Python 客户端包，只用标准库，附带 pytest fixture | ① | 上面的间接需求；`examples/python-demo/conftest.py` 只支持 ping 规则 | agent 推荐排第二；维护者决定仍放在最低优先级 |
| 按网卡区分的规则 | ③ | 没有来自用户的（第七轮有设计） | 等 |
| Testcontainers 模块、用 netlink 替代 `ip`、Packet Too Big、root 模式的 DNS、回放延迟、外网访问的 IPv6、Java 和 Node 客户端 | — | 没有 | 等 |
| 连到模拟地址、却没有规则的连接记进历史 | ① | 实验里发现：这样的连接不留任何事件 | 等，单独决定 |
| A/AAAA 以外的 DNS 记录 | — | MX、SRV、TXT、HTTPS、CNAME 现在就回「没有记录」，这本身是正确的回答 | **去掉**：没有要做的 |

## 下一次规划时

1. **先看用户的反馈**：[Discussions](https://github.com/nanmuyao/pingmock/discussions) 和 issue 中的真实需求，优先于任何调研
2. 用新版本的工具和库重新运行兼容性测试，并加入新出现的常用工具
3. 回头看这几轮引用的 issue 有没有进展：Trippy #1551、#1552、#1554；pro-bing #70、#195；Toxiproxy #447、#610、#17；Trippy #1741；comcast #42、#82；Chaos Mesh #2295；slirp4netns #361；asyncpg #1155
4. 重复上面的七步，把新的证据补充到这份文档中

Claude Code 的 agent [`pingmock-planning`](../.claude/agents/pingmock-planning.md) 可以承担这套方法中的三类工作：市场调研（第 2、3、5 步，以及引用过的 issue 的进展和用户反馈）、技术选型（针对一个问题比较各个方案，逐一检查四个核心价值，在[开发容器](../.devcontainer/README.zh-CN.md)里做实验验证）和项目设计（为路线中的一项写设计方案，包括用户要写的语法和待确认的问题）。它的每条结论都有来源、实验或代码行作为依据。它只写报告和方案，不改动任何东西；结论写进这份文档之前要核对，设计由维护者决定。
