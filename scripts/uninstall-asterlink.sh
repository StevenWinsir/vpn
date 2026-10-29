#!/bin/bash
# 卸载 AsterLink：只删除 AsterLink 自己的应用、设置与后台服务，不影响其他应用。
#
#   直接打开：只列出将要删除的内容，不做任何改动
#   加 --apply 运行：执行卸载（删除系统级内容时需要输入本机登录密码）
#
# 卸载前请先从菜单栏退出 AsterLink。
set -euo pipefail

APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1
SYSROOT="${ASTERLINK_UNINSTALL_ROOT:-}"
APP_ID="com.asterlink.vpn"
HELPER_LABEL="com.asterlink.vpn.helper"

user_paths=(
  "$HOME/Library/Preferences/$APP_ID.plist"
  "$HOME/Library/Caches/$APP_ID"
  "$HOME/Library/HTTPStorages/$APP_ID"
  "$HOME/Library/Saved Application State/$APP_ID.savedState"
)
dns_record="$SYSROOT/Library/Application Support/AsterLink/dns-override.json"
core_bin="$SYSROOT/Applications/AsterLink.app/Contents/Resources/asterlink-core"
system_paths=(
  "$SYSROOT/Applications/AsterLink.app"
  "$SYSROOT/Library/Application Support/AsterLink"
)

if pgrep -x AsterLink >/dev/null 2>&1 || pgrep -x asterlink-core >/dev/null 2>&1; then
  echo "AsterLink 仍在运行。请先点击屏幕右上角菜单栏中的 AsterLink 图标，选择“退出”，然后再试。" >&2
  exit 2
fi

echo "卸载 AsterLink 将删除以下内容（不会影响其他应用）："
for p in "${user_paths[@]}" "${system_paths[@]}"; do
  [[ -e "$p" ]] && echo "  · $p"
done
echo "  · 钥匙串中保存的 AsterLink 登录密码与设备标识"
echo "  · AsterLink 后台服务（如仍在运行）"
[[ -e "$dns_record" ]] && echo "  · 恢复 AsterLink 修改过的系统 DNS 设置"

if [[ $APPLY -ne 1 ]]; then
  echo ""
  echo "以上只是检查，尚未删除任何内容。"
  echo "确认卸载：打开“终端”，输入下面这一行后按回车（过程中需要输入本机登录密码）："
  echo "  \"$0\" --apply"
  exit 0
fi

for p in "${user_paths[@]}"; do rm -rf -- "$p"; done

if [[ -z "$SYSROOT" ]] && launchctl print "system/${HELPER_LABEL}" >/dev/null 2>&1; then
  echo "正在移除 AsterLink 后台服务（需要输入本机登录密码）"
  sudo launchctl bootout "system/${HELPER_LABEL}" || echo "  未能移除后台服务。请在“系统设置 › 通用 › 登录项与扩展”中关闭 AsterLink。" >&2
fi

if [[ -z "$SYSROOT" && -e "$dns_record" ]]; then
  echo "正在恢复系统 DNS 设置（需要输入本机登录密码）"
  if [[ -x "$core_bin" ]]; then
    sudo "$core_bin" --recover-network || echo "  未能自动恢复。请在“系统设置 › 网络”中当前网络的“详细信息 › DNS”里删除 198.18.0.2。" >&2
  else
    echo "  未能自动恢复。请在“系统设置 › 网络”中当前网络的“详细信息 › DNS”里删除 198.18.0.2。" >&2
  fi
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
echo "AsterLink 已卸载。其他应用与系统网络设置未受影响。"
