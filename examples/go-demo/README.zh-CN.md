# Go 示例：测试一个按延迟挑选服务器的程序

[English](README.md) | 中文

这个示例演示怎样用 ghostwire 测试一段**依赖 ping 延迟做决定**的 Go 代码。被测代码完全不知道 ghostwire 的存在，测试时也不需要修改它，也不需要 root。

## 被测代码

[`fastest.go`](fastest.go) 是一个常见的场景：调用系统的 `ping` 命令测量几台服务器的延迟，选出最快的一台；当前服务器变慢或不可用时，切换到另一台。

| 函数 | 作用 |
|---|---|
| `Measure` | 对一台服务器 ping 几次，统计平均延迟、丢包率和"不可达"错误 |
| `Pick` | 同时测量多台服务器，返回最快且可用的一台（丢包不超过 25%） |
| `Selector.Check` | 当前服务器依然良好时继续使用，否则重新挑选 |
| `Connect` | 依次连接各台服务器，返回第一个接受 TCP 连接的；连接被拒或超时就换下一台 |

[`cmd/fastest`](cmd/fastest/main.go) 是一个调用它的命令行程序。

## 运行测试

需要先[安装 ghostwire](../../README.zh-CN.md#安装)。然后在这个目录下执行：

```bash
go test -exec 'ghostwire exec -config ghostwire.yaml --' ./...
```

`go test -exec` 会把编译好的测试程序交给 `ghostwire exec` 运行，于是测试运行在 ghostwire 的独立网络里：[`ghostwire.yaml`](ghostwire.yaml) 设置了三台模拟服务器，测试代码再通过 ghostwire 的 [Go 客户端](https://pkg.go.dev/github.com/nanmuyao-tools/ghostwire/client)按需添加其他网络状况。

不在 ghostwire 下运行时（比如直接 `go test ./...`），测试会自动跳过，不会失败。

## 每个测试演示了什么

| 测试 | 模拟的网络状况 | 用到的 ghostwire 功能 |
|---|---|---|
| `TestPicksFastest` | 三台服务器分别是 80ms、20ms、50ms | [配置文件](../../README.zh-CN.md#配置文件)里的规则 |
| `TestSkipsUnreachableServer` | 最快的服务器返回"目标不可达" | `unreachable` |
| `TestSkipsServerThatTimesOut` | 服务器没有响应 | `timeout` |
| `TestAvoidsLossyServer` | 服务器每两个 ping 丢一个 | `pattern=.x`（确定性的丢包，每次结果都一样） |
| `TestSwitchesWhenCurrentServerSlowsDown` | 测试进行到一半，当前服务器变慢 | 用客户端在运行中修改规则 |
| `TestSwitchesWhenCurrentServerGoesDown` | 服务器回复 3 次之后断开 | [场景](../../README.zh-CN.md#场景随时间变化的规则)：`15ms for 3 pings; timeout` |
| `TestSendsConfiguredNumberOfPings` | — | 用[请求历史](../../README.zh-CN.md#运行时控制-api)检查被测代码实际发了几个 ping |
| `TestConnectFailsOverWhenTheConnectionTimesOut` | 一台数据库对连接始终不回应，`connect` 要等到超时 | [TCP 规则](../../README.zh-CN.md#tcp-连接)：`timeout` |
| `TestConnectFailsOverAtOnceWhenRefused` | 一台数据库宕机，立即拒绝连接 | TCP 规则 `refuse`，再用历史检查被测代码只试了一次 |

测试之间互不干扰：每个测试使用不同的地址，并在结束时用 `t.Cleanup` 删除自己添加的规则。

## 运行命令行程序

在 ghostwire 的网络里运行 `cmd/fastest`，可以直观地看到网络状况怎样改变程序的选择：

```bash
go build -o fastest ./cmd/fastest
ghostwire exec -config ghostwire.yaml -- ./fastest 10.99.0.1 10.99.0.2 10.99.0.3
```

```
10.99.0.2         21.1ms  loss   0%
10.99.0.3         51.2ms  loss   0%
10.99.0.1         80.5ms  loss   0%
fastest: 10.99.0.2
```

不改代码，只让最快的服务器丢包，程序就会改选另一台：

```bash
ghostwire exec -config ghostwire.yaml -rule '10.99.0.2=delay=20ms,pattern=.x' -- ./fastest 10.99.0.1 10.99.0.2 10.99.0.3
```

```
10.99.0.3         50.5ms  loss   0%
10.99.0.1         80.7ms  loss   0%
10.99.0.2         20.4ms  loss  33%
fastest: 10.99.0.3
```

**先编译，再运行。** `ghostwire exec` 里的程序没有外网访问，所以如果在里面执行 `go run`，而它需要下载依赖，就会因为 `network is unreachable` 而失败。先在外面 `go build`（或 `go mod download`）就不会有这个问题。`go test -exec` 总是先编译再运行，所以不受影响。

## 用到自己的项目里

1. 在测试中用 `client.FromEnv()` 连接 ghostwire，出错时 `t.Skip`，参考 [`fastest_test.go`](fastest_test.go) 中的 `ghostwire` 函数
2. 写一个配置文件，列出测试开始时的网络状况；每个测试需要的额外状况，用 `SetRule`（连接用 `SetTCP`）添加，用 `t.Cleanup` 删除
3. 用 `go test -exec 'ghostwire exec -config ghostwire.yaml --' ./...` 运行测试；在 CI 中也这样运行。[`examples/github-actions`](../github-actions/README.zh-CN.md) 是一份可以直接复制的配置

配置文件里设置了 `seed: 42`，所以即使用了随机的丢包和抖动，每次运行的结果也都一样。
