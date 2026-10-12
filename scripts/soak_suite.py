"""`test-native-macos.py --suite soak`: record CPU/memory of a RUNNING AsterLink for a while.

This never starts, connects or changes anything: the tester connects through the app, then
runs the suite, which samples `ps` for the three product processes and reports measured
numbers. There is no built-in "good" figure; the only pass/fail rule is the explicit
`--max-rss-growth-pct` (resident memory of the last quarter vs the first quarter), because a
steadily growing Core is the failure this guards against. Without a running app it is BLOCKED.
"""
import json
import statistics
import subprocess
import time

PROCESSES = {'AsterLink': 'AsterLink', 'AsterLinkHelper': 'AsterLinkHelper', 'asterlink-core': 'asterlink-core'}


def pids(name):
    result = subprocess.run(['pgrep', '-x', name], capture_output=True, text=True)
    return [int(p) for p in result.stdout.split()]


def sample(pid):
    result = subprocess.run(['ps', '-o', 'rss=,%cpu=', '-p', str(pid)], capture_output=True, text=True)
    fields = result.stdout.split()
    return (int(fields[0]), float(fields[1])) if len(fields) == 2 else None


def run(ctx, minutes, interval, max_growth_pct, output_dir):
    record = ctx['record']
    running = {label: pids(proc) for label, proc in PROCESSES.items()}
    if not running['AsterLink'] or not running['asterlink-core']:
        record('SOAK-01', 'AsterLink is running and connected', 'AsterLink and/or asterlink-core not running: start the app, connect, then rerun', 'blocked')
        return
    series = {label: [] for label in PROCESSES}
    deadline = time.monotonic() + minutes * 60
    while True:
        for label, proc in PROCESSES.items():
            for pid in pids(proc):
                value = sample(pid)
                if value:
                    series[label].append(value)
        if time.monotonic() >= deadline:
            break
        time.sleep(interval)
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / 'soak-samples.json').write_text(json.dumps(series))
    for label, values in series.items():
        if len(values) < 8:
            record('SOAK-' + label, 'at least 8 samples', '%d samples (process absent or run too short)' % len(values), 'blocked')
            continue
        rss = [v[0] for v in values]
        cpu = [v[1] for v in values]
        quarter = max(1, len(rss) // 4)
        first, last = statistics.mean(rss[:quarter]), statistics.mean(rss[-quarter:])
        growth = (last - first) / first * 100 if first else 0.0
        observed = 'n=%d rss_kb min/mean/max=%d/%d/%d growth=%.1f%% cpu%% mean/max=%.1f/%.1f' % (
            len(values), min(rss), statistics.mean(rss), max(rss), growth, statistics.mean(cpu), max(cpu))
        record('SOAK-' + label, 'resident memory growth <= %.0f%% over the window' % max_growth_pct, observed,
               'fail' if growth > max_growth_pct else 'pass', str(output_dir / 'soak-samples.json'))
