#!/usr/bin/env python3
"""nginx -t on every Nginx example of docs/deployment.md (handoff_seo.md S4-04).

Each fenced ```nginx block is checked. A block with its own server blocks (the domain redirects
and HTTPS of the search engine launch) goes into the http context as it is, with its certificate
paths pointed at a throw-away self-signed pair; a block of location directives is wrapped in a
server block of its own. Nothing binds a port: nginx -t only parses and loads the configuration.

The launch block must also keep its promises: http and the bare domain answer 301 to
https://www.asterlink.vip with the path, www and api send HSTS for a year without preload, and the
website keeps the Host header. A deliberately broken copy must fail, so a check that passes
everything cannot go unnoticed.

Exit status: 0 checked, 1 a failure, 2 nginx is not installed here (BLOCKED, never a pass).
"""
from pathlib import Path
import os
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
DOC = ROOT / 'docs' / 'deployment.md'
CANONICAL = 'https://www.asterlink.vip$request_uri'
HSTS = 'add_header Strict-Transport-Security "max-age=31536000" always;'


def blocks(text):
    return re.findall(r'```nginx\n(.*?)```', text, flags=re.S)


def servers(block):
    """The top-level server blocks of a snippet, by brace depth."""
    out, depth, start = [], 0, None
    for match in re.finditer(r'server\s*\{|\{|\}', block):
        token = match.group(0)
        if token.startswith('server') and depth == 0:
            start = match.start()
            depth = 1
        elif token == '{' or token.startswith('server'):
            depth += 1
        else:
            depth -= 1
            if depth == 0 and start is not None:
                out.append(block[start:match.end()])
                start = None
    return out


def promises(block):
    problems = []
    found = servers(block)
    names = {s: re.search(r'server_name\s+([^;]+);', s).group(1).split() for s in found}
    def having(host, port):
        return [s for s in found if host in names[s] and re.search(rf'listen\s+(\[::\]:)?{port}\b', s)]
    for host in ('asterlink.vip', 'www.asterlink.vip'):
        for s in having(host, 80) or [None]:
            if s is None or f'return 301 {CANONICAL};' not in s:
                problems.append(f'http://{host} must answer 301 {CANONICAL}')
    for s in having('asterlink.vip', 443) or [None]:
        if s is None or f'return 301 {CANONICAL};' not in s:
            problems.append(f'https://asterlink.vip must answer 301 {CANONICAL}')
    for host in ('www.asterlink.vip', 'api.asterlink.vip'):
        for s in having(host, 443) or [None]:
            if s is None or HSTS not in s:
                problems.append(f'{host} must send {HSTS}')
    if 'preload' in block:
        problems.append('preload is the operator\'s decision, not a default (handoff_seo.md 7.2)')
    www = having('www.asterlink.vip', 443)
    if www and 'proxy_set_header Host $http_host;' not in www[0]:
        problems.append('www must pass the browser Host header (handoff_seo.md D12)')
    return problems


def configuration(snippets, work, cert, key):
    parts = []
    for index, block in enumerate(snippets):
        block = re.sub(r'ssl_certificate\s+[^;]+;', f'ssl_certificate {cert};', block)
        block = re.sub(r'ssl_certificate_key\s+[^;]+;', f'ssl_certificate_key {key};', block)
        if servers(block):
            parts.append(block)
        else:
            parts.append(f'server {{\n listen 127.0.0.1:{18080 + index};\n server_name check-{index}.invalid;\n{block}}}\n')
    temp = '\n'.join(f'  {name}_temp_path {work}/{name};' for name in ('client_body', 'proxy', 'fastcgi', 'uwsgi', 'scgi'))
    return (f'pid {work}/nginx.pid;\nerror_log {work}/error.log;\nevents {{}}\nhttp {{\n  access_log off;\n{temp}\n'
            + '\n'.join(parts) + '\n}\n')


def nginx_test(nginx, work, text):
    conf = work / 'nginx.conf'
    conf.write_text(text)
    return subprocess.run([nginx, '-t', '-p', str(work), '-c', str(conf), '-e', str(work / 'error.log')],
                          capture_output=True, text=True)


def main():
    snippets = blocks(DOC.read_text(encoding='utf-8'))
    launch = [b for b in snippets if servers(b)]
    if len(snippets) < 4 or len(launch) != 1:
        print(f'FAIL: expected the location examples and one launch block, found {len(snippets)} and {len(launch)}')
        return 1
    problems = promises(launch[0])
    if problems:
        print('FAIL: ' + '\n      '.join(problems))
        return 1
    nginx = shutil.which('nginx') or next((p for p in ('/usr/sbin/nginx', '/opt/homebrew/bin/nginx') if os.path.exists(p)), None)
    if not nginx:
        print('BLOCKED: nginx is not installed here; the promises of the launch block were checked, nginx -t was not run')
        return 2
    with tempfile.TemporaryDirectory(prefix='nginx-docs-') as tmp:
        work = Path(tmp)
        cert, key = work / 'cert.pem', work / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=check.invalid',
                        '-keyout', str(key), '-out', str(cert)], check=True, capture_output=True)
        good = nginx_test(nginx, work, configuration(snippets, work, cert, key))
        if good.returncode != 0:
            print('FAIL: nginx -t\n' + good.stderr)
            return 1
        broken = nginx_test(nginx, work, configuration([snippets[0].replace('proxy_pass', 'proxy_pas', 1)], work, cert, key))
        if broken.returncode == 0:
            print('FAIL: a misspelt directive passed nginx -t; the check proves nothing')
            return 1
        version = subprocess.run([nginx, '-v'], capture_output=True, text=True).stderr.strip()
        print(f'PASS: {len(snippets)} Nginx examples of docs/deployment.md pass nginx -t ({version}); '
              f'the launch block keeps its redirects, HSTS and Host header; a misspelt copy fails')
        return 0


if __name__ == '__main__':
    sys.exit(main())
