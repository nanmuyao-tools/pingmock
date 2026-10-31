# GitHub Actions example: ghostwire in CI

English | [中文](README.zh-CN.md)

[`ghostwire-tests.yml`](ghostwire-tests.yml) is a GitHub Actions workflow ready to copy: it installs ghostwire in CI and runs the tests under it, without `sudo`.

Copy it into your repository's `.github/workflows/` directory and change the last step to your own tests.

## What each step does

### 1. Install ghostwire

```yaml
- name: Install ghostwire
  run: |
    base="https://github.com/nanmuyao-tools/ghostwire/releases/download/$GHOSTWIRE_VERSION"
    file="ghostwire-linux-$(dpkg --print-architecture)"
    curl -fsSLO "$base/$file"
    curl -fsSLO "$base/SHA256SUMS"
    sha256sum --check --ignore-missing SHA256SUMS
    sudo install -m 755 "$file" /usr/local/bin/ghostwire
    ...
```

- It downloads the version named in `GHOSTWIRE_VERSION` from the [releases](https://github.com/nanmuyao-tools/ghostwire/releases). **Pinning the version** keeps CI on the same ghostwire every time, so a new release never changes things unexpectedly
- `dpkg --print-architecture` gives `amd64` or `arm64` depending on the machine, which matches the release's two file names, so this works as is on GitHub's ARM runners too
- Before installing, it **checks the file** against the release's `SHA256SUMS`, so a damaged or tampered download is never installed

### 2. Allow ghostwire to create user namespaces

```yaml
- name: Allow ghostwire to create user namespaces
  run: |
    curl -fsSL "https://raw.githubusercontent.com/nanmuyao-tools/ghostwire/$GHOSTWIRE_VERSION/contrib/apparmor/ghostwire" |
      sudo tee /etc/apparmor.d/ghostwire >/dev/null
    sudo apparmor_parser -r /etc/apparmor.d/ghostwire
```

GitHub's Ubuntu runners restrict unprivileged user namespaces with AppArmor: Ubuntu 24.04, and Ubuntu 26.04, which `ubuntu-latest` moves to between 2026-10-19 and 2026-11-19, alike. Without this step `ghostwire exec` fails, with an error that mentions `apparmor_restrict_unprivileged_userns`.

The [AppArmor profile](../../contrib/apparmor/ghostwire) it installs **only allows them for `/usr/local/bin/ghostwire`** and affects nothing else on the machine; the path in it is exactly where step 1 installs ghostwire, so it needs no change. It comes from the same version as the binary.

### 3. Run the tests

```yaml
- name: Test
  run: go test -exec "ghostwire exec -config ghostwire.yaml --" ./...
```

Replace this with your own tests. For other languages, see the matching example:

| Tests | `run:` | Example |
|---|---|---|
| Go | `go test -exec "ghostwire exec -config ghostwire.yaml --" ./...` | [go-demo](../go-demo/) |
| Python (pytest) | `ghostwire exec -config ghostwire.yaml -- python -m pytest` | [python-demo](../python-demo/) |
| A shell script | `ghostwire exec -config ghostwire.yaml -- ./test.sh` | [shell-demo](../shell-demo/) |

`ghostwire exec` exits with the tests' status, so the step fails when the tests do.

## Things to know

- **Install dependencies outside `ghostwire exec`.** Programs inside it have no network access, so steps such as `pip install` or `npm install` go before the test step. `go test -exec` builds first and runs afterwards, so Go is not affected
- **Projects using Docker Compose need neither of the first two steps**: use the ghostwire image in the Compose file instead; see [compose-demo](../compose-demo/)
- If you would rather not install the AppArmor profile, step 3 can run `sudo ghostwire exec ...` instead, since root is not restricted. The tests then run as root too

## This workflow is tested

This repository's CI **takes the commands of each step from this file and runs them as written**, on both Ubuntu 24.04 and 26.04: it installs the release named in `GHOSTWIRE_VERSION`, installs the AppArmor profile, and runs [go-demo](../go-demo/)'s tests, requiring all of them to pass with none skipped. What you copy is known to work, before and after `ubuntu-latest` moves.
