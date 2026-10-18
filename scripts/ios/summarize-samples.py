#!/usr/bin/env python3
"""Summarize the tunnel extension's resource samples from a device log (handoff_app_ios.md 8.7, round 5).

The extension writes one line a minute (ResourceSample.logLine in AsterLinkKit/Lifecycle.swift):

    sample t=600 footprint=33554432 peak=37748736 fds=41 threads=19 goroutines=88 heap=12582912 sys=26214400 gc=140

plus event lines ("gate closed ...", "gate reopened ...", "foreground lease expired", "stop: reason ...",
"sleep", "wake"). Collect them on the Mac with the iPhone connected:

    xcrun devicectl device info ... / Console.app / `log collect --device-udid <udid> --last 30m`
    log show <collected .logarchive> --predicate 'subsystem == "asterlink.tunnel"' --style compact > tunnel.log

and run:   python3 scripts/ios/summarize-samples.py tunnel.log [--hard-limit-mib 44]

Numbers only, no account data: the extension never logs tokens, lines or traffic figures.
Exit codes: 0 = within the limits, 1 = a limit was crossed, 2 = no samples found (nothing proven).
"""
import argparse
import json
import re
import sys

FIELDS = ('t', 'footprint', 'peak', 'fds', 'threads', 'goroutines', 'heap', 'sys', 'gc')
EVENTS = {
    'gate_closed': re.compile(r'gate closed reason=(\S+)'),
    'gate_reopened': re.compile(r'gate reopened reason=(\S+) outcome=(\S+) closed_ms=(\d+) attempts=(\d+) total=(\d+)'),
    'lease_expired': re.compile(r'foreground lease expired'),
    'sleep': re.compile(r'\bsleep$'),
    'wake': re.compile(r'\bwake$'),
    'stop': re.compile(r'stop: reason (\S+)'),
    'memory_pressure': re.compile(r'memory pressure: (\d+) MiB'),
}


def parse_sample(line):
    start = line.find('sample ')
    if start < 0:
        return None
    pairs = dict(re.findall(r'(\w+)=(\d+)', line[start:]))
    if any(field not in pairs for field in FIELDS):
        return None
    return {field: int(pairs[field]) for field in FIELDS}


def summarize(lines, hard_limit_mib=44, fd_growth=8, goroutine_growth=20):
    samples = [s for s in map(parse_sample, lines) if s]
    events = {name: [] for name in EVENTS}
    for line in lines:
        text = line.rstrip()
        for name, pattern in EVENTS.items():
            match = pattern.search(text)
            if match:
                events[name].append(match.groups() or True)
    if not samples:
        return {'samples': 0}, 2
    # Growth is judged from the second sample on: the first minute still includes start-up allocations.
    base = samples[1] if len(samples) > 1 else samples[0]
    last = samples[-1]
    peak = max(max(s['peak'], s['footprint']) for s in samples)
    problems = []
    if peak >= hard_limit_mib << 20:
        problems.append('footprint peak %.1f MiB is at or above the %d MiB hard limit' % (peak / 1048576, hard_limit_mib))
    if last['fds'] - base['fds'] > fd_growth:
        problems.append('open files grew %d -> %d' % (base['fds'], last['fds']))
    if last['goroutines'] - base['goroutines'] > goroutine_growth:
        problems.append('goroutines grew %d -> %d' % (base['goroutines'], last['goroutines']))
    closed_ms = [int(item[2]) for item in events['gate_reopened']]
    report = {
        'samples': len(samples), 'span_seconds': last['t'] - samples[0]['t'],
        'footprint_peak_mib': round(peak / 1048576, 1), 'footprint_last_mib': round(last['footprint'] / 1048576, 1),
        'fds': {'first': base['fds'], 'last': last['fds'], 'max': max(s['fds'] for s in samples)},
        'threads_max': max(s['threads'] for s in samples),
        'goroutines': {'first': base['goroutines'], 'last': last['goroutines'], 'max': max(s['goroutines'] for s in samples)},
        'go_heap_max_mib': round(max(s['heap'] for s in samples) / 1048576, 1),
        'gate': {'closures': len(events['gate_reopened']), 'longest_closed_ms': max(closed_ms, default=0),
                 'reasons': sorted({item[0] for item in events['gate_reopened']})},
        'lease_expired': len(events['lease_expired']), 'sleep': len(events['sleep']), 'wake': len(events['wake']),
        'stops': [item[0] if isinstance(item, tuple) else item for item in events['stop']],
        'memory_pressure_events': len(events['memory_pressure']),
        'problems': problems,
    }
    return report, 1 if problems else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('log', help='a text log with the extension\'s lines (- for stdin)')
    parser.add_argument('--hard-limit-mib', type=int, default=44)
    args = parser.parse_args()
    text = sys.stdin.read() if args.log == '-' else open(args.log, encoding='utf-8', errors='replace').read()
    report, code = summarize(text.splitlines(), args.hard_limit_mib)
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if code == 2:
        print('no "sample" lines found: nothing was measured', file=sys.stderr)
    return code


if __name__ == '__main__':
    sys.exit(main())
