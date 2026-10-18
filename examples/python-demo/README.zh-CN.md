# Python 示例：测试一个判断网络状态的模块

[English](README.md) | 中文

这个示例演示怎样用 pingmock 测试一段**依赖 ping 结果**的 Python 代码。被测代码完全不知道 pingmock 的存在，测试时不需要修改它，也不需要 root。测试只用到 pytest 和 Python 标准库，通过 HTTP 调用 pingmock 的控制 API，不需要 Go。

## 被测代码

[`netcheck.py`](netcheck.py) 调用系统的 `ping` 命令，同时 ping 8.8.8.8 和 1.1.1.1 两台公共 DNS 服务器，判断网络状态：

| 状态 | 条件 | 命令行的退出码 |
|---|---|---|
| `online`（在线） | 至少有一台在 200ms 内回复，且丢包不超过 20% | 0 |
| `degraded`（不稳定） | 有回复，但都太慢或丢包太多 | 1 |
| `offline`（离线） | 都没有回复 | 2 |

被测代码 ping 的是**真实的公网地址**。在 `pingmock exec` 里，模拟 8.8.8.8 只影响被测程序，机器上的其他程序照常访问真实的 8.8.8.8。

## 运行测试

需要先[安装 pingmock](../../README.zh-CN.md#安装)，然后在这个目录下执行：

```bash
pip install -r requirements.txt      # 先在外面安装：pingmock exec 里没有外网
pingmock exec -config pingmock.yaml -- python3 -m pytest -v
```

`pingmock exec` 让 pytest 运行在 pingmock 的独立网络里，并通过环境变量 `PINGMOCK_API` 告诉它控制 API 的地址。[`pingmock.yaml`](pingmock.yaml) 让两台服务器都快速回复，每个测试再按需修改。

不在 pingmock 下运行时（比如直接 `python3 -m pytest`），测试会自动跳过，不会失败。

## 每个测试演示了什么

| 测试 | 模拟的网络状况 | 用到的 pingmock 功能 |
|---|---|---|
| `test_online_when_hosts_answer_quickly` | 两台都快速回复 | [配置文件](../../README.zh-CN.md#配置文件)里的规则 |
| `test_online_while_one_host_is_down` | 8.8.8.8 没有响应，1.1.1.1 正常 | `timeout` |
| `test_degraded_when_every_host_is_slow` | 两台都很慢，其中一台有抖动 | `delay=300ms,jitter=20ms` |
| `test_degraded_when_every_host_loses_pings` | 两台都每三个 ping 丢一个 | `pattern=..x`（确定性的丢包，每次结果都一样） |
| `test_offline_when_unreachable_is_reported_quickly` | 两台都返回"目标不可达"，而且**很快就返回** | `unreachable`、`unreachable=admin` |
| `test_offline_when_nothing_answers` | 两台都没有响应 | `timeout` |
| `test_notices_when_the_connection_drops` | 回复 3 次之后断开 | [场景](../../README.zh-CN.md#场景随时间变化的规则)：`15ms for 3 pings; timeout` |
| `test_pings_each_host_the_given_number_of_times` | — | 用[请求历史](../../README.zh-CN.md#运行时控制-api)检查被测代码实际发了几个 ping |

"不可达"和"超时"的区别值得留意：两者都导致离线，但不可达的错误会立即返回，所以第 5 个测试还检查了整个检测在 1 秒内完成。

## 测试的辅助工具

[`conftest.py`](conftest.py) 里有一个 pytest fixture `pingmock`，是一个只用标准库写成的小客户端。可以直接复制到自己的项目里用：

```python
def test_something(pingmock):
    pingmock.set_rule("8.8.8.8", "timeout")    # 只在这个测试中生效
    ...
    events = pingmock.history("8.8.8.8")       # 被测代码发出的 ping
```

它有一个细节：测试结束时，它会把修改过的规则**恢复成测试开始前的样子**，而不是直接删除。因为被测代码 ping 的地址是固定的，配置文件已经为它们设置了规则，直接删除会让它们变成默认规则，影响后面的测试。

## 运行命令行程序

```bash
pingmock exec -config pingmock.yaml -- python3 netcheck.py
```

```
8.8.8.8           15.4ms  loss   0%
1.1.1.1           20.7ms  loss   0%
online: 8.8.8.8 answers in 15ms
```

换一组网络状况，不需要修改代码：

```bash
pingmock exec -config pingmock.yaml -rule 8.8.8.8=350ms -rule '1.1.1.1=delay=500ms,pattern=..x' -- python3 netcheck.py
```

```
8.8.8.8          351.0ms  loss   0%
1.1.1.1          500.5ms  loss  33%
degraded: best is 8.8.8.8: 351ms, 0% lost
```

```bash
pingmock exec -config pingmock.yaml -rule 8.8.8.8=unreachable -rule 1.1.1.1=unreachable=admin -- python3 netcheck.py
```

```
8.8.8.8         no reply (3 errors)
1.1.1.1         no reply (3 errors)
offline: no host answers (6 unreachable errors)
```

## 用到自己的项目里

1. 把 [`conftest.py`](conftest.py) 复制到测试目录，测试函数加上 `pingmock` 参数即可使用
2. 写一个配置文件，列出测试开始时的网络状况
3. 先安装好依赖，再用 `pingmock exec -config pingmock.yaml -- python3 -m pytest` 运行测试；在 CI 中也这样运行。GitHub Actions 的 Ubuntu 机器需要先安装 AppArmor 配置，参见[系统要求](../../README.zh-CN.md#系统要求)

其他语言也可以照这个思路：从环境变量 `PINGMOCK_API` 读出地址，用该语言的 HTTP 库调用[控制 API](../../README.zh-CN.md#运行时控制-api)。
