#!/usr/bin/env bash
set -euo pipefail
if ! command -v node >/dev/null 2>&1; then
 export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
 if [ -s "$NVM_DIR/nvm.sh" ]; then
  set +u
  . "$NVM_DIR/nvm.sh" --no-use
  nvm use --silent default >/dev/null
  set -u
 fi
fi
if ! command -v node >/dev/null 2>&1; then
 echo 'Node.js 22+ is required. Install it or run nvm use before starting.' >&2
 exit 1
fi
exec "$@"
