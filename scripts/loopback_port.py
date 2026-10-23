#!/usr/bin/env python3
"""Loopback ports for the test harnesses, for a listener that another process binds later.

Binding port 0 and closing the socket hands back a number from the kernel's ephemeral range, and
the kernel may give that same number to the next bind(0) or outgoing connection on the host (adb,
the emulator, `go test` downloading modules, the backend world's own listeners) before the helper
binds it. Main CI hit exactly that on 2026-10-23: "listen tcp 127.0.0.1:42625: bind: address
already in use". The kernel never hands out a port below the ephemeral range, so once such a port
is checked free (TCP and UDP) only an explicit bind of that same number can take it.

Where a listener can bind port 0 itself and report its address, that is better still: nothing to
race. This module is for the cases that need the number up front (a port baked into an APK, a
config handed to Mihomo, a world that must come back on the same address).
"""
import random
import socket
import sys

# Well above the fixed ports a CI host or the emulator uses (adb 5037, emulator 5554-5585, 8080...).
FLOOR = 20000
_issued = set()
_random = random.SystemRandom()


def ephemeral_start():
    """First port the kernel hands out by itself (Linux reads it; macOS and Windows default 49152)."""
    if sys.platform.startswith('linux'):
        try:
            with open('/proc/sys/net/ipv4/ip_local_port_range') as f:
                return int(f.read().split()[0])
        except (OSError, ValueError, IndexError):
            return 32768
    return 49152


def _bindable(host, port):
    for kind in (socket.SOCK_STREAM, socket.SOCK_DGRAM):
        with socket.socket(socket.AF_INET, kind) as s:
            try:
                s.bind((host, port))
            except OSError:
                return False
    return True


def free_port(host='127.0.0.1'):
    """A port free for TCP and UDP on host, below the ephemeral range, never the same one twice."""
    top = ephemeral_start()
    if top - FLOOR >= 1000:
        for _ in range(500):
            port = _random.randrange(FLOOR, top)
            if port not in _issued and _bindable(host, port):
                _issued.add(port)
                return port
    # No room below the ephemeral range on this host: the kernel's choice, as before.
    with socket.socket() as s:
        s.bind((host, 0))
        return s.getsockname()[1]
