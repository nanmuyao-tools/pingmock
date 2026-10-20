"""Connects to each ADDR:PORT given with Python's socket module, as Python
programs do, and prints one line per target for test/e2e-compat.sh:

    python 10.99.0.11:80 accepted 201ms read=timeout
    python 10.99.0.12:80 refused

On an accepted connection the client writes a request and reads the
answer, which shows what pingmock does after the handshake: nothing
(timeout), close (closed) or reset (reset). Standard library only.
"""

import socket
import sys
import time


def outcome(err):
    if isinstance(err, ConnectionRefusedError):
        return "refused"
    if isinstance(err, ConnectionResetError):
        return "reset"
    if isinstance(err, (socket.timeout, TimeoutError)):
        return "timeout"
    if isinstance(err, OSError) and err.errno in (113, 101):  # EHOSTUNREACH, ENETUNREACH
        return "unreachable"
    return f"error: {err!r}"


for target in sys.argv[1:]:
    host, port = target.rsplit(":", 1)
    start = time.monotonic()
    try:
        conn = socket.create_connection((host, int(port)), timeout=1)
    except OSError as err:
        print(f"python {target} {outcome(err)}")
        continue
    took = round((time.monotonic() - start) * 1000)
    conn.settimeout(0.3)
    try:
        conn.sendall(b"hello\n")
        read = "data" if conn.recv(1) else "closed"
    except OSError as err:
        read = outcome(err)
    conn.close()
    print(f"python {target} accepted {took}ms read={read}")
