# Docker Compose 示例：在容器里使用 pingmock

[English](README.md) | 中文

这个示例演示怎样用 Docker Compose 让一个**被测服务**运行在 pingmock 的网络里。被测服务就是 [Shell 示例](../shell-demo/README.zh-CN.md)中的线路切换脚本，在这里它作为一个持续运行的监控服务，每 2 秒检查一次线路。

## 组成

[`compose.yaml`](compose.yaml) 定义了三个服务：

| 服务 | 作用 |
|---|---|
| `pingmock` | 运行 pingmock，按 [`pingmock.yaml`](pingmock.yaml) 模拟两条线路；控制 API 映射到宿主机的 `127.0.0.1:9100` |
| `monitor` | **被测服务**：每 2 秒运行一次 `failover.sh`，在日志里输出当前应该使用的线路 |
| `tests` | 运行 Shell 示例的测试 `test.sh` |

关键是这一行：

```yaml
network_mode: service:pingmock
```

它让 `monitor` 和 `tests` 加入 `pingmock` 容器的网络，于是它们发出的 ping 和连接都会被模拟，`127.0.0.1:9100` 就是控制 API。`pingmock` 的健康检查确保它准备好之后，另外两个服务才启动。

pingmock 容器需要 `NET_ADMIN` 权限和 TUN 设备（`cap_add` 和 `devices` 两项），被测服务的容器则不需要任何特殊权限。

## 现场演示

启动 pingmock 和监控服务，并查看监控日志：

```bash
docker compose up -d monitor
docker compose logs -f monitor
```

```
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
```

在**另一个终端**里，通过宿主机上的 API 把主线路设成断开：

```bash
curl -X PUT localhost:9100/rules/10.99.0.1 -d timeout
```

监控服务马上切换到备用线路：

```
monitor-1  | link=backup primary unhealthy (no reply); backup: 3/3 replies, 40ms
```

再让主线路恢复，可以看到防抖动的效果：先显示"恢复中 1/2"，下一次检查才切回主线路：

```bash
curl -X PUT localhost:9100/rules/10.99.0.1 -d 20ms
```

```
monitor-1  | link=backup primary recovering (1/2): 3/3 replies, 20ms
monitor-1  | link=primary primary healthy: 3/3 replies, 20ms
```

结束后停止所有服务：

```bash
docker compose down
```

## 运行测试

```bash
docker compose run --rm tests
```

```
PASS uses the primary while it is healthy
...
15 passed, 0 failed
```

`docker compose run` 会先启动 `tests` 依赖的 `pingmock`，但**不会**启动 `monitor`。这一点很重要：`monitor` 每 2 秒就会 ping 一次主线路，如果和测试同时运行，会干扰"检查发了几个 ping"之类的测试。所以如果之前用 `up` 启动过 `monitor`，先 `docker compose down` 再运行测试。

`docker compose run` 以测试的退出码退出，可以直接用在 CI 里。

## 用到自己的项目里

1. 复制 `pingmock` 服务的配置：`image`、`cap_add`、`devices` 和 `healthcheck`
2. 在被测服务上加上 `network_mode: service:pingmock`，以及等待 pingmock 就绪的 `depends_on`
3. 在测试中通过 `http://127.0.0.1:9100` 控制 pingmock；从宿主机控制的话，像这个示例一样让 pingmock 监听 `0.0.0.0:9100`，并把端口映射到 `127.0.0.1`

注意：使用 `network_mode: service:pingmock` 的服务不能再设置自己的 `ports`，需要对外提供的端口要写在 `pingmock` 服务上。另外，Docker 默认在容器里禁用 IPv6，所以 pingmock 只会使用 IPv4，并在日志里提示这一点。
