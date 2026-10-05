#!/usr/bin/env python3
"""Check the real Android service gate using cached JVM tools, without Gradle or an APK."""
from pathlib import Path
import os
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    libraries = sorted((Path.home() / ".gradle/wrapper/dists/gradle-9.2.1-all").glob("*/gradle-9.2.1/lib"))
    if len(libraries) != 1:
        raise SystemExit("Expected one cached Gradle 9.2.1 distribution; no dependencies will be downloaded")
    library = libraries[0]
    java = Path(os.environ["JAVA_HOME"]) / "bin/java"
    dependencies = [library / name for name in (
        "kotlin-stdlib-2.2.20.jar", "kotlinx-coroutines-core-jvm-1.10.2.jar",
        "gson-2.13.1.jar", "annotations-24.0.1.jar",
    )]
    if not java.is_file() or not all(path.is_file() for path in dependencies):
        raise SystemExit("Required cached Java/Kotlin dependencies are missing")
    classpath = os.pathsep.join(map(str, dependencies))
    source = ROOT / "FlClash/android/app/src/main/kotlin/com/follow/clash/ManagedServiceGate.kt"
    check = ROOT / "FlClash/android/tests/standalone/ManagedServiceGateCheck.kt"
    log = ROOT / "artifacts/p2-p3-20260930/android-gate-standalone.log"
    log.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="vpn-managed-gate-") as temporary, log.open("w") as output:
        target = Path(temporary) / "gate.jar"
        commands = [
            [str(java), "-cp", str(library / "*"), "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
             "-no-stdlib", "-no-reflect", "-jvm-target", "17", "-classpath", classpath,
             "-d", str(target), str(source), str(check)],
            [str(java), "-cp", str(target) + os.pathsep + classpath, "com.follow.clash.ManagedServiceGateCheckKt"],
        ]
        for command in commands:
            result = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT, timeout=45)
            output.flush()
            if result.returncode:
                raise SystemExit(f"Standalone Kotlin check failed ({result.returncode}); see {log}")
    print(log.read_text(), end="")


if __name__ == "__main__":
    main()
