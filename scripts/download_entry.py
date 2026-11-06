"""The administrator's entry sheet for one release (handoff_download.md D17).

Packaging no longer prints lines for frontend/.env: client downloads are published in the console
(管理后台 → 客户端下载). Each packaging script writes download-entry.txt into its release-private folder
(never published) with exactly the fields of the console's form, one block per file. The administrator
uploads the file, then copies these values and the public address into the form.
"""
import re
from pathlib import Path

NAME = 'download-entry.txt'

# The console's form, in its order. Keys are the API's; labels are the form's.
FIELDS = (
    ('platform', '平台'),
    ('arch', '架构'),
    ('format', '格式'),
    ('app_version', '版本号'),
    ('file', '文件名（上传后把公网地址填入“下载地址”）'),
    ('sha256', 'SHA-256'),
    ('size_bytes', '文件大小（字节）'),
    ('signing_cert_sha256', '签名证书 SHA-256'),
    ('bundle_id', 'Bundle ID'),
)
REQUIRED = ('platform', 'arch', 'format', 'app_version', 'file', 'sha256', 'size_bytes')
HEX64 = re.compile(r'^[0-9a-f]{64}$')

HEADER = (
    '后台录入单（内部资料，不得发布）\n'
    '在 管理后台 → 客户端下载 中按下列内容添加或编辑条目。先把文件上传到公网 https 地址，确认可以下载，\n'
    '再把该地址填入“下载地址”；新条目默认不上架，核对无误后再打开“上架”。保存后约 30 秒在 /download 生效。\n'
)


def entry_text(entries, note=''):
    """The sheet's text. Every entry needs REQUIRED; Android adds the certificate, an iOS web install the bundle id."""
    blocks = []
    for n, entry in enumerate(entries, 1):
        missing = [k for k in REQUIRED if entry.get(k) in (None, '')]
        if missing:
            raise ValueError('download entry %d lacks %s' % (n, ', '.join(missing)))
        for key in ('sha256', 'signing_cert_sha256'):
            if entry.get(key) and not HEX64.match(entry[key]):
                raise ValueError('download entry %d: %s is not 64 lower-case hex digits' % (n, key))
        lines = ['[%d]' % n]
        for key, label in FIELDS:
            value = entry.get(key)
            if value not in (None, ''):
                lines.append('%s：%s' % (label, value))
        blocks.append('\n'.join(lines))
    return HEADER + (note.rstrip() + '\n' if note else '') + '\n' + '\n\n'.join(blocks) + '\n'


def write(private: Path, entries, note=''):
    path = Path(private) / NAME
    path.write_text(entry_text(entries, note), encoding='utf-8')
    return path
