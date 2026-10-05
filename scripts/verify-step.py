#!/usr/bin/env python3
"""Record one local verification command; never invokes a shell or changes its result."""
import argparse
import datetime
import json
from pathlib import Path
import subprocess
import sys
import time


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--name', required=True)
    parser.add_argument('--cwd', type=Path, default=Path.cwd())
    parser.add_argument('--timeout', type=int, default=1800)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.name.replace('-', '').replace('_', '').isalnum():
        parser.error('name must be alphanumeric with hyphens/underscores')
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('a command is required after --')
    args.output.mkdir(parents=True, exist_ok=True)
    log = args.output / (args.name + '.log')
    result = {'name': args.name, 'command': command, 'cwd': str(args.cwd.resolve()),
              'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'passed': False}
    start = time.monotonic()
    with log.open('w') as stream:
        try:
            run = subprocess.run(command, cwd=args.cwd, stdout=stream, stderr=subprocess.STDOUT, timeout=args.timeout)
            result.update(exit_code=run.returncode, passed=run.returncode == 0)
        except (OSError, subprocess.TimeoutExpired) as error:
            result.update(exit_code=124, error=type(error).__name__)
    result['duration_seconds'] = round(time.monotonic() - start, 3)
    result['log'] = str(log)
    (args.output / (args.name + '.json')).write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False))
    print('\n'.join(log.read_text(errors='replace').splitlines()[-20:]))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
