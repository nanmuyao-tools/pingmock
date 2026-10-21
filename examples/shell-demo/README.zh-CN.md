# Shell 示例：测试一个线路切换脚本

[English](README.md) | 中文

这个示例演示怎样用 pingmock 测试一个**依赖 ping 结果的运维脚本**。被测脚本和测试都只用 `bash`、`ping` 和 `curl`，不需要任何编程语言，也不需要 root。

## 被测脚本

[`failover.sh`](failover.sh) 是运维中常见的线路切换逻辑：检查主线路和备用线路，决定当前该用哪一条。

| 输出 | 条件 | 退出码 |
|---|---|---|
| `link=primary` | 主线路正常：3 个 ping 至少回复 2 个，平均延迟不超过 150ms | 0 |
| `link=backup` | 主线路不正常，备用线路正常 | 1 |
| `link=none` | 两条都不正常 | 2 |

加上 `-s 状态文件` 后，它会记住当前用的线路，并且**防止来回抖动**：切换到备用线路后，主线路要**连续两次**检查都正常才切回去。这样，一条时断时续的主线路不会让脚本跟着来回切换。

阈值都可以用环境变量调整：`COUNT`（每次 ping 几个）、`MIN_REPLIES`、`MAX_MS`、`RECOVER_CHECKS`。

## 运行测试

需要先[安装 pingmock](../../README.zh-CN.md#安装)，然后在这个目录下执行：

```bash
pingmock exec -config pingmock.yaml -- ./test.sh
```

```
PASS uses the primary while it is healthy
PASS switches to the backup when the primary does not answer
...
15 passed, 0 failed
```

不在 pingmock 下运行时，测试会打印 `SKIP` 并正常退出。

## 每个测试演示了什么

[`test.sh`](test.sh) 中的每个测试，开头都用 `network 主线路规则 备用线路规则` **完整写出它需要的网络状况**，所以看一个测试就知道它模拟的是什么网络，测试之间也互不影响。

| 测试 | 模拟的网络状况 | 用到的 pingmock 功能 |
|---|---|---|
| uses the primary while it is healthy | 两条都正常 | 固定延迟 |
| switches to the backup when the primary does not answer | 主线路没有响应 | `timeout` |
| switches to the backup when the primary is unreachable | 主线路返回"目标不可达" | `unreachable` |
| switches to the backup when the primary is slow | 主线路回复了所有 ping，但很慢 | 固定延迟 `300ms` |
| keeps / leaves a primary that loses pings | 主线路三个丢一个 / 三个丢两个 | `pattern=..x`、`pattern=.xx`（确定性的丢包，正好测试阈值的边界） |
| reports no link when both are down | 两条都没有响应 | `timeout` |
| recovery | 主线路断开一次检查的时间，然后恢复 | [场景](../../README.zh-CN.md#场景随时间变化的规则)：`timeout for 3 pings; 20ms` |
| flapping primary | 主线路一直时断时续 | 循环的场景：`timeout for 3 pings; 20ms for 3 pings; repeat` |
| sends 3 pings to the primary and none to the backup | — | 用[请求历史](../../README.zh-CN.md#运行时控制-api)检查脚本实际发了几个 ping |

`recovery` 和 `flapping primary` 这两组测试体现了场景规则的用处：脚本每次检查正好发 3 个 ping，所以 `for 3 pings` 的阶段和脚本的每次运行一一对应，结果每次都完全一样。

这些测试能发现真实的问题。比如把防抖动关掉（`RECOVER_CHECKS=1`），恰好是检查防抖动的 3 个测试会失败：

```
FAIL recovery: waits for a second healthy check: got "link=primary primary healthy: 3/3 replies, 20ms" (exit 0), want link=backup (exit 1) mentioning "recovering (1/2)"
FAIL flapping primary: stays on the backup (check 2): got "link=primary ..." (exit 0), want link=backup (exit 1)
FAIL flapping primary: stays on the backup (check 4): got "link=primary ..." (exit 0), want link=backup (exit 1)
12 passed, 3 failed
```

## 运行脚本

```bash
pingmock exec -config pingmock.yaml -- ./failover.sh 10.99.0.1 10.99.0.2
# link=primary primary healthy: 3/3 replies, 20ms

pingmock exec -config pingmock.yaml -rule 10.99.0.1=timeout -- ./failover.sh 10.99.0.1 10.99.0.2
# link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms

pingmock exec -config pingmock.yaml -rule 10.99.0.1=unreachable -rule 10.99.0.2=timeout -- ./failover.sh 10.99.0.1 10.99.0.2
# link=none primary unhealthy (unreachable); backup unhealthy (no reply)
```

主线路断开后恢复，用状态文件连续检查 3 次，可以看到防抖动的效果：

```bash
pingmock exec -config pingmock.yaml -rule '10.99.0.1=timeout for 3 pings; 20ms' -- \
  sh -c 'for i in 1 2 3; do ./failover.sh -s /tmp/link.state 10.99.0.1 10.99.0.2; done'
```

```
link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms
link=backup primary recovering (1/2): 3/3 replies, 20ms
link=primary primary healthy: 3/3 replies, 20ms
```

## 用到自己的脚本里

1. 测试中用 `curl -X PUT "$PINGMOCK_API/rules/地址" -d 规则` 设置网络状况，用 `curl "$PINGMOCK_API/history?target=地址"` 查看脚本实际发出的 ping
2. 用 `pingmock exec -- ./你的测试脚本` 运行，pingmock 会以测试脚本的退出码退出，所以可以直接用在 CI 里
3. 如果被测脚本 ping 的是真实地址（比如 8.8.8.8），直接为它设置规则即可，`pingmock exec` 里的模拟不会影响机器上的其他程序
