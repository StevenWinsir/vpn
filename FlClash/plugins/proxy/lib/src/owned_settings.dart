class OwnedProxyRecord {
  const OwnedProxyRecord(this.key, this.original, this.expected);
  final String key, original;
  final List<String> expected;

  factory OwnedProxyRecord.fromJson(Object? value) {
    if (value is! Map || value.length != 3) {
      throw const FormatException('Invalid proxy recovery entry');
    }
    final key = value['key'], original = value['original'];
    final expected = value['expected'];
    if (key is! String ||
        key.isEmpty ||
        key.length > 2048 ||
        original is! String ||
        original.length > 65536 ||
        expected is! List ||
        expected.isEmpty ||
        expected.length > 32 ||
        expected.any((item) => item is! String || item.length > 65536)) {
      throw const FormatException('Invalid proxy recovery entry');
    }
    return OwnedProxyRecord(key, original, expected.cast<String>());
  }

  Map<String, Object> toJson() => {
    'key': key,
    'original': original,
    'expected': expected,
  };
}

class ProxySetting {
  const ProxySetting({
    required this.key,
    required this.target,
    required this.read,
    required this.write,
    this.acceptOriginal,
    this.equivalent,
    this.intermediate,
  });
  final String key, target;
  final Future<String?> Function() read;
  final Future<bool> Function(String) write;
  final bool Function(String)? acceptOriginal;
  final bool Function(String, String)? equivalent;
  final List<String> Function(String, String)? intermediate;
  bool same(String left, String right) =>
      equivalent?.call(left, right) ?? left == right;
}

class _OwnedSetting {
  _OwnedSetting(this.setting, this.original, this.expected);
  final ProxySetting setting;
  final String original;
  List<String> expected;
}

/// Serializes setting changes and restores only values still written by this owner.
class OwnedProxySettings {
  OwnedProxySettings({this.checkpoint});

  final Future<void> Function(List<OwnedProxyRecord>)? checkpoint;
  final _owned = <String, _OwnedSetting>{};
  Future<void> _pending = Future.value();
  Future<bool> _serial(Future<bool> Function() action) {
    final result = _pending.then((_) => action());
    _pending = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<void> _persist() async {
    await checkpoint?.call([
      for (final entry in _owned.values)
        OwnedProxyRecord(entry.setting.key, entry.original, entry.expected),
    ]);
  }

  Future<bool> recover(
    List<OwnedProxyRecord> records,
    ProxySetting? Function(String) resolve,
  ) => _serial(() async {
    if (_owned.isNotEmpty) return false;
    final recovered = <String, _OwnedSetting>{};
    for (final record in records) {
      final setting = resolve(record.key);
      if (setting == null ||
          recovered.containsKey(record.key) ||
          !(setting.acceptOriginal?.call(record.original) ?? true) ||
          record.expected.any(
            (value) => !(setting.acceptOriginal?.call(value) ?? true),
          )) {
        return false;
      }
      recovered[record.key] = _OwnedSetting(
        setting,
        record.original,
        List.of(record.expected),
      );
    }
    _owned.addAll(recovered);
    return true;
  });

  Future<bool> apply(List<ProxySetting> settings) => _serial(() async {
    if (settings.isEmpty) return false;
    try {
      for (final setting in settings) {
        final before = await setting.read();
        final existing = _owned[setting.key];
        if (before == null ||
            !(setting.acceptOriginal?.call(before) ?? true) ||
            (existing != null &&
                !existing.expected.any(
                  (value) => setting.same(before, value),
                ))) {
          await _restore();
          return false;
        }
        final entry = _OwnedSetting(setting, existing?.original ?? before, [
          setting.target,
          ...?setting.intermediate?.call(before, setting.target),
          ...?existing?.expected,
        ]);
        _owned[setting.key] = entry;
        await _persist();
        if (!await setting.write(setting.target)) {
          await _restore();
          return false;
        }
        final actual = await setting.read();
        if (actual == null || !setting.same(actual, setting.target)) {
          await _restore();
          return false;
        }
        entry.expected = [setting.target];
        await _persist();
      }
      return true;
    } catch (_) {
      await _restore();
      return false;
    }
  });

  Future<bool> restore() => _serial(_restore);
  Future<bool> _restore() async {
    var success = true;
    for (final entry in _owned.values.toList().reversed) {
      try {
        final current = await entry.setting.read();
        if (current == null) {
          success = false;
          continue;
        }
        if (!entry.expected.any(
          (value) => entry.setting.same(current, value),
        )) {
          _owned.remove(entry.setting.key);
          continue;
        }
        entry.expected = {
          ...entry.expected,
          entry.original,
          ...?entry.setting.intermediate?.call(current, entry.original),
        }.toList();
        await _persist();
        if (!await entry.setting.write(entry.original)) {
          success = false;
          continue;
        }
        final restored = await entry.setting.read();
        if (restored == null || !entry.setting.same(restored, entry.original)) {
          success = false;
          continue;
        }
        _owned.remove(entry.setting.key);
      } catch (_) {
        success = false;
      }
    }
    try {
      await _persist();
    } catch (_) {
      success = false;
    }
    return success;
  }
}
