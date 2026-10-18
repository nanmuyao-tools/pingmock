# TODO

已发布 v0.5.0（见 [Releases](https://github.com/nanmuyao/pingmock/releases)）：v0.2–v0.4 的规划全部完成，v0.5 改用了 YAML 配置文件。之后又在 main 分支上完成了代码结构重构、3 份技术文档（[往返之旅](docs/packet-path.zh-CN.md)、[数据包详解](docs/packets.zh-CN.md)、[代码导读](docs/architecture.zh-CN.md)）和 5 个[完整示例](examples/README.zh-CN.md)，尚未发布。

接下来的重点不是继续加功能，而是**让真实用户用起来**，再根据反馈决定方向。

## 下一步（按顺序）

### 1. 升级 GitHub Actions 的版本

- [x] CI 出现了 Node.js 20 弃用警告：`actions/checkout@v4`、`actions/setup-go@v5` 等使用的 Node.js 20 已被 GitHub 弃用，将来可能停止支持
- [x] 升级 `.github/workflows/` 中的全部 action（包括 `setup-python`、`docker/*`），以及 [`examples/github-actions/pingmock-tests.yml`](examples/github-actions/pingmock-tests.yml)：用户复制这份示例时会遇到同样的问题
- [x] 只选择发布超过两周的版本；升级后确认 CI 的三个任务都通过，且不再出现弃用警告

### 2. 发布 v0.5.1

- [ ] 没有功能变化，但积累了不少改进（见上），发布后用户在 Releases 页面才能看到
- [ ] 升级 action 之后必须跑一次发布流程，才能确认发布流程依然正常，所以放在第 1 项之后
- [ ] 发布后按 [CONTRIBUTING](CONTRIBUTING.md#releasing) 更新示例中的版本号：`examples/github-actions/pingmock-tests.yml` 的 `PINGMOCK_VERSION`，`examples/go-demo/go.mod` 的 `require`

### 3. README 开头加一段演示动画

- [x] 用真实的命令输出生成（[asciinema](https://asciinema.org) 或 SVG），展示一条 `pingmock exec` 命令怎样改变程序的选择，例如 [go-demo](examples/go-demo/) 的 `fastest` 程序在不同规则下选出不同的服务器：[`docs/demo/record.sh`](docs/demo/record.sh) 录制，结果在 [`docs/demo/demo.svg`](docs/demo/demo.svg)
- [ ] 现在的录像是在没有 IPv6 的容器里录的，开头有一行 IPv6 不可用的提示，版本号也是伪版本。发布 v0.5.1 之后，在有 IPv6 的机器上用 `VERSION=v0.5.1 docs/demo/record.sh` 重录一次
- [x] 写一段 30 秒能看懂的介绍：解决什么问题 → 一个最短的示例 → 和 `tc netem` 的区别（按目标地址设置、运行中可修改、不需要 root）

### 4. 准备收集反馈的渠道（要在发帖之前完成）

- [ ] 开启 GitHub Discussions，用来回答使用问题、讨论想法（仓库 Settings → General → Features → Discussions；issue 模板的「Question or idea to discuss」链接已经指向它，开启前这个链接是 404）
- [x] 添加 issue 模板：bug 报告（版本、系统、复现步骤）和功能建议（使用场景）

### 5. 起草推广帖子，由作者本人发布

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

## 候选方向（等反馈再决定）

| 方向 | 说明 | 备注 |
|---|---|---|
| TCP / HTTP 延迟模拟 | 很多软件用 TCP 连接耗时测延迟，而不是 ping | 需求可能最大，但范围会扩大很多，项目定位会从"ping 模拟器"变成"网络模拟器" |
| traceroute 支持 | 模拟 TTL 超时（ICMP Time Exceeded），让 traceroute 显示设定的路径 | 和现有架构契合 |
| 更多异常行为 | 重复回复（`DUP!`）、乱序、损坏的回复 | 测试软件的健壮性 |
| 其他系统 | macOS 等 | `exec` 依赖 Linux 命名空间，工作量大 |

## 维护

- [ ] 删除用于推送的 Fine-grained token（**2026-11-16 过期**）：它出现在过去的对话记录里，切换到新的工作方式后就应该删除；以后需要推送时，每个工作阶段新建一个有效期很短的 token
- [ ] 每次发布后更新示例中的版本号（见第 2 项）
