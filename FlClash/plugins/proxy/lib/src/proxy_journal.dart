import 'dart:convert';
import 'dart:io';

import 'owned_settings.dart';

abstract interface class ProxyJournal {
  Future<List<OwnedProxyRecord>> read();
  Future<void> write(List<OwnedProxyRecord> records);
}

class FileProxyJournal implements ProxyJournal {
  FileProxyJournal(this.directory);

  final Future<Directory> Function() directory;
  static final _held = <String>{};
  Directory? _directory;
  RandomAccessFile? _lock;

  Future<Directory> _open() async {
    if (_lock != null) return _directory!;
    final root = await directory();
    final type = await FileSystemEntity.type(root.path, followLinks: false);
    if (type != FileSystemEntityType.notFound &&
        type != FileSystemEntityType.directory) {
      throw const FileSystemException('Unsafe proxy recovery directory');
    }
    await root.create(recursive: true);
    await _private(root.path, '700');
    final canonical = await root.resolveSymbolicLinks();
    if (!_held.add(canonical)) {
      throw const FileSystemException('Proxy recovery already owned');
    }
    RandomAccessFile? handle;
    try {
      final file = File('$canonical/owner.lock');
      await _regular(file);
      handle = await file.open(mode: FileMode.append);
      await _private(file.path, '600');
      await handle.lock(FileLock.exclusive);
      _directory = Directory(canonical);
      _lock = handle;
      return _directory!;
    } catch (_) {
      await handle?.close();
      _held.remove(canonical);
      rethrow;
    }
  }

  Future<void> _regular(File file) async {
    final type = await FileSystemEntity.type(file.path, followLinks: false);
    if (type != FileSystemEntityType.notFound &&
        type != FileSystemEntityType.file) {
      throw const FileSystemException('Unsafe proxy recovery file');
    }
  }

  Future<void> _private(String path, String mode) async {
    final result = await Process.run('/bin/chmod', [mode, path]);
    if (result.exitCode != 0) {
      throw const FileSystemException('Cannot protect proxy recovery data');
    }
  }

  @override
  Future<List<OwnedProxyRecord>> read() async {
    final root = await _open();
    final file = File('${root.path}/settings.json');
    await _regular(file);
    if (!await file.exists()) return [];
    if (await file.length() > 1024 * 1024) {
      throw const FormatException('Proxy recovery record too large');
    }
    final decoded = jsonDecode(await file.readAsString());
    if (decoded is! Map || decoded['version'] != 1 || decoded.length != 2) {
      throw const FormatException('Unknown proxy recovery version');
    }
    final records = decoded['records'];
    if (records is! List || records.length > 1024) {
      throw const FormatException('Invalid proxy recovery records');
    }
    final result = records.map(OwnedProxyRecord.fromJson).toList();
    if (result.map((entry) => entry.key).toSet().length != result.length) {
      throw const FormatException('Duplicate proxy recovery keys');
    }
    return result;
  }

  @override
  Future<void> write(List<OwnedProxyRecord> records) async {
    final root = await _open();
    final file = File('${root.path}/settings.json');
    await _regular(file);
    if (records.isEmpty) {
      if (await file.exists()) await file.delete();
      return;
    }
    final temporary = File('${root.path}/settings.pending');
    await _regular(temporary);
    await temporary.writeAsString(
      jsonEncode({
        'version': 1,
        'records': records.map((record) => record.toJson()).toList(),
      }),
      flush: true,
    );
    await _private(temporary.path, '600');
    await temporary.rename(file.path);
  }

  Future<void> close() async {
    final handle = _lock;
    _lock = null;
    if (handle != null) {
      await handle.close();
      _held.remove(_directory!.path);
    }
  }
}
