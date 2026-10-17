# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

在 Linux 上模拟 ping（ICMP Echo）的响应，可以精确设定每个目标地址的**延迟、抖动和丢包率**，并在运行中随时修改。

适用场景：被测软件通过 ping 测量网络延迟，并根据延迟做出不同行为（选择最快的服务器、切换线路、判断网络是否可用、显示信号强度等）。有了 pingmock，这些行为可以被稳定地复现和测试。

## 原理

pingmock 创建一块 TUN 虚拟网卡，把测试网段（默认 `10.99.0.0/24`）以及你指定的地址路由到它上面。内核会把发往这些地址的 ICMP Echo 请求交给 pingmock，pingmock 按匹配的规则等待一段时间后构造回复，或者直接丢弃。

因此被测软件**不需要任何修改**：无论它调用 `ping` 命令，还是自己用原始套接字发送 ICMP，都会被模拟。回复会原样带回请求中的负载，所以 `ping` 这类在负载里写入时间戳的工具，计算出的延迟就是设定值。

## 安装

需要 Go 1.22+，只依赖标准库。运行时需要 `ip` 命令（iproute2）。

```bash
go install github.com/nanmuyao/pingmock@latest
```

或者从源码构建：

```bash
git clone https://github.com/nanmuyao/pingmock.git
cd pingmock
go build -o pingmock .
```

用 `go install` 安装的程序在 `$(go env GOPATH)/bin` 下。因为需要 root 权限运行，`sudo` 时可能找不到它，可以写完整路径，例如 `sudo $(go env GOPATH)/bin/pingmock`。

## 使用

需要 root 权限（或 `CAP_NET_ADMIN`）。

```bash
sudo ./pingmock \
  -rule 10.99.0.1=50ms \
  -rule 10.99.0.2=delay=200ms,jitter=30ms \
  -rule 10.99.0.3=loss=20% \
  -rule 10.99.0.4=timeout
```

然后在另一个终端：

```bash
ping 10.99.0.1   # 约 50ms
ping 10.99.0.2   # 170–230ms 之间随机
ping 10.99.0.3   # 约 20% 丢包
ping 10.99.0.4   # 全部超时
ping 10.99.0.9   # 没有规则，使用默认值（立即回复）
```

按 Ctrl+C 退出后，虚拟网卡和相关路由会自动删除。

### 规则格式

`-rule 目标=规则`，可以重复使用。

**目标**是 IPv4 地址或网段，如 `10.99.0.1`、`10.99.0.128/25`。多条规则匹配时，**网段最精确的那条生效**。

**规则**可以是：

| 写法 | 含义 |
|---|---|
| `120ms`、`120`、`1.5s` | 固定延迟（纯数字表示毫秒） |
| `delay=200ms,jitter=30ms` | 延迟在 170–230ms 之间均匀随机 |
| `loss=5%` 或 `loss=0.05` | 5% 的请求不回复 |
| `timeout` | 全部不回复，等同于 `loss=100%` |

各项可以组合，例如 `delay=80ms,jitter=10ms,loss=2%`。

### 模拟真实地址

如果被测软件 ping 的是写死的真实地址（比如 `8.8.8.8`），直接为它设置规则即可：

```bash
sudo ./pingmock -rule 8.8.8.8=120ms
```

pingmock 会把这个地址路由到虚拟网卡上。**注意：在 pingmock 运行期间，本机发往该地址的所有流量都会被拦截**（不只是 ping），退出后自动恢复。建议在测试机或容器里使用。

如果软件 ping 的是域名，可以在 `/etc/hosts` 里把域名指向测试网段的地址，例如 `10.99.0.1 api.example.com`。

### 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-rule` | | `目标=规则`，可重复 |
| `-default` | `0ms` | 没有匹配规则时使用的规则 |
| `-addr` | `10.99.0.254/24` | 虚拟网卡的地址和网段。网段内**除这个地址本身外**的地址都是模拟主机 |
| `-dev` | `pingmock0` | 虚拟网卡名称 |
| `-api` | `127.0.0.1:9100` | 控制 API 的监听地址，设为空字符串则关闭 |
| `-v` | `false` | 打印每个请求的处理日志 |

## 运行时控制 API

测试过程中可以通过 HTTP 随时修改规则，修改立即生效。请求体可以是和 `-rule` 相同的文本格式，也可以是 JSON。

```bash
# 查看当前规则和每个目标的统计（请求数、回复数、丢弃数）
curl localhost:9100/rules

# 设置或修改规则
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,loss=10%'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# 删除规则（回到默认值）
curl -X DELETE localhost:9100/rules/10.99.0.1

# 修改默认规则
curl -X PUT localhost:9100/default -d timeout

# 清零统计
curl -X DELETE localhost:9100/stats
```

## 在自动化测试中使用

典型流程：启动 pingmock，让被测软件 ping 测试地址，在测试过程中通过 API 改变延迟，再检查软件的反应。

```python
import subprocess, time, requests

pm = subprocess.Popen(["sudo", "./pingmock", "-rule", "10.99.0.1=20ms", "-rule", "10.99.0.2=200ms"])
time.sleep(1)
try:
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # 网络状况变化：原来快的线路变慢了
    requests.put("http://127.0.0.1:9100/rules/10.99.0.1", data="500ms")
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

    # 统计可以用来确认软件确实发出了 ping
    stats = requests.get("http://127.0.0.1:9100/rules").json()["stats"]
    assert stats["10.99.0.1"]["requests"] > 0
finally:
    pm.terminate()
```

`e2e_test.sh` 是一个完整的端到端检查脚本：

```bash
go build -o pingmock . && sudo ./e2e_test.sh
```

单元测试不需要 root：

```bash
go test -race ./...
```

## 限制

- 只支持 IPv4（ICMPv6 的 ping 不会被模拟）。
- 只模拟 ICMP Echo。发往模拟地址的 TCP、UDP 流量不会得到响应。
- 延迟的精度在 1ms 以内（实测设定 50ms 时为 50.4ms），额外的零点几毫秒来自内核和调度开销。
- 不支持分片的 ICMP 包（例如 `ping -s 2000`），这类请求会被忽略。

## 许可证

[MIT](LICENSE)
