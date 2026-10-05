#!/usr/bin/env python3
"""Run Go tests against a disposable, private Unix-socket PostgreSQL cluster."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    os.environ["LC_ALL"] = "C"
    os.environ["LANG"] = "C"
    for binary in ("initdb", "pg_ctl", "go"):
        if shutil.which(binary) is None:
            raise SystemExit(f"Required local tool not found: {binary}")
    with tempfile.TemporaryDirectory(prefix="vpn-pg-", dir="/tmp") as temporary:
        directory = Path(temporary)
        data = directory / "data"
        socket = directory / "sock"
        socket.mkdir(mode=0o700)
        subprocess.run(["initdb", "-D", str(data), "-U", "postgres", "--auth-local=trust", "--auth-host=reject", "--no-locale", "-E", "UTF8"], check=True, stdout=subprocess.DEVNULL)
        started = False
        try:
            startup = subprocess.run(["pg_ctl", "-D", str(data), "-l", str(directory / "postgres.log"), "-o", f"-k {socket} -c listen_addresses='' -c unix_socket_permissions=0700", "-w", "start"])
            if startup.returncode:
                raise SystemExit((directory / "postgres.log").read_text())
            started = True
            environment = dict(os.environ)
            environment.update(NATIVE_TEST_DSN=f"host={socket} user=postgres dbname=postgres sslmode=disable", RUN_DB_TESTS="0", GOPROXY="off", GOTOOLCHAIN="local")
            subprocess.run(["go", "test", "-race", "-count=1", "-v", "./..."], cwd=ROOT / "backend", env=environment, check=True)
        finally:
            if started:
                subprocess.run(["pg_ctl", "-D", str(data), "-m", "fast", "-w", "stop"], check=True)


if __name__ == "__main__":
    main()
