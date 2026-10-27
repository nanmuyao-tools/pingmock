# GitHub Actions 示例：在 CI 中使用 pingmock

[English](README.md) | 中文

[`pingmock-tests.yml`](pingmock-tests.yml) 是一份可以直接复制的 GitHub Actions 配置：在 CI 中安装 pingmock，然后在 pingmock 下运行测试，不需要 `sudo`。

把它复制到自己仓库的 `.github/workflows/` 目录下，再把最后一步改成自己的测试命令即可。

## 每一步做了什么

### 1. 安装 pingmock

```yaml
- name: Install pingmock
  run: |
    base="https://github.com/nanmuyao-tools/pingmock/releases/download/$PINGMOCK_VERSION"
    file="pingmock-linux-$(dpkg --print-architecture)"
    curl -fsSLO "$base/$file"
    curl -fsSLO "$base/SHA256SUMS"
    sha256sum --check --ignore-missing SHA256SUMS
    sudo install -m 755 "$file" /usr/local/bin/pingmock
    ...
```

- 从[发布页](https://github.com/nanmuyao-tools/pingmock/releases)下载 `PINGMOCK_VERSION` 指定的版本。**固定版本号**能保证 CI 每次用的都是同一个 pingmock，不会因为新版本发布而突然出现变化
- `dpkg --print-architecture` 会根据机器返回 `amd64` 或 `arm64`，正好对应发布页上的两个文件名，所以在 GitHub 的 ARM 机器上也能直接使用
- 安装之前先用发布页上的 `SHA256SUMS` **校验文件**，确认下载的程序没有损坏或被篡改

### 2. 允许 pingmock 创建用户命名空间

```yaml
- name: Allow pingmock to create user namespaces
  run: |
    curl -fsSL "https://raw.githubusercontent.com/nanmuyao-tools/pingmock/$PINGMOCK_VERSION/contrib/apparmor/pingmock" |
      sudo tee /etc/apparmor.d/pingmock >/dev/null
    sudo apparmor_parser -r /etc/apparmor.d/pingmock
```

GitHub 的 Ubuntu 机器通过 AppArmor 限制了普通用户创建用户命名空间：Ubuntu 24.04 是这样，`ubuntu-latest` 在 2026-10-19 至 2026-11-19 之间迁移到的 Ubuntu 26.04 也是这样。没有这一步，`pingmock exec` 会失败，错误信息会提到 `apparmor_restrict_unprivileged_userns`。

这一步安装的 [AppArmor 配置](../../contrib/apparmor/pingmock)**只为 `/usr/local/bin/pingmock` 放行**，不影响机器上的其他程序；它里面写的路径正好是第 1 步的安装位置，所以不需要修改。配置文件取自和程序相同的版本。

### 3. 运行测试

```yaml
- name: Test
  run: go test -exec "pingmock exec -config pingmock.yaml --" ./...
```

把这一步换成自己的测试命令。其他语言的写法，可以参考对应的示例：

| 测试方式 | `run:` 写法 | 示例 |
|---|---|---|
| Go | `go test -exec "pingmock exec -config pingmock.yaml --" ./...` | [go-demo](../go-demo/README.zh-CN.md) |
| Python（pytest） | `pingmock exec -config pingmock.yaml -- python -m pytest` | [python-demo](../python-demo/README.zh-CN.md) |
| Shell 脚本 | `pingmock exec -config pingmock.yaml -- ./test.sh` | [shell-demo](../shell-demo/README.zh-CN.md) |

`pingmock exec` 以测试命令的退出码退出，所以测试失败时，这一步也会失败。

## 注意事项

- **依赖要在 `pingmock exec` 之外安装。** `pingmock exec` 里的程序没有外网访问，所以 `pip install`、`npm install` 之类的步骤要放在测试步骤之前单独运行。`go test -exec` 会先编译再运行，所以 Go 不受影响
- **使用 Docker Compose 的项目不需要前两步**，直接在 Compose 文件里使用 pingmock 镜像即可，参见 [compose-demo](../compose-demo/README.zh-CN.md)
- 如果不想安装 AppArmor 配置，也可以把第 3 步改成 `sudo pingmock exec ...`，以 root 运行时不受这个限制。不过测试也会以 root 运行

## 这份配置经过了验证

本仓库的 CI 会**从这份文件中提取每一步的命令，原样执行**，而且在 Ubuntu 24.04 和 26.04 上各执行一遍：安装 `PINGMOCK_VERSION` 指定的正式版，安装 AppArmor 配置，然后运行 [go-demo](../go-demo/README.zh-CN.md) 的测试，并且要求所有测试都通过、没有被跳过。所以照着复制，就能得到一份可用的配置，无论 `ubuntu-latest` 迁移前还是迁移后。
