# Examples

English | [中文](README.zh-CN.md)

| Example | What it shows |
|---|---|
| [`go-demo/`](go-demo/) | Testing Go code that picks servers by ping latency, with `go test -exec 'ghostwire exec ...'` and the Go client |
| [`python-demo/`](python-demo/) | Testing Python code that checks the network with ping, with pytest under `ghostwire exec` and the control API over HTTP |
| [`pinglib-demo/`](pinglib-demo/) | Testing a ping library itself (pro-bing): timeouts, unreachable hosts, loss and TTL, each from a host in the configuration file |
| [`shell-demo/`](shell-demo/) | Testing a link failover shell script with nothing but `bash`, `ping` and `curl` |
| [`compose-demo/`](compose-demo/) | The shell example's script as a service in ghostwire's network with Docker Compose, with a live demonstration and the tests |
| [`github-actions/`](github-actions/) | A GitHub Actions workflow ready to copy: installs ghostwire from the releases, allows it under Ubuntu's AppArmor restriction, and runs the tests |
| [`ghostwire.yaml`](ghostwire.yaml) | A configuration file with comments: ping rules of every kind, and a path. TCP rules work in it too ([TCP connections](../README.md#tcp-connections)); names, `hosts:` and `dns:` need `ghostwire exec` |
| [`docker-compose.yml`](docker-compose.yml) | Running the code under test in ghostwire's network with Docker Compose |
