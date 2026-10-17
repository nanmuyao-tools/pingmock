# TODO

v0.2–v0.4 的规划已经全部完成，v0.5 又改用了 YAML 配置文件（见 [Releases](https://github.com/nanmuyao/pingmock/releases)）。接下来的重点不是继续加功能，而是**让真实用户用起来**，再根据反馈决定方向。

## 1. 推广

发帖前先准备好素材，效果会好很多：

- [ ] 录一段演示（[asciinema](https://asciinema.org) 或 GIF），放在 README 开头：`pingmock exec` 一条命令改变 ping 延迟
- [ ] 写一段 30 秒能看懂的介绍：解决什么问题 → 一个最短的示例 → 和 `tc netem` 的区别（按目标地址设置、运行中可修改、不需要 root）

然后发到这些社区，**每个社区先读一遍发帖规则**：

- [ ] **V2EX**：「分享创造」节点，用中文介绍
- [ ] **Reddit r/golang**：英文，强调 Go 实现、依赖极少、`go test -exec` 的用法
- [ ] **Hacker News**：标题用 `Show HN: Pingmock – mock ping latency, loss and unreachable errors on Linux` 这样的格式；发帖后在评论区回答问题
- [ ] （可选）掘金、Go 中文社区、Twitter / X

## 2. 收集反馈

- [ ] 开启 GitHub Discussions，用来回答使用问题、讨论想法
- [ ] 添加 issue 模板：bug 报告（版本、系统、复现步骤）和功能建议（使用场景）
- [ ] 把收到的需求记在这里，按**有多少人需要**排优先级

## 3. 候选方向（等反馈再决定）

| 方向 | 说明 | 备注 |
|---|---|---|
| TCP / HTTP 延迟模拟 | 很多软件用 TCP 连接耗时测延迟，而不是 ping | 需求可能最大，但范围会扩大很多，项目定位会从"ping 模拟器"变成"网络模拟器" |
| traceroute 支持 | 模拟 TTL 超时（ICMP Time Exceeded），让 traceroute 显示设定的路径 | 和现有架构契合 |
| 更多异常行为 | 重复回复（`DUP!`）、乱序、损坏的回复 | 测试软件的健壮性 |
| 其他系统 | macOS 等 | `exec` 依赖 Linux 命名空间，工作量大 |

## 4. 维护

- [ ] 不再迭代时，在 GitHub 上删除用于推送的 Fine-grained token（**2026-11-16 过期**）
- [ ] 关注 GitHub Actions 中使用的 action 版本（`actions/checkout`、`docker/*`），有大版本更新时升级
