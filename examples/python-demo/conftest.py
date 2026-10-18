"""A small client for pingmock's control API, as a pytest fixture.

It uses only the standard library, so the tests need nothing but pytest.
"""

import json
import os
import urllib.error
import urllib.parse
import urllib.request

import pytest


class Pingmock:
    """Changes pingmock's rules and reads what happened, over HTTP."""

    def __init__(self, url):
        self.url = url.rstrip("/")
        self._saved = {}  # target -> rule before this test changed it (None if there was none)
        self.rules_before = {}

    def _call(self, method, path, body=None):
        data = body.encode() if body is not None else None
        req = urllib.request.Request(self.url + path, data=data, method=method)
        with urllib.request.urlopen(req, timeout=10) as resp:
            text = resp.read()
        return json.loads(text) if text else None

    def rules(self):
        """The installed rules, as {"8.8.8.8/32": "delay=15ms", ...}."""
        return {r["target"]: r["rule"] for r in self._call("GET", "/rules")["rules"]}

    def set_rule(self, target, rule):
        """Install a rule for target until the end of the test."""
        installed = self._call("PUT", f"/rules/{target}", rule)["target"]
        self._saved.setdefault(installed, self.rules_before.get(installed))

    def history(self, target):
        """The requests pingmock received for target, oldest first."""
        return self._call("GET", "/history?target=" + urllib.parse.quote(target))["events"]

    def clear_history(self):
        self._call("DELETE", "/history")

    def restore(self):
        """Put back the rules this test changed, as the configuration had them."""
        for target, rule in self._saved.items():
            if rule is None:
                try:
                    self._call("DELETE", f"/rules/{target}")
                except urllib.error.HTTPError as e:
                    if e.code != 404:
                        raise
            else:
                self._call("PUT", f"/rules/{target}", rule)


@pytest.fixture
def pingmock():
    """The pingmock these tests run under; skips the test if there is none."""
    url = os.environ.get("PINGMOCK_API")
    if not url:
        pytest.skip("not running under pingmock: run  pingmock exec -config pingmock.yaml -- python3 -m pytest")
    pm = Pingmock(url)
    pm.rules_before = pm.rules()
    yield pm
    pm.restore()
