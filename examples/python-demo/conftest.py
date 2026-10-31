"""A small client for ghostwire's control API, as a pytest fixture.

It uses only the standard library, so the tests need nothing but pytest.
"""

import json
import os
import urllib.parse
import urllib.request

import pytest


class Ghostwire:
    """Changes ghostwire's rules and reads what happened, over HTTP."""

    def __init__(self, url):
        self.url = url.rstrip("/")

    def _call(self, method, path, body=None):
        data = body.encode() if body is not None else None
        req = urllib.request.Request(self.url + path, data=data, method=method)
        with urllib.request.urlopen(req, timeout=10) as resp:
            text = resp.read()
        return json.loads(text) if text else None

    def version(self):
        """The version of the running ghostwire, such as "vX.Y.Z"."""
        return self._call("GET", "/version")["version"]

    def rules(self):
        """The installed rules, as {"8.8.8.8/32": "delay=15ms", ...}."""
        return {r["target"]: r["rule"] for r in self._call("GET", "/rules")["rules"]}

    def set_rule(self, target, rule):
        """Install a rule for target until the end of the test."""
        self._call("PUT", f"/rules/{target}", rule)

    def set_tcp(self, target, rule):
        """Install a TCP rule for target, an address and port such as "10.99.0.10:443", until the end of the test."""
        self._call("PUT", "/tcp/" + urllib.parse.quote(target, safe=":[]"), rule)

    def set_dns(self, name, rule):
        """Set how the tests' DNS queries for name are answered, such as "nxdomain"."""
        self._call("PUT", "/dns/" + urllib.parse.quote(name), rule)

    def history(self, target):
        """The requests ghostwire received for target, oldest first."""
        return self._call("GET", "/history?target=" + urllib.parse.quote(target))["events"]

    def clear_history(self):
        self._call("DELETE", "/history")

    def reset(self):
        """Back to the rules of the configuration file, with no statistics or history."""
        self._call("POST", "/reset")


@pytest.fixture
def ghostwire():
    """The ghostwire these tests run under; skips the test if there is none."""
    url = os.environ.get("GHOSTWIRE_API")
    if not url:
        pytest.skip("not running under ghostwire: run  ghostwire exec -config ghostwire.yaml -- python3 -m pytest")
    pm = Ghostwire(url)
    pm.reset()  # whatever an earlier test changed, start from the configuration file
    return pm
