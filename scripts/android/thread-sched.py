#!/usr/bin/env python3
"""Android scheduling check for the connected app (handoff_fix.md 6.2, R3-06).

Reads, from outside and over adb only, how the system schedules the AsterLink process while it
is connected: every thread's nice value, priority and policy group (toybox ps -T), the process's
cgroups, its oom_score_adj and the screen state. It changes nothing on the device and needs no
root and no debug build, so it works on a customer's phone as well as on the emulator.

    python3 scripts/android/thread-sched.py [--serial <id>] [--samples 1] [--interval 30] [--out report.json]

Connect in the app first, then put another app in front (YouTube) or turn the screen off, and
run it. The verdict follows R3-06: FAIL when any thread of the process that is not one of ART's
own housekeeping threads has a nice value above 0, or when the process sits in a background
group (policy "bg"/cpuset "background"); PASS otherwise; BLOCKED when no device or no process.
ART runs its heap and finalizer daemons below normal on purpose; they are listed, not judged.
On the emulator the scheduling groups are not those of a phone vendor's system: say so in the
record (the report carries ro.product.model and ro.build.version.release).
"""
import argparse
import json
import re
import signal
import subprocess
import sys
import time

PACKAGE = 'com.asterlink.vpn'
# ART / framework housekeeping threads that Android itself runs below normal priority.
ART_BACKGROUND = re.compile(r'^(HeapTaskDaemon|FinalizerDaemon|FinalizerWatchd|ReferenceQueueD|Jit thread pool|Profile Saver|'
                            r'Signal Catcher|perfetto_hprof_|ADB-JDWP Connec|binder:.*|hwuiTask\d*|RenderThread)$')


def adb(serial, *args, check=False):
    command = ['adb'] + (['-s', serial] if serial else []) + list(args)
    result = subprocess.run(command, capture_output=True, text=True)
    if check and result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or 'adb failed')
    return result.stdout


def shell(serial, command):
    return adb(serial, 'shell', command)


def parse_threads(text):
    """toybox `ps -T -o TID,NI,PRI,PCY,CMD`: one row per thread; CMD is the thread name."""
    threads = []
    for line in text.splitlines()[1:]:
        parts = line.split(None, 4)
        if len(parts) < 4 or not parts[0].isdigit():
            continue
        name = parts[4].strip() if len(parts) > 4 else ''
        threads.append({'tid': int(parts[0]), 'nice': int(parts[1]), 'pri': int(parts[2]), 'policy': parts[3], 'name': name})
    return threads


def judge_sample(threads, cgroup, pid):
    problems = []
    listed = []
    for t in threads:
        if t['nice'] <= 0:
            continue
        if ART_BACKGROUND.match(t['name']):
            listed.append('%s(nice %d)' % (t['name'], t['nice']))
        else:
            problems.append('thread %s tid %d nice %d policy %s' % (t['name'] or '?', t['tid'], t['nice'], t['policy']))
    main_thread = [t for t in threads if t['tid'] == pid]
    if main_thread and main_thread[0]['policy'] == 'bg':
        problems.append('main thread in policy group bg')
    if re.search(r'cpuset:/background\b|cpuset:/restricted\b', cgroup):
        problems.append('process in cpuset ' + re.search(r'cpuset:(\S+)', cgroup).group(1))
    return problems, listed


