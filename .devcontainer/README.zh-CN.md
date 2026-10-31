# 在容器里开发

[English](README.md) | 中文

ghostwire 只能在 Linux 上运行，端到端检查还需要 root、TUN 设备和 iproute2。这个目录是一个[开发容器](https://containers.dev)，把这些都备齐了，所以在 Mac 上，或者任何装了 Docker 的机器上都能开发：仓库挂载进容器，命令在容器里执行，任何一边改了文件，另一边立刻看到。

## 里面有什么

Go（版本固定在 [`Dockerfile`](Dockerfile) 里）、iproute2、`ping`、`curl`、traceroute、mtr、fping、nscd（`ghostwire exec` 的一项检查要用）、带 icmplib 和 pytest 的 Python、shellcheck、git，以及 Claude Code。容器以普通用户 `dev` 运行，需要 root 的检查可以免密码 `sudo`。

## 怎样打开

用 VS Code：安装 Dev Containers 扩展，打开仓库，选择 *Reopen in Container*。用 [devcontainer CLI](https://github.com/devcontainers/cli)：

```bash
npx @devcontainers/cli up --workspace-folder .
npx @devcontainers/cli exec --workspace-folder . make test
```

CLI 要用新版：0.89.0 没问题，0.80.0 在 Docker Desktop 4.68 上启动容器之后会一直卡住，等一行永远不来的 `docker events` 输出。

或者只用 Docker，CI 就是这样用它的：

```bash
docker build -t ghostwire-dev -f .devcontainer/Dockerfile .
docker run --rm -it --cap-add NET_ADMIN --cap-add SYS_ADMIN --device /dev/net/tun \
  --security-opt apparmor=unconfined \
  -v "$PWD:/workspace" -v ghostwire-gomod:/go/pkg/mod -v ghostwire-cache:/home/dev/.cache \
  -w /workspace ghostwire-dev
```

两个卷保存 Go 的模块缓存和构建缓存，所以任何东西第二次构建都是增量的。镜像第一次构建要几分钟，之后只要几秒。

## 各项要多久

在 Mac 上用 Docker Desktop 实测。端到端检查的时间都花在等超时和等 mtr 上，不是在计算，所以机器再快也差不多。

| 命令 | 用时 | 什么时候跑 |
|---|---|---|
| 在 Mac 上直接 `go test ./internal/rule/... ./internal/engine/... ./internal/icmp/... ./internal/responder/... ./internal/dns/... ./internal/config/... ./internal/api/... ./client/...` | 几秒 | 改的不是 `internal/app` 和 `internal/tun`（这两个只能在 Linux 上编译）时 |
| `make test` | 约 15 秒 | 任何改动 |
| `make e2e` | 约 25 秒 | 数据包、规则、路由、路径 |
| `make e2e-exec` | 约 20 秒 | `ghostwire exec`、域名、DNS |
| `make e2e-compat` | 第一次约 25 秒，之后 15 秒 | `icmp` 和 `engine` 包，各种工具和库会用到 |
| `make e2e-all` | 约 25 秒 | 推送之前：上面三项同时跑，用 root 跑的各自在自己的网络命名空间里 |

## 哪些在容器里跑，哪些交给 CI

| 检查 | 容器里 | 说明 |
|---|---|---|
| `make lint`、`make test` | 能 | |
| `make e2e` | 能 | Docker Desktop 的容器里有 IPv6，所以 IPv6 的检查也会跑；Linux 上的 Docker 默认没有，这些检查会跳过 |
| `make e2e-exec` | 能 | 免 root 和 root 两种都跑 |
| `make e2e-compat` | 能 | |
| `make e2e-all` | 能 | |
| `make e2e-docker` | 不能 | 需要 Docker，由 CI 运行 |
| `make e2e-exec` 里 `-outside` 的检查 | 不能 | 镜像里没有 passt，Docker 默认的 seccomp 配置也会挡住 pasta 的沙箱；这些检查会跳过并给出提示。CI 的 `test` 任务会运行它们 |
| Ubuntu 的 AppArmor 对用户命名空间的限制 | 不能 | 容器里没有 AppArmor，CI 的 `test` 任务覆盖 [`contrib/apparmor`](../contrib/apparmor/) 里的配置 |

CI 每次推送都会构建这个容器，并在里面运行前四项，保证它一直可用。

## 需要哪些权限，为什么

[`devcontainer.json`](devcontainer.json) 给了容器 `NET_ADMIN` 和 `/dev/net/tun`，这是 ghostwire 本身需要的；还有 `SYS_ADMIN`，这是 `ghostwire exec` 在容器里需要的：Docker 默认的 seccomp 策略只允许有这个能力的容器创建命名空间，而以 root 运行的 `exec` 要创建网络命名空间，本来就需要这个能力。别的都不需要：不用 `--privileged`，也不用关掉 seccomp（在 Mac 上用 Docker Desktop 4.68 验证过）。

在有 AppArmor 的 Linux 主机上（比如 Ubuntu），Docker 还会套上它默认的 AppArmor 配置，其中禁止 `mount`；`ghostwire exec` 要给命令挂载私有的 `/etc/hosts`，于是报错 `making mounts private: permission denied`。`--security-opt apparmor=unconfined` 只对这个容器去掉这份配置。Mac 上没有 AppArmor，加不加都一样。

## 和 Linux 机器的差别

- Docker Desktop 的解析器对任何域名都会回答一个虚构的地址，不存在的域名也是。所以「`ghostwire exec` 里不存在的域名解析不到」这项检查在这里会跳过，并说明原因。
- 容器里的 `/proc/sys` 是只读的。兼容性检查只在必要时才写 `ping_group_range`，而容器本来就允许所有组使用 ping 套接字。
- 虚拟机里的时间比 Linux 主机上略微抖动：容差很小的检查偶尔会失败一次，重跑就过。

## Claude Code

`claude` 已为用户 `dev` 装好。它的登录状态和设置放在一个卷里，重建容器也不会丢；宿主机的 `~/.claude` 不会共享进来。
