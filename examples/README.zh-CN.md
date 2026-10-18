# 示例

[English](README.md) | 中文

| 示例 | 演示的内容 |
|---|---|
| [`go-demo/`](go-demo/) | 用 `go test -exec 'pingmock exec ...'` 和 Go 客户端，测试一段按 ping 延迟挑选服务器的 Go 代码 |
| [`python-demo/`](python-demo/) | 在 `pingmock exec` 下用 pytest 测试一段用 ping 检查网络状态的 Python 代码，通过 HTTP 调用控制 API |
| [`shell-demo/`](shell-demo/) | 只用 `bash`、`ping` 和 `curl`，测试一个线路切换的 Shell 脚本 |
| [`pingmock.yaml`](pingmock.yaml) | 一个用到了各种规则、带注释的配置文件 |
| [`docker-compose.yml`](docker-compose.yml) | 用 Docker Compose 让被测程序运行在 pingmock 的网络里 |
