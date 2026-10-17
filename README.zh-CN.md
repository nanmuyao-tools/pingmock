# pingmock

[![CI](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml/badge.svg)](https://github.com/nanmuyao/pingmock/actions/workflows/ci.yml)

[English](README.md) | 中文

在 Linux 上模拟 ping（ICMP Echo）的响应，可以精确设定每个目标地址的**延迟、抖动和丢包率**，并在运行中随时修改。

适用场景：被测软件通过 ping 测量网络延迟，并根据延迟做出不同行为（选择最快的服务器、切换线路、判断网络是否可用、显示信号强度等）。有了 pingmock，这些行为可以被稳定地复现和测试。

## 原理

pingmock 创建一块 TUN 虚拟网卡，把测试网段（默认 `10.99.0.0/24`）以及你指定的地址路由到它上面。内核会把发往这些地址的 ICMP Echo 请求交给 pingmock，pingmock 按匹配的规则等待一段时间后构造回复，或者直接丢弃。

因此被测软件**不需要任何修改**：无论它调用 `ping` 命令，还是自己用原始套接字发送 ICMP，都会被模拟。回复会原样带回请求中的负载，所以 `ping` 这类在负载里写入时间戳的工具，计算出的延迟就是设定值。

## 安装

运行时需要 `ip` 命令（iproute2）。

**下载编译好的程序**（不需要安装 Go）：在 [Releases](https://github.com/nanmuyao/pingmock/releases) 页面下载对应架构的文件，例如：

```bash
curl -L -o pingmock https://github.com/nanmuyao/pingmock/releases/latest/download/pingmock-linux-amd64
chmod +x pingmock
```

**用 Go 安装**（需要 Go 1.22+，只依赖标准库）：

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

**规则**由以下设置组成：

| 写法 | 含义 |
|---|---|
| `120ms`、`120`、`1.5s` | 固定延迟（纯数字表示毫秒） |
| `delay=200ms,jitter=30ms` | 延迟在 170–230ms 之间均匀随机 |
| `loss=5%` 或 `loss=0.05` | 每个请求有 5% 的概率不回复 |
| `pattern=..x` | 确定性丢包：`.` 回复，`x` 丢弃，按顺序循环（这里是每 3 个请求丢第 3 个） |
| `timeout` | 全部不回复，等同于 `loss=100%` |

各项可以组合，例如 `delay=80ms,jitter=10ms,loss=2%`。`loss` 和 `pattern` 不能同时使用。

### 场景：随时间变化的规则

一条规则可以由多个**阶段**组成，用 `;` 分隔。每个阶段持续一段时间（`for 10s`）或一定数量的请求（`for 5 pings`），最后一个阶段会一直保持。如果以 `repeat` 结尾，则会从头循环：

```bash
# 前 10 个 ping 正常，然后断网 5 秒，然后恢复
sudo ./pingmock -rule '10.99.0.1=30ms for 10 pings; timeout for 5s; 30ms'

# 线路时好时坏：好 20 秒，差 10 秒，一直循环
sudo ./pingmock -rule '10.99.0.2=40ms for 20s; delay=400ms,jitter=100ms for 10s; repeat'
```

按时间切换的阶段从规则设置的那一刻开始计时；按请求数切换的阶段，对每个目标地址分别计数。重新设置规则（例如通过 API）会让它从第一个阶段重新开始。

如果测试要求每次结果都一样，优先使用按请求数切换的阶段（`for 5 pings`），因为它不受测试运行快慢的影响。

### 可复现的随机性

`loss` 和 `jitter` 是随机的，但可以复现。每个目标地址有自己独立的随机序列，由**种子**和地址共同决定。所以发往某个地址的请求，无论和发往其他地址的请求怎样交错，得到的结果都相同。

不指定 `-seed` 时，pingmock 会随机选一个种子，并在启动时打印出来：

```
seed 4871203317 (pass -seed 4871203317 to reproduce this run)
```

测试失败时，用这个种子重新运行，就能得到完全相同的丢包和延迟。在 CI 里建议固定种子（例如 `-seed 42`），让每次运行的行为都一致。

### 模拟真实地址

如果被测软件 ping 的是写死的真实地址（比如 `8.8.8.8`），直接为它设置规则即可：

```bash
sudo ./pingmock -rule 8.8.8.8=120ms
```

pingmock 会把这个地址路由到虚拟网卡上。**注意：在 pingmock 运行期间，本机发往该地址的所有流量都会被拦截**（不只是 ping），退出后自动恢复。建议在测试机或容器里使用。

如果软件 ping 的是域名，可以在 `/etc/hosts` 里把域名指向测试网段的地址，例如 `10.99.0.1 api.example.com`。

### 配置文件

规则较多时，用 JSON 文件更容易维护，还可以和测试代码放在一起。参见 [`examples/pingmock.json`](examples/pingmock.json)：

```json
{
  "seed": 42,
  "default": "1ms",
  "rules": {
    "10.99.0.1": "20ms",
    "10.99.0.2": "delay=120ms,jitter=15ms,loss=2%",
    "10.99.0.3": "30ms for 10 pings; timeout for 5s; 30ms",
    "10.99.0.4": {
      "phases": [
        {"spec": "delay=40ms,pattern=....x", "for": "20s"},
        {"spec": {"delay": "400ms", "jitter": "100ms"}, "for": "10s"}
      ],
      "repeat": true
    },
    "8.8.8.8": "timeout"
  }
}
```

```bash
sudo ./pingmock -config examples/pingmock.json
```

每条规则可以写成和 `-rule` 相同的文本，也可以写成 JSON 对象。配置文件还可以设置 `dev`、`addr`、`api` 和 `history`。命令行参数会覆盖配置文件，所以一个配置文件可以用于多个测试：`-config base.json -rule 10.99.0.1=500ms` 只替换这一条规则。配置文件中出现未知的字段会直接报错，拼写错误在启动时就能发现。

### 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-rule` | | `目标=规则`，可重复 |
| `-default` | `0ms` | 没有匹配规则时使用的规则 |
| `-config` | | JSON 配置文件 |
| `-seed` | 随机 | `loss` 和 `jitter` 的随机种子，相同的种子会复现相同的运行结果 |
| `-addr` | `10.99.0.254/24` | 虚拟网卡的地址和网段。网段内**除这个地址本身外**的地址都是模拟主机 |
| `-dev` | `pingmock0` | 虚拟网卡名称 |
| `-api` | `127.0.0.1:9100` | 控制 API 的监听地址，设为空字符串则关闭 |
| `-history` | `1000` | `GET /history` 保留的最近请求数，`0` 表示关闭 |
| `-v` | `false` | 打印每个请求的处理日志 |
| `-version` | | 打印版本号并退出 |

## 运行时控制 API

测试过程中可以通过 HTTP 随时修改规则，修改立即生效。请求体可以是和 `-rule` 相同的文本格式，也可以是 JSON。

```bash
# 查看种子、规则和每个目标的统计（请求数、回复数、丢弃数、当前阶段）
curl localhost:9100/rules

# 设置或修改规则
curl -X PUT localhost:9100/rules/10.99.0.1 -d 300ms
curl -X PUT localhost:9100/rules/10.99.0.1 -d 'delay=300ms,pattern=.x'
curl -X PUT localhost:9100/rules/10.99.0.1 -d '50ms for 3 pings; timeout'
curl -X PUT localhost:9100/rules/10.99.0.128/25 -d '{"delay":"80ms","jitter":"5ms","loss":0.02}'

# 删除规则（回到默认值）
curl -X DELETE localhost:9100/rules/10.99.0.1

# 修改默认规则
curl -X PUT localhost:9100/default -d timeout

# 清零统计
curl -X DELETE localhost:9100/stats

# 最近的请求，按时间从早到晚排列；可以按地址或网段过滤，只保留最后 N 条
curl 'localhost:9100/history?target=10.99.0.1&limit=20'

# 清空历史
curl -X DELETE localhost:9100/history
```

每条历史记录的格式如下（`phase` 从 1 开始计数，`delay` 只在回复时出现）：

```json
{"time": "2026-10-17T11:16:10.113Z", "src": "10.99.0.254", "dst": "10.99.0.1",
 "id": 4242, "seq": 3, "reply": true, "delay": "40ms", "rule": "10.99.0.1/32", "phase": 2}
```

## 在自动化测试中使用

典型流程：用固定的种子启动 pingmock，让被测软件 ping 测试地址，在测试过程中改变网络状况，再检查软件的反应。

```python
import subprocess, time, requests

API = "http://127.0.0.1:9100"
pm = subprocess.Popen(["sudo", "./pingmock", "-seed", "42",
                       "-rule", "10.99.0.1=20ms", "-rule", "10.99.0.2=200ms"])
time.sleep(1)
try:
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.1"

    # 网络状况变化：原来快的线路变慢了
    requests.put(f"{API}/rules/10.99.0.1", data="500ms")
    assert my_app.pick_fastest(["10.99.0.1", "10.99.0.2"]) == "10.99.0.2"

    # 重试逻辑：前两个 ping 丢失，之后线路恢复
    requests.delete(f"{API}/history")
    requests.put(f"{API}/rules/10.99.0.3", data="timeout for 2 pings; 30ms")
    assert my_app.check_reachable("10.99.0.3")
    events = requests.get(f"{API}/history", params={"target": "10.99.0.3"}).json()["events"]
    assert [e["reply"] for e in events] == [False, False, True]
finally:
    pm.terminate()
```

单元测试不需要 root，[`test/e2e.sh`](test/e2e.sh) 中的端到端测试需要：

```bash
make test   # go vet 和带竞态检测的单元测试
make e2e    # 编译 pingmock，并用 sudo 运行 test/e2e.sh
```

代码的组织方式参见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 限制

- 只支持 IPv4（ICMPv6 的 ping 不会被模拟）。
- 只模拟 ICMP Echo。发往模拟地址的 TCP、UDP 流量不会得到响应。
- 延迟的精度在 1ms 以内（实测设定 50ms 时为 50.4ms），额外的零点几毫秒来自内核和调度开销。
- 不支持分片的 ICMP 包（例如 `ping -s 2000`），这类请求会被忽略。

## 许可证

[MIT](LICENSE)
