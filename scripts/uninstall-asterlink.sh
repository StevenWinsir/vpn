#!/bin/bash
# Remove AsterLink (and only AsterLink) from this Mac.
#
#   ./uninstall-asterlink.sh            # dry run: prints what would be removed
#   ./uninstall-asterlink.sh --apply    # removes it; asks for sudo only for the system-level items
#
# Never touches other VPNs, routes, DNS, or network settings. Quit AsterLink first (the app's
# Settings -> "移除特权服务" is the cleanest way to unregister the privileged service).
set -euo pipefail

APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1
# Test hook: prefix for the absolute system paths so the script can be exercised in a sandbox.
SYSROOT="${ASTERLINK_UNINSTALL_ROOT:-}"
APP_ID="com.asterlink.vpn"
HELPER_LABEL="com.asterlink.vpn.helper"

user_paths=(
  "$HOME/Library/Preferences/$APP_ID.plist"
  "$HOME/Library/Caches/$APP_ID"
  "$HOME/Library/HTTPStorages/$APP_ID"
  "$HOME/Library/Saved Application State/$APP_ID.savedState"
)
system_paths=(
  "$SYSROOT/Applications/AsterLink.app"
  "$SYSROOT/Library/Application Support/AsterLink"
)

if pgrep -x AsterLink >/dev/null 2>&1 || pgrep -x asterlink-core >/dev/null 2>&1; then
  echo "AsterLink 仍在运行。请先在 App 中断开连接并退出（菜单“退出”），再重试。" >&2
  exit 2
fi

echo "将处理（仅限 AsterLink 自身的资源）："
for p in "${user_paths[@]}" "${system_paths[@]}"; do
  [[ -e "$p" ]] && echo "  存在  $p" || echo "  不存在 $p"
done
echo "  钥匙串条目  ${APP_ID}.credentials、${APP_ID}.device（记住的密码与设备 ID）"
echo "  launchd 服务 system/${HELPER_LABEL}（如仍已注册）"

if [[ $APPLY -ne 1 ]]; then
  echo "（这是预演，未做任何改动；加 --apply 才会执行。）"
  exit 0
fi

for p in "${user_paths[@]}"; do rm -rf -- "$p"; done

if [[ -z "$SYSROOT" ]] && launchctl print "system/${HELPER_LABEL}" >/dev/null 2>&1; then
  echo "注销特权服务 ${HELPER_LABEL}（需要管理员密码）"
  sudo launchctl bootout "system/${HELPER_LABEL}" || echo "  bootout 失败；请在 App 设置中点击“移除特权服务”或在系统设置的登录项中关闭" >&2
fi

for p in "${system_paths[@]}"; do
  [[ -e "$p" ]] || continue
  if [[ -n "$SYSROOT" ]]; then rm -rf -- "$p"; else sudo rm -rf -- "$p"; fi
done

if [[ "${ASTERLINK_UNINSTALL_SKIP_KEYCHAIN:-0}" != "1" ]]; then
  for service in "${APP_ID}.credentials" "${APP_ID}.device"; do
    while security delete-generic-password -s "$service" >/dev/null 2>&1; do :; done
  done
fi
echo "AsterLink 已卸载。其他 VPN 与系统网络设置未改动。"