def sample(serial, package):
    pid = shell(serial, 'pidof %s' % package).split()
    if not pid:
        return None
    pid = pid[0]
    threads = parse_threads(shell(serial, 'ps -T -p %s -o TID,NI,PRI,PCY,CMD' % pid))
    cgroup = shell(serial, 'cat /proc/%s/cgroup' % pid).strip()
    oom = shell(serial, 'cat /proc/%s/oom_score_adj' % pid).strip()
    wake = re.search(r'mWakefulness=(\w+)', shell(serial, 'dumpsys power'))
    top = re.search(r'mResumedActivity: ActivityRecord\{\S+ \S+ (\S+)', shell(serial, 'dumpsys activity activities'))
    # `ip link` is refused to the shell user on recent Android; /sys/class/net is readable. tunl0 is the IP-in-IP device.
    vpn = any(re.fullmatch(r'tun\d+', name) for name in shell(serial, 'ls /sys/class/net').split())
    problems, listed = judge_sample(threads, cgroup, int(pid))
    by_policy = {}
    for t in threads:
        key = '%s/nice %d' % (t['policy'], t['nice'])
        by_policy[key] = by_policy.get(key, 0) + 1
    return {'at': time.strftime('%Y-%m-%dT%H:%M:%S'), 'pid': int(pid), 'threads': len(threads), 'groups': by_policy,
            'cgroup': cgroup.replace('\n', ' '), 'oom_score_adj': oom, 'screen': wake.group(1) if wake else '',
            'foreground': top.group(1) if top else '', 'tun_up': vpn, 'problems': problems, 'art_background': listed,
            'thread_list': threads}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--serial')
    parser.add_argument('--package', default=PACKAGE)
    parser.add_argument('--samples', type=int, default=1)
    parser.add_argument('--interval', type=float, default=30)
    parser.add_argument('--duration', type=float, default=0,
                        help='sample for this many seconds instead of --samples times, waiting for the process when it is absent')
    parser.add_argument('--only-connected', action='store_true', help='judge only samples taken while the VPN interface was up')
    parser.add_argument('--label', default='')
    parser.add_argument('--out')
    args = parser.parse_args()
    if not adb(args.serial, 'get-state').strip() == 'device':
        print('BLOCKED  no device (adb get-state)')
        return 3
    device = {key: shell(args.serial, 'getprop ' + prop).strip() for key, prop in
              (('model', 'ro.product.model'), ('release', 'ro.build.version.release'), ('sdk', 'ro.build.version.sdk'),
               ('emulator', 'ro.kernel.qemu'), ('fingerprint', 'ro.build.fingerprint'))}
    samples = []
    absent = 0
    deadline = time.monotonic() + args.duration if args.duration else None
    i = 0

    def stop(*_):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, stop)  # a driver that ends the run early still gets the report
    try:
        while (deadline is None and i < args.samples) or (deadline is not None and time.monotonic() < deadline):
            if i:
                time.sleep(args.interval)
            i += 1
            s = sample(args.serial, args.package)
            if s is None:
                absent += 1
                if deadline is None:
                    print('BLOCKED  %s is not running' % args.package)
                    break
                continue  # --duration: the app may not be started yet, or be between two test cases
            samples.append(s)
            report_line(s)
    except KeyboardInterrupt:
        pass
    return finish(args, device, samples, absent)


def report_line(s):
    print('%s pid %d threads %d screen %s foreground %s tun %s groups %s cgroup %s oom %s%s' % (
            s['at'], s['pid'], s['threads'], s['screen'], s['foreground'], 'up' if s['tun_up'] else 'DOWN',
            s['groups'], s['cgroup'], s['oom_score_adj'], ('  PROBLEMS: ' + '; '.join(s['problems'])) if s['problems'] else ''), flush=True)


def finish(args, device, samples, absent):
    judged = [s for s in samples if s['tun_up'] or not args.only_connected]
    status = 'BLOCKED' if not judged else ('FAIL' if any(s['problems'] for s in judged) else 'PASS')
    summary = {'samples': len(samples), 'judged': len(judged), 'absent': absent,
               'connected_screen_off': sum(1 for s in judged if s['tun_up'] and s['screen'] != 'Awake'),
               'connected_app_not_in_front': sum(1 for s in judged if s['tun_up'] and args.package not in s['foreground']),
               'max_nice_non_art': max([t['nice'] for s in judged for t in s['thread_list'] if not ART_BACKGROUND.match(t['name'])] or [0]),
               'cgroups': sorted({s['cgroup'] for s in judged}), 'oom_score_adj': sorted({s['oom_score_adj'] for s in judged})}
    print('summary %s' % json.dumps(summary, ensure_ascii=False))
    report = {'label': args.label, 'device': device, 'status': status, 'summary': summary, 'samples': samples}
    if args.out:
        with open(args.out, 'w') as handle:
            json.dump(report, handle, indent=2, ensure_ascii=False)
    note = ' (emulator: its scheduling groups are not a vendor phone\'s)' if device.get('emulator') == '1' else ''
    print('%s  %s Android %s%s' % (status, device.get('model'), device.get('release'), note))
    return {'PASS': 0, 'FAIL': 1}.get(status, 3)


if __name__ == '__main__':
    sys.exit(main())
