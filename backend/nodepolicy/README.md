# Generated standalone dependency

Do not edit this directory independently. Its Go source, tests and fixtures are
byte-identical snapshots of FlClash/core/nodepolicy. From the repository root:

    python3 scripts/sync-backend-shared.py --write
    python3 scripts/sync-backend-shared.py --check

Commit the regenerated files together with canonical changes. CI rejects drift,
missing files, unexpected files and symlinks. The pinned-schema compatibility
test remains in the canonical Core module because it needs Mihomo source.
Servers need only the complete backend directory; Python, Flutter and Mihomo
source are not required to build the API. See ../DEPLOYMENT.md.
