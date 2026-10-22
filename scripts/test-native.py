#!/usr/bin/env python3
"""Run Go tests against a disposable, private Unix-socket PostgreSQL cluster."""
from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile

import prepare_mihomo

ROOT = Path(__file__).resolve().parents[1]
# The last release before node groups and online leases (handoff_node_use_history_and_
# load_balancing_c.md round 7): its Core is what every installed app runs until the new
# Core ships, so TestReleasedCoreAgainstNodeGroupsPostgres drives it against this tree.
RELEASED_CORE_REF = "a63a882ff41d83f04a0527128c96d9b8a9782365"
AUTH_MAIL_PREFIXES = ('SMTP_', 'AUTH_MAIL_', 'REGISTRATION_', 'ALLOW_REGISTRATION', 'WEBSITE_BASE_URL', 'TRUSTED_PROXIES', 'PAYMENT_')


def main() -> None:
    prepare_mihomo.prepare()
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
            # Never inherit real mail or payment settings from the runner (handoff_auth.md 11.1,
            # handoff_payment_account.md 15.1): tests use synthetic values, a Fake Mailer and a fake gateway.
            environment = {key: value for key, value in os.environ.items() if not key.startswith(AUTH_MAIL_PREFIXES)}
            # The real Core binary (built with the test-only fake engine: no root, no routes) is exercised against the real API (TestNativeCoreEndToEnd).
            core_bin = directory / "asterlink-core"
            subprocess.run(["go", "build", "-tags", "fakeengine", "-o", str(core_bin), "./cmd/asterlink-core"], cwd=ROOT / "native/core", check=True, env=dict(os.environ, CGO_ENABLED="0"))
            environment["ASTERLINK_CORE_BIN"] = str(core_bin)
            build_released_core(directory, environment)
            # The real node agent (reference VLESS forwarder) is run against the real API (TestNodeAgentEndToEnd*).
            agent_bin = directory / "asterlink-nodeagent"
            subprocess.run(["go", "build", "-o", str(agent_bin), "./cmd/asterlink-nodeagent"], cwd=ROOT / "native/nodeagent", check=True, env=dict(os.environ, CGO_ENABLED="0"))
            environment["ASTERLINK_NODEAGENT_BIN"] = str(agent_bin)
            # The round-7 acceptance tool and its controlled traffic target run against that same real agent (TestBilltestGrants*).
            for variable, command in (("ASTERLINK_BILLTEST_BIN", "asterlink-billtest"), ("ASTERLINK_TRAFFICSINK_BIN", "asterlink-trafficsink")):
                binary = directory / command
                subprocess.run(["go", "build", "-o", str(binary), f"./cmd/{command}"], cwd=ROOT / "native/core", check=True, env=dict(os.environ, CGO_ENABLED="0"))
                environment[variable] = str(binary)
            environment.update(NATIVE_TEST_DSN=f"host={socket} user=postgres dbname=postgres sslmode=disable", RUN_DB_TESTS="0", GOPROXY="off", GOTOOLCHAIN="local")
            # Optional extra `go test` arguments (e.g. -run TestGrant ./internal/api) narrow the run.
            targets = sys.argv[1:] or ["./..."]
            subprocess.run(["go", "test", "-race", "-count=1", "-v", *targets], cwd=ROOT / "backend", env=environment, check=True)
        finally:
            if started:
                subprocess.run(["pg_ctl", "-D", str(data), "-m", "fast", "-w", "stop"], check=True)


def build_released_core(directory: Path, environment: dict) -> None:
    """Build the released Core (fake engine) from git history next to the pinned Mihomo."""
    present = subprocess.run(["git", "cat-file", "-e", RELEASED_CORE_REF + "^{commit}"], cwd=ROOT, stderr=subprocess.DEVNULL).returncode == 0
    if not present:
        if os.environ.get("REQUIRE_RELEASED_CORE"):
            raise SystemExit(f"released Core commit {RELEASED_CORE_REF} is not in this clone (fetch it first)")
        print(f"note: released Core commit {RELEASED_CORE_REF[:7]} not in this clone; its compatibility test will skip", flush=True)
        return
    tree = directory / "released"
    tree.mkdir()
    archive = subprocess.run(["git", "archive", "--format=tar", RELEASED_CORE_REF, "native/core", "native/nodeagent", "shared"], cwd=ROOT, check=True, capture_output=True).stdout
    subprocess.run(["tar", "-x", "-C", str(tree)], input=archive, check=True)
    # Same submodule commit and lifecycle patch as this tree (unchanged since that release).
    (tree / "native/third_party").symlink_to(ROOT / "native/third_party", target_is_directory=True)
    binary = directory / "asterlink-core-released"
    subprocess.run(["go", "build", "-tags", "fakeengine", "-o", str(binary), "./cmd/asterlink-core"], cwd=tree / "native/core", check=True, env=dict(os.environ, CGO_ENABLED="0"))
    environment["ASTERLINK_RELEASED_CORE_BIN"] = str(binary)


if __name__ == "__main__":
    main()
