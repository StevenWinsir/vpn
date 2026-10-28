#!/usr/bin/env python3
"""User-visible wording of the native clients (handoff_app_standardization.md 7.1, 7.4).

Everything a user can read in the apps must be commercial copy: no internals (core engine, tunnel,
helper service, protocol, metering, settlement), no development leftovers, no open source or licence
information (D10), no unit notes such as GiB. This scans the sources of every client that has been
converted so far; the runtime guards of the UI tests use the same word lists (the lists in
AsterLinkUITests/CopyGuard.swift and the Android CopyGuard.kt are compared with this file below).

Scanned: Core messages.go values; the Chinese string literals of iOS, AsterLinkKit and the tunnel
extension; the Android strings.xml files (Chinese and English) and Chinese literals in Kotlin; the
macOS app and its shared module (round 3), where "VPN" is forbidden as well (7.1 item 7). The macOS
design preview checks the text of every rendered screen against these same lists at run time
(scripts/test-native-macos.py --suite ui imports them from this file).

A line is exempt when it, or the line above it, contains the marker "copy-guard" followed by the
reason. Whole files are exempt only when listed in DEBUG_ONLY_FILES (they are not compiled into
release builds). Standard library only.
"""
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Chinese words and phrases (regular expressions) a user must never read. 7.1 items 2, 3, 3a, 4, 5, 6.
FORBIDDEN_ZH = [
    '内核', '隧道', '代理', '特权服务', '握手', '协议版本', '构建渠道', '渠道', '通道', '代次', '批次', '账本',
    '会话交接', '节点计量', '客户端上报', '计量', '幂等', '请求标识', '数据通路', '转发', '校验', '清单', '构建',
    '签名', '令牌', '进程', '运行时', '架构', '退出码', '错误代码',
    '开发版', '调试', '联调', '测试线路', '内部', '第.轮', '仓库',
    '开源', '许可证', '第三方组件', '源代码', '源码',
    '管理员(?!账号|权限)', '结算', '2³⁰', '1024³',
]
# English technical words (case-sensitive) in any user-visible string. 3a has no exemptions for the open
# source names; "VPN" is not listed because iOS and Android must say it when the system asks for a VPN
# configuration (7.1 item 7); the desktop clients add it to their own scans.
FORBIDDEN_EN = [
    'Core', 'Mihomo', 'Clash', 'Helper', 'GiB', 'MiB', 'KiB', 'GPL', 'Apache', 'sing-tun', 'GitHub', 'handoff',
    'channel', 'generation', 'handshake', 'protocol', 'metering', 'settle', 'ledger', 'tunnel', 'Tunnel',
    '.env', 'localhost', 'debug', 'Debug',
]
# The desktop clients never ask the system for a VPN configuration, so they never say it (7.1 item 7).
FORBIDDEN_DESKTOP_EN = ['VPN']
# English words for the English strings.xml, case-insensitive whole words.
FORBIDDEN_EN_TEXT = [
    'core', 'mihomo', 'clash', 'helper', 'gib', 'gpl', 'apache', 'github', 'channel', 'generation', 'handshake',
    'protocol', 'metering', 'settle', 'settled', 'settlement', 'ledger', 'tunnel', 'batch', 'idempotent',
    'licence', 'license', 'licences', 'licenses', 'open source', 'source code', 'debug', 'handoff',
    'administrator', 'admin',
]
# Never compiled into a release build (checked by the release artefact audit of round 5).
DEBUG_ONLY_FILES = {
    'apps/ios/AsterLink/TunnelDebugView.swift',
    'apps/ios/AsterLink/UIFixtures.swift',
    'apps/macos/Sources/AsterLinkApp/DirectCoreTransport.swift',
    'apps/macos/Sources/AsterLinkApp/PreviewRunner.swift',
}
MARKER = 'copy-guard'

failures = []
scanned = 0
exempted = 0


def text(path):
    return (ROOT / path).read_text(encoding='utf-8-sig')


def report(path, line, word, literal):
    failures.append('%s:%d  "%s" in %r' % (path, line, word, literal))


