# Examples

English | [中文](README.zh-CN.md)

| Example | What it shows |
|---|---|
| [`go-demo/`](go-demo/) | Testing Go code that picks servers by ping latency, with `go test -exec 'pingmock exec ...'` and the Go client |
| [`python-demo/`](python-demo/) | Testing Python code that checks the network with ping, with pytest under `pingmock exec` and the control API over HTTP |
| [`pingmock.yaml`](pingmock.yaml) | A configuration file using every kind of rule, with comments |
| [`docker-compose.yml`](docker-compose.yml) | Running the code under test in pingmock's network with Docker Compose |
