# 测试 ping 库：在 pingmock 下测试 pro-bing

[English](README.md) | 中文

这个示例面向**写 ping 库或维护 ping 库的人**，以及任何自己发送 ICMP 的代码。他们的测试有个难处：最要紧的几种行为——主机一直不回应、回应"不可达"、丢包、不寻常的 TTL——在真实网络上没法随叫随到，于是要么不测，要么在 CI 里去 ping `www.google.com`。pingmock 可以随叫随到地制造这些情况，不需要联网，每次结果都一样。

被测的库是 [pro-bing](https://github.com/prometheus-community/pro-bing)，用得最多的 Go ping 库，原样使用。库里没有任何东西被替换，只有网络是模拟的。

## 主机

[`pingmock.yaml`](pingmock.yaml) 为每种行为定义了一台主机：

| 主机 | 规则 | 行为 |
|---|---|---|
| 10.99.0.1 | `20ms` | 每个 ping 都在 20ms 后回复 |
| 10.99.0.2 | `timeout` | 从不回应 |
| 10.99.0.3 | `unreachable` | 立即回应"目标主机不可达" |
| 10.99.0.4 | `delay=10ms,pattern=.x` | 回复一个、丢一个，如此反复 |
| 10.99.0.5 | `delay=10ms,ttl=128` | 回复的 TTL 是 128，像 Windows 主机那样 |

## 运行测试

先[安装 pingmock](../../README.zh-CN.md#安装)。然后在这个目录里：

```bash
go test -exec 'pingmock exec -config pingmock.yaml --' ./...
```

`go test -exec` 把测试程序交给 `pingmock exec`，让它运行在一个只有这五台主机的独立网络里。虽然库会打开原始套接字，但不需要 root：在 `pingmock exec` 里，测试进程在自己的用户命名空间中就是 root。不在 pingmock 下运行时（直接 `go test ./...`），测试会自动跳过。

## 每个测试演示了什么

| 测试 | 检查什么 | 关于 pro-bing 说明了什么 |
|---|---|---|
| `TestRepliesGiveTheRtt` | 4 个回复，每个约 20ms；pingmock 看到 4 个请求 | RTT 和规则一致；[请求历史](../../README.zh-CN.md#运行时控制-api)证实了库真正发出了什么 |
| `TestTimeoutEndsTheRun` | `Timeout` 到时 `Run` 返回，没有收到回复 | 超时通过统计数据体现，**不是错误**：`Run` 返回 `nil`。这正是 [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70) 讨论的行为；不管一个库决定怎么做，这样的测试都能把它固定下来 |
| `TestUnreachableLooksLikeLoss` | 没有回复，而且一直等到超时 | pro-bing 只数回显应答，所以拒绝 ping 的主机和沉默的主机在它看来没有区别。pingmock 的历史能看出区别：每个请求都得到了回应，只是回应的是 `unreachable` |
| `TestLossIsCounted` | 发 6 个、收 3 个、丢包 50%，每次都一样 | `Count` 数的是回复，所以有丢包时会一直等到超时；确定性的 `pattern` 让数字精确可查 |
| `TestTTLIsReported` | `OnRecv` 和 `Statistics().TTLs` 都是 128 | 回复的 TTL 原样到达调用者 |

每个测试只 ping 自己的那台主机，互不干扰；也都不需要通过控制 API 改任何东西：配置文件就是全部的测试夹具。

## 用到自己的库里

复制 `pingmock.yaml` 和 [`probing_test.go`](probing_test.go) 开头的两个辅助函数，然后为你的库应该处理的每种行为写一个测试。最常用的规则：

- `timeout` 和 `unreachable`（还有 `unreachable=net`、`unreachable=admin`）测错误路径
- `pattern=..x` 精确丢包，`loss=10%` 配合 `seed` 随机但可复现的丢包
- `delay=50ms,jitter=20ms` 测 RTT 统计
- `30ms for 3 pings; timeout` 模拟运行中宕机的主机，见[场景](../../README.zh-CN.md#场景随时间变化的规则)
- `ttl=1` 到 `ttl=255`

[GitHub Actions 示例](../github-actions/)展示了怎样在 CI 里运行这样的测试。
