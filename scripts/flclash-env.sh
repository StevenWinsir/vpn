#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# -eq 0 ]]; then
  echo "Usage: bash scripts/flclash-env.sh <command> [arguments...]" >&2
  exit 64
fi

export PATH="$HOME/.cargo/bin:/opt/homebrew/bin:$PATH"
if [[ -n "${FLUTTER_ROOT:-}" ]]; then
  export PATH="$FLUTTER_ROOT/bin:$PATH"
elif ! command -v flutter >/dev/null 2>&1; then
  for candidate in "$HOME/development/flutter" /opt/homebrew/share/flutter; do
    if [[ -x "$candidate/bin/flutter" ]]; then
      export FLUTTER_ROOT="$candidate"
      export PATH="$candidate/bin:$PATH"
      break
    fi
  done
fi

if [[ -z "${JAVA_HOME:-}" && -d /opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home ]]; then
  export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
fi
if [[ -n "${JAVA_HOME:-}" ]]; then
  export PATH="$JAVA_HOME/bin:$PATH"
fi

sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [[ -z "$sdk" ]]; then
  for candidate in "$HOME/Library/Android/sdk" /opt/homebrew/share/android-commandlinetools; do
    if [[ -d "$candidate/platforms" ]]; then
      sdk="$candidate"
      break
    fi
  done
fi
if [[ -n "$sdk" ]]; then
  export ANDROID_HOME="$sdk" ANDROID_SDK_ROOT="$sdk"
  export PATH="$sdk/platform-tools:$sdk/cmdline-tools/latest/bin:$PATH"
fi

rust_channel="$(sed -nE 's/^channel = "([^"]+)"$/\1/p' "$root/FlClash/plugins/rust_api/rust/rust-toolchain.toml")"
if [[ -z "$rust_channel" ]]; then
  echo "Missing pinned Rust channel" >&2
  exit 65
fi
export RUSTUP_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-$rust_channel}"
if [[ -z "${DEVELOPER_DIR:-}" && -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild ]]; then
  selected_developer="$(xcode-select -p 2>/dev/null || true)"
  if [[ -z "$selected_developer" || "$selected_developer" == /Library/Developer/CommandLineTools ]]; then
    export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
  fi
fi
cd "$root/FlClash"
exec "$@"