def scan_literal(path, line_no, literal, lines, extra_en=()):
    """Check one user-visible string; lines is the source so the exemption marker can be found."""
    global scanned, exempted
    scanned += 1
    here = lines[line_no - 1]
    above = lines[line_no - 2] if line_no > 1 else ''
    hits = [w for w in FORBIDDEN_ZH if re.search(w, literal)]
    hits += [w for w in FORBIDDEN_EN + list(extra_en) if re.search(r'(?<![A-Za-z])%s(?![a-z])' % re.escape(w), literal)]
    if not hits:
        return
    if MARKER in here or MARKER in above:
        exempted += 1
        return
    for word in hits:
        report(path, line_no, word, literal)


CJK = re.compile('[一-龥]')
# An English-only literal is user-visible only when a view displays it; log lines and error details are not.
VIEW_CALL = re.compile(r'\b(Text|Label|Button|Section|Toggle|TextField|SecureField|Link|navigationTitle|accessibilityLabel|'
                       r'accessibilityHint|accessibilityValue|alert|confirmationDialog|Picker|Menu|ShareLink|LabeledContent)\b')


def literals(source):
    """[(line, text)] of every string literal of a Swift or Kotlin source, with the \\(...) interpolations
    cut out (their inner literals are returned on their own) and comments skipped."""
    found = []
    length = len(source)

    def parse_string(i, line):
        """source[i] is the opening quote; returns (index after the closing quote, line)."""
        start_line, parts = line, []
        i += 1
        while i < length and source[i] != '"':
            char = source[i]
            if char == '\\' and source[i + 1:i + 2] == '(':
                depth, i = 1, i + 2
                while i < length and depth:
                    if source[i] == '"':
                        i, line = parse_string(i, line)
                        continue
                    depth += (source[i] == '(') - (source[i] == ')')
                    line += source[i] == '\n'
                    i += 1
                parts.append(' ')
                continue
            if char == '\\':
                parts.append(source[i + 1:i + 2])
                i += 2
                continue
            line += char == '\n'
            parts.append(char)
            i += 1
        found.append((start_line, ''.join(parts)))
        return i + 1, line

    i, line = 0, 1
    while i < length:
        char = source[i]
        if source.startswith('//', i):
            while i < length and source[i] != '\n':
                i += 1
        elif source.startswith('/*', i):
            end = source.find('*/', i + 2)
            end = length if end < 0 else end + 2
            line += source.count('\n', i, end)
            i = end
        elif char == '"':
            if source.startswith('"""', i):
                raise SystemExit('multi-line string literals are not supported by the copy guard (line %d)' % line)
            i, line = parse_string(i, line)
        else:
            line += char == '\n'
            i += 1
    return sorted(found)


