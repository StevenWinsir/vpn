#!/usr/bin/env python3
"""Exercise a real desktop Core over private IPC, without accounts or VPN setup."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import struct
import subprocess
import tempfile
import time


def read_exact(connection: socket.socket, length: int) -> bytes:
    result = bytearray()
    while len(result) < length:
        chunk = connection.recv(length - len(result))
        if not chunk:
            raise RuntimeError("Core closed IPC before replying")
        result.extend(chunk)
    return bytes(result)


def scenario(binary: Path, termination: str) -> dict:
    with tempfile.TemporaryDirectory(prefix="vpn-core-gate-", dir="/tmp") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir(mode=0o700)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        profile = home / "config.yaml"
        profile.write_text(
            f"mixed-port: {port}\nallow-lan: false\nmode: direct\n"
            "external-controller: ''\ntun:\n  enable: false\n"
            "dns:\n  enable: false\nrules: ['MATCH,DIRECT']\n"
        )
        original = profile.read_bytes()
        address = root / "core.sock"
        process = None
        connection = None
        checks = []
        with socket.socket(socket.AF_UNIX) as server, (root / "core.log").open("wb") as log:
            server.bind(str(address))
            address.chmod(0o600)
            server.listen(1)
            server.settimeout(8)
            try:
                process = subprocess.Popen(
                    [str(binary), str(address)], cwd=root,
                    stdin=subprocess.DEVNULL, stdout=log, stderr=log,
                    start_new_session=True,
                )
                connection, _ = server.accept()
                sequence = 0

                def rpc(method: str, arguments=None) -> dict:
                    nonlocal sequence
                    sequence += 1
                    request_id = str(sequence)
                    data = json.dumps({"id": request_id, "method": method, "arguments": arguments}).encode()
                    connection.sendall(struct.pack("<I", len(data)) + data)
                    deadline = time.monotonic() + 8
                    while time.monotonic() < deadline:
                        connection.settimeout(max(0.01, deadline - time.monotonic()))
                        length = struct.unpack("<I", read_exact(connection, 4))[0]
                        if length > 1 << 20:
                            raise RuntimeError("Unexpected oversized Core response")
                        response = json.loads(read_exact(connection, length))
                        if response.get("id") == request_id:
                            return response
                    raise TimeoutError("Core RPC timed out")

                def require(condition: bool, name: str) -> None:
                    if not condition:
                        raise AssertionError(name)
                    checks.append(name)

                def closed_port() -> bool:
                    with socket.socket() as probe:
                        probe.settimeout(0.5)
                        return probe.connect_ex(("127.0.0.1", port)) != 0

                require(closed_port(), "fixture_port_initially_closed")
                require(rpc("initClash", {"home-dir": str(home), "version": 0}).get("result") is True, "control_core_initialized")
                account = rpc("managedStatus").get("result", {})
                require(account.get("phase") == "signed_out" and account.get("can_connect") is False and account.get("user") is None, "cold_start_signed_out")
                require(not ({"token", "password", "yaml"} & account.keys()), "snapshot_has_no_secrets")
                require(rpc("startListener").get("result") is False, "listener_start_rejected")
                for method, arguments in (
                    ("setupConfig", {"selected-map": {}, "test-url": ""}),
                    ("updateConfig", {"mixed-port": port}),
                ):
                    require(rpc(method, arguments).get("error", {}).get("code") == "managed_connection_required", method + "_rejected")
                require(closed_port(), "old_profile_not_listening")
                require(profile.read_bytes() == original, "old_profile_unchanged")
                reset = rpc("managedReset").get("result", {})
                require(reset.get("can_connect") is False and reset.get("user") is None and reset.get("generation", -1) > account.get("generation", -1), "reset_advances_generation_without_authorization")
                require(rpc("startListener").get("result") is False, "listener_still_rejected_after_reset")
                if termination == "ipc_eof":
                    connection.close()
                    connection = None
                else:
                    process.terminate()
                require(process.wait(timeout=8) == 0, "owned_process_exited_cleanly")
                require(closed_port(), "no_listener_after_exit")
            finally:
                if connection is not None:
                    connection.close()
                if process is not None and process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
        output = (root / "core.log").read_text(errors="replace")
        if "panic:" in output or "cleanup did not finish" in output:
            raise AssertionError("Core emitted a panic or cleanup timeout")
        return {"termination": termination, "passed": True, "checks": checks}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--result", type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    if os.geteuid() == 0 or binary.stat().st_mode & (stat.S_ISUID | stat.S_ISGID):
        raise SystemExit("Run a non-privileged test Core as the current user")
    report = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "scope": "real Core IPC only; no Flutter App, HTTP login, system proxy or TUN", "scenarios": []}
    try:
        for mode in ("ipc_eof", "sigterm"):
            report["scenarios"].append(scenario(binary, mode))
        report["passed"] = True
    except Exception as error:
        report.update(passed=False, error=f"{type(error).__name__}: {error}")
        raise
    finally:
        args.result.parent.mkdir(parents=True, exist_ok=True)
        args.result.write_text(json.dumps(report, indent=2) + "\n")
    print("PASS: real Core cold gate, seeded old profile, IPC EOF and SIGTERM cleanup (2 scenarios)")


if __name__ == "__main__":
    main()
