#!/usr/bin/env bash
# AsterLink Android lifecycle helpers (handoff_app_android.md round 5, docs/android-lifecycle.md).
#
# The automated acceptance is `python3 scripts/test-native-android.py --suite lifecycle`. This
# script covers what needs a person or many hours, on an emulator or a USB-debugging phone that
# already runs a connected AsterLink:
#
#   lifecycle.sh soak [--hours H] [--every SECONDS] [--out DIR]
#       Samples the app process every SECONDS (default 60) for H hours (default 8): RSS (PSS
#       from dumpsys meminfo), CPU % (top), threads, open descriptors, whether the VPN network is
#       up, and the battery level; dumpsys batterystats for the app at the start and the end.
#       Writes DIR/soak.csv (default artifacts/native-android/soak-<timestamp>/). Read-only: it
#       never changes the device. Descriptor counts need a debuggable build (run-as).
#   lifecycle.sh screen-off MINUTES
#       Turns the screen off for MINUTES and back on (L01 by hand), then prints the VPN state.
#   lifecycle.sh doze-in | doze-out
#       Forces deep Doze (screen off, unplugged) and leaves it again (L02 by hand).
#   lifecycle.sh wifi-off | wifi-on | airplane-on | airplane-off
#       The network changes of L03, one at a time.
#   lifecycle.sh state
#       The VPN network, the app process and its service, as the system reports them.
#
# Every command acts on the device selected by ANDROID_SERIAL (or the only one attached) and
# prints the adb commands it runs. Nothing here touches this Mac's own network.
set -euo pipefail

PACKAGE=com.asterlink.vpn
ADB=${ADB:-adb}
if [[ -n "${ANDROID_HOME:-}" && -x "$ANDROID_HOME/platform-tools/adb" ]]; then ADB="$ANDROID_HOME/platform-tools/adb"; fi

run() { echo "+ adb $*" >&2; "$ADB" "$@"; }
sh_() { "$ADB" shell "$@" 2>/dev/null | tr -d '\r'; }

vpn_up() {
  # Only network agents count (SystemUI's requests mention the VPN transport too).
  if sh_ dumpsys connectivity | grep -Eq '^\s*NetworkAgentInfo\{.*ni\{VPN CONNECTED'; then echo up; else echo down; fi
}

state() {
  local pid
  pid=$(sh_ pidof "$PACKAGE" || true)
  echo "vpn_network=$(vpn_up) pid=${pid:-none}"
  if sh_ dumpsys activity services "$PACKAGE" | grep -q AsterVpnService; then echo "service=AsterVpnService running"; else echo "service=none"; fi
}

soak() {
  local hours=8 every=60 out=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --hours) hours=$2; shift 2 ;;
      --every) every=$2; shift 2 ;;
      --out) out=$2; shift 2 ;;
      *) echo "unknown option $1" >&2; exit 2 ;;
    esac
  done
  out=${out:-artifacts/native-android/soak-$(date +%Y%m%d-%H%M%S)}
  mkdir -p "$out"
  local end=$(( $(date +%s) + $(awk "BEGIN{print int($hours*3600)}") ))
  sh_ dumpsys batterystats --reset >/dev/null || true
  sh_ dumpsys batterystats "$PACKAGE" > "$out/batterystats-start.txt" || true
  echo "time,elapsed_s,pid,pss_kb,cpu_pct,threads,fds,vpn,battery_pct" > "$out/soak.csv"
  local start
  start=$(date +%s)
  echo "sampling every ${every}s for ${hours}h into $out/soak.csv (Ctrl-C ends early; the CSV stays valid)" >&2
  while [[ $(date +%s) -lt $end ]]; do
    local pid pss cpu threads fds vpn battery
    pid=$(sh_ pidof "$PACKAGE" || true)
    if [[ -n "$pid" ]]; then
      pss=$(sh_ dumpsys meminfo "$PACKAGE" | awk '/TOTAL PSS:/{print $3; exit} /^ *TOTAL /{print $2; exit}')
      cpu=$(sh_ top -b -n 1 -p "$pid" | awk -v p="$pid" '$1==p{print $9; exit}')
      threads=$(sh_ cat "/proc/$pid/status" | awk '/^Threads:/{print $2}')
      fds=$(sh_ run-as "$PACKAGE" ls "/proc/$pid/fd" | wc -l | tr -d ' ')
    else
      pss="" cpu="" threads="" fds=""
    fi
    vpn=$(vpn_up)
    battery=$(sh_ dumpsys battery | awk '/level:/{print $2; exit}')
    echo "$(date -u +%FT%TZ),$(( $(date +%s) - start )),${pid:-},${pss:-},${cpu:-},${threads:-},${fds:-},$vpn,${battery:-}" >> "$out/soak.csv"
    sleep "$every"
  done
  sh_ dumpsys batterystats "$PACKAGE" > "$out/batterystats-end.txt" || true
  echo "done: $out/soak.csv ($(($(wc -l < "$out/soak.csv") - 1)) samples)" >&2
}

case "${1:-}" in
  soak) shift; soak "$@" ;;
  screen-off)
    minutes=${2:?minutes}
    run shell input keyevent KEYCODE_SLEEP
    sleep $(( minutes * 60 ))
    run shell input keyevent KEYCODE_WAKEUP
    run shell wm dismiss-keyguard
    sleep 5
    state ;;
  doze-in)
    run shell dumpsys deviceidle enable deep
    run shell dumpsys battery unplug
    run shell input keyevent KEYCODE_SLEEP
    run shell dumpsys deviceidle force-idle deep
    run shell dumpsys deviceidle get deep ;;
  doze-out)
    run shell dumpsys deviceidle unforce
    run shell dumpsys battery reset
    run shell input keyevent KEYCODE_WAKEUP
    run shell wm dismiss-keyguard ;;
  wifi-off) run shell svc wifi disable ;;
  wifi-on) run shell svc wifi enable ;;
  airplane-on) run shell cmd connectivity airplane-mode enable ;;
  airplane-off) run shell cmd connectivity airplane-mode disable ;;
  state) state ;;
  *) sed -n '2,30p' "$0"; exit 2 ;;
esac
