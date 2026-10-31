# 示例

[English](README.md) | 中文

| 示例 | 演示的内容 |
|---|---|
| [`go-demo/`](go-demo/README.zh-CN.md) | 用 `go test -exec 'ghostwire exec ...'` 和 Go 客户端，测试一段按 ping 延迟挑选服务器的 Go 代码 |
| [`python-demo/`](python-demo/README.zh-CN.md) | 在 `ghostwire exec` 下用 pytest 测试一段用 ping 检查网络状态的 Python 代码，通过 HTTP 调用控制 API |
| [`pinglib-demo/`](pinglib-demo/README.zh-CN.md) | 测试 ping 库本身（pro-bing）：超时、不可达、丢包和 TTL，每种行为对应配置文件里的一台主机 |
| [`shell-demo/`](shell-demo/README.zh-CN.md) | 只用 `bash`、`ping` 和 `curl`，测试一个线路切换的 Shell 脚本 |
| [`compose-demo/`](compose-demo/README.zh-CN.md) | 用 Docker Compose 让 Shell 示例中的脚本作为服务运行在 ghostwire 的网络里，包括现场演示和测试 |
| [`github-actions/`](github-actions/README.zh-CN.md) | 一份可以直接复制的 GitHub Actions 配置：从发布页安装 ghostwire，在 Ubuntu 的 AppArmor 限制下为它放行，然后运行测试 |
| [`ghostwire.yaml`](ghostwire.yaml) | 一个带注释的配置文件：各种 ping 规则和一条路径。TCP 规则也可以写在里面（[TCP 连接](../README.zh-CN.md#tcp-连接)）；域名、`hosts:` 和 `dns:` 需要 `ghostwire exec` |
| [`docker-compose.yml`](docker-compose.yml) | 用 Docker Compose 让被测程序运行在 ghostwire 的网络里 |