def debug_lines(source):
    """Line numbers inside `#if DEBUG` (up to its #else or #endif): never compiled into a release build."""
    inside, stack, depth_of_debug = set(), [], None
    for number, line in enumerate(source.splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith('#if'):
            stack.append(stripped)
            if stripped in ('#if DEBUG', '#if DEBUG && !targetEnvironment(simulator)') and depth_of_debug is None:
                depth_of_debug = len(stack)
        elif stripped.startswith('#else') or stripped.startswith('#elseif'):
            if depth_of_debug == len(stack):
                depth_of_debug = None
        elif stripped.startswith('#endif'):
            if depth_of_debug == len(stack):
                depth_of_debug = None
            stack.pop()
        elif depth_of_debug is not None:
            inside.add(number)
    return inside


def scan_code(path, extra_en=()):
    """Scan the user-visible string literals of a Swift or Kotlin file."""
    source = text(path)
    lines = source.splitlines()
    skipped = debug_lines(source) if path.endswith('.swift') else set()
    for number, literal in literals(source):
        if number in skipped:
            continue
        literal = literal.strip()
        if CJK.search(literal) or (path.endswith('.swift') and VIEW_CALL.search(lines[number - 1])
                                   and re.search('[A-Za-z]{3}', literal)):
            scan_literal(path, number, literal, lines, extra_en)


def scan_core():
    source = text('native/core/internal/apiclient/messages.go')
    lines = source.splitlines()
    for number, line in enumerate(lines, 1):
        match = re.match(r'\s*"([a-z_0-9]+)":\s+"([^"]*)",', line)
        if match:
            scan_literal('native/core/internal/apiclient/messages.go', number, match.group(2), lines)
    fallback = re.search(r'return "([^"]+)"', source)
    if fallback:
        scan_literal('native/core/internal/apiclient/messages.go', source[:fallback.start()].count('\n') + 1, fallback.group(1), lines)


def scan_strings_xml(path):
    lines = text(path).splitlines()
    english = '/values-en/' in path
    root = ET.fromstring(text(path))
    for element in root.iter('string'):
        value = ''.join(element.itertext())
        number = next((i for i, l in enumerate(lines, 1) if 'name="%s"' % element.get('name') in l), 1)
        if english:
            global scanned, exempted
            scanned += 1
            hits = [w for w in FORBIDDEN_EN_TEXT if re.search(r'(?<![a-z])%s(?![a-z])' % re.escape(w), value.lower())]
            hits += [w for w in FORBIDDEN_ZH if re.search(w, value)]
            if hits:
                if MARKER in lines[number - 1] or (number > 1 and MARKER in lines[number - 2]):
                    exempted += 1
                else:
                    for word in hits:
                        report(path, number, word, value)
        else:
            scan_literal(path, number, value, lines)


def list_in(path, name):
    """The quoted entries of the array literal that follows `name` in a Swift or Kotlin file."""
    source = text(path)
    start = source.index(name)
    body = source[source.index('[', start) if '[' in source[start:start + 80] else source.index('listOf(', start):]
    end = body.index(']') if body.startswith('[') else body.index('\n    )')
    return re.findall(r'"((?:[^"\\]|\\.)*)"', body[:end])


def check_runtime_lists():
    """The UI tests carry their own copy of the word lists; they must not drift from this file."""
    expected = {'forbiddenZh': FORBIDDEN_ZH, 'forbiddenEn': FORBIDDEN_EN}
    for path in ('apps/ios/AsterLinkUITests/CopyGuard.swift', 'apps/android/app/src/test/java/com/asterlink/vpn/ui/CopyGuard.kt'):
        if not (ROOT / path).is_file():
            failures.append('%s is missing (the runtime copy guard)' % path)
            continue
        for name, words in expected.items():
            got = [w.replace('\\\\', '\\') for w in list_in(path, name)]
            if got != words:
                failures.append('%s: %s differs from scripts/test-app-copy.py' % (path, name))


def main():
    scan_core()
    ios = [p for p in (ROOT / 'apps/ios').rglob('*.swift')
           if '/AsterLinkTests/' not in str(p) and '/AsterLinkUITests/' not in str(p) and '/Tests/' not in str(p)
           and '/.build/' not in str(p) and '/.swiftpm/' not in str(p) and '/Harness/' not in str(p)]
    for path in sorted(ios):
        relative = str(path.relative_to(ROOT))
        if relative not in DEBUG_ONLY_FILES:
            scan_code(relative)
    for path in sorted((ROOT / 'apps/android/app/src/main').rglob('strings.xml')):
        scan_strings_xml(str(path.relative_to(ROOT)))
    for path in sorted((ROOT / 'apps/android/app/src/main/java').rglob('*.kt')):
        source = path.read_text(encoding='utf-8')
        if CJK.search(source):
            scan_code(str(path.relative_to(ROOT)))
    for module in ('AsterLinkApp', 'AsterLinkShared'):
        for path in sorted((ROOT / 'apps/macos/Sources' / module).glob('*.swift')):
            relative = str(path.relative_to(ROOT))
            if relative not in DEBUG_ONLY_FILES:
                scan_code(relative, FORBIDDEN_DESKTOP_EN)
    check_runtime_lists()

    for failure in failures:
        print('FAIL  ' + failure)
    print('\n%d user-visible string(s) scanned, %d exempted by a "%s" marker, %d problem(s)' % (scanned, exempted, MARKER, len(failures)))
    return 1 if failures else 0


if __name__ == '__main__':
    sys.exit(main())
