# GitHub Actions example: pingmock in CI

English | [中文](README.zh-CN.md)

[`pingmock-tests.yml`](pingmock-tests.yml) is a GitHub Actions workflow ready to copy: it installs pingmock in CI and runs the tests under it, without `sudo`.

Copy it into your repository's `.github/workflows/` directory and change the last step to your own tests.

## What each step does

### 1. Install pingmock

```yaml
- name: Install pingmock
  run: |
    base="https://github.com/nanmuyao/pingmock/releases/download/$PINGMOCK_VERSION"
    file="pingmock-linux-$(dpkg --print-architecture)"
    curl -fsSLO "$base/$file"
    curl -fsSLO "$base/SHA256SUMS"
    sha256sum --check --ignore-missing SHA256SUMS
    sudo install -m 755 "$file" /usr/local/bin/pingmock
    ...
```

- It downloads the version named in `PINGMOCK_VERSION` from the [releases](https://github.com/nanmuyao/pingmock/releases). **Pinning the version** keeps CI on the same pingmock every time, so a new release never changes things unexpectedly
- `dpkg --print-architecture` gives `amd64` or `arm64` depending on the machine, which matches the release's two file names, so this works as is on GitHub's ARM runners too
- Before installing, it **checks the file** against the release's `SHA256SUMS`, so a damaged or tampered download is never installed

### 2. Allow pingmock to create user namespaces

```yaml
- name: Allow pingmock to create user namespaces
  run: |
    curl -fsSL "https://raw.githubusercontent.com/nanmuyao/pingmock/$PINGMOCK_VERSION/contrib/apparmor/pingmock" |
      sudo tee /etc/apparmor.d/pingmock >/dev/null
    sudo apparmor_parser -r /etc/apparmor.d/pingmock
```

`ubuntu-latest` (Ubuntu 24.04) restricts unprivileged user namespaces with AppArmor; without this step `pingmock exec` fails, with an error that mentions `apparmor_restrict_unprivileged_userns`.

The [AppArmor profile](../../contrib/apparmor/pingmock) it installs **only allows them for `/usr/local/bin/pingmock`** and affects nothing else on the machine; the path in it is exactly where step 1 installs pingmock, so it needs no change. It comes from the same version as the binary.

### 3. Run the tests

```yaml
- name: Test
  run: go test -exec "pingmock exec -config pingmock.yaml --" ./...
```

Replace this with your own tests. For other languages, see the matching example:

| Tests | `run:` | Example |
|---|---|---|
| Go | `go test -exec "pingmock exec -config pingmock.yaml --" ./...` | [go-demo](../go-demo/) |
| Python (pytest) | `pingmock exec -config pingmock.yaml -- python -m pytest` | [python-demo](../python-demo/) |
| A shell script | `pingmock exec -config pingmock.yaml -- ./test.sh` | [shell-demo](../shell-demo/) |

`pingmock exec` exits with the tests' status, so the step fails when the tests do.

## Things to know

- **Install dependencies outside `pingmock exec`.** Programs inside it have no network access, so steps such as `pip install` or `npm install` go before the test step. `go test -exec` builds first and runs afterwards, so Go is not affected
- **Projects using Docker Compose need neither of the first two steps**: use the pingmock image in the Compose file instead; see [compose-demo](../compose-demo/)
- If you would rather not install the AppArmor profile, step 3 can run `sudo pingmock exec ...` instead, since root is not restricted. The tests then run as root too

## This workflow is tested

This repository's CI **takes the commands of each step from this file and runs them as written**: it installs the release named in `PINGMOCK_VERSION`, installs the AppArmor profile, and runs [go-demo](../go-demo/)'s tests, requiring all of them to pass with none skipped. What you copy is known to work.
