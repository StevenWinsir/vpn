import 'dart:async';

import 'package:fl_clash/common/constant.dart';
import 'package:fl_clash/common/preferences.dart';
import 'package:fl_clash/common/print.dart';
import 'package:fl_clash/common/system.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;

class SystemDnsRecord {
  final String service;
  final List<String> servers;

  const SystemDnsRecord({required this.service, required this.servers});

  static SystemDnsRecord? fromJson(Object? json) {
    if (json is! Map) {
      return null;
    }
    final service = json['service'];
    final servers = json['servers'];
    if (service is! String || service.isEmpty || servers is! List) {
      return null;
    }
    return SystemDnsRecord(
      service: service,
      servers: servers.whereType<String>().toList(),
    );
  }

  Map<String, Object?> toJson() => {'service': service, 'servers': servers};

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is SystemDnsRecord &&
          other.service == service &&
          stringListEquality.equals(other.servers, servers);

  @override
  int get hashCode => Object.hash(service, stringListEquality.hash(servers));

  @override
  String toString() => 'SystemDnsRecord($service, $servers)';
}

abstract interface class SystemDnsPort {
  Future<String?> resolveDefaultService();

  Future<List<String>?> readDnsServers(String service);

  Future<bool> writeDnsServers(String service, List<String> servers);
}

abstract interface class SystemDnsStore {
  Future<SystemDnsRecord?> read();

  Future<void> write(SystemDnsRecord record);

  Future<void> clear();
}

final class PreferencesSystemDnsStore implements SystemDnsStore {
  const PreferencesSystemDnsStore();

  @override
  Future<SystemDnsRecord?> read() => preferences.getSystemDnsRecord();

  @override
  Future<void> write(SystemDnsRecord record) =>
      preferences.saveSystemDnsRecord(record);

  @override
  Future<void> clear() => preferences.clearSystemDnsRecord();
}

final class SystemDnsCoordinator {
  final SystemDnsPort port;
  final SystemDnsStore store;
  final String fallbackDns;

  Future<void> _queue = Future.value();
  SystemDnsRecord? _applied;
  bool _desired = false;
  bool _recovered = false;
  bool _shutdown = false;

  SystemDnsCoordinator({
    required this.port,
    required this.store,
    this.fallbackDns = defaultSystemDnsFallback,
  });

  @visibleForTesting
  SystemDnsRecord? get appliedRecord => _applied;

  Future<void> sync(bool patch) {
    if (_shutdown) {
      return _queue;
    }
    _desired = patch;
    return _enqueue();
  }

  Future<void> resync() {
    if (_shutdown) {
      return _queue;
    }
    return _enqueue();
  }

  Future<void> shutdown() {
    _shutdown = true;
    _desired = false;
    final operation = _queue.then((_) => _converge()).catchError(_reportError);
    _queue = operation;
    return operation;
  }

  Future<void> _enqueue() {
    final operation = _queue.then((_) => _converge()).catchError(_reportError);
    _queue = operation;
    return operation;
  }

  void _reportError(Object error) {
    commonPrint.log(
      'System DNS update failed: ${compactError(error)}',
      logLevel: LogLevel.error,
    );
  }

  Future<void> _converge() async {
    await _recover();
    if (_desired) {
      await _apply();
      return;
    }
    await _release();
  }

  Future<void> _recover() async {
    if (_recovered) {
      return;
    }
    _applied = await store.read();
    _recovered = true;
  }

  Future<void> _apply() async {
    final service = await port.resolveDefaultService();
    if (service == null) {
      commonPrint.log(
        'Skipped the system DNS update, no default network service',
        logLevel: LogLevel.warning,
      );
      return;
    }
    final applied = _applied;
    if (applied != null) {
      if (applied.service == service) {
        await _reapply(service);
        return;
      }
      await _release();
      if (_applied != null) {
        commonPrint.log(
          'Skipped the system DNS update, ${applied.service} still holds it',
          logLevel: LogLevel.warning,
        );
        return;
      }
    }
    final current = await port.readDnsServers(service);
    if (current == null) {
      commonPrint.log(
        'Skipped the system DNS update, unable to read $service',
        logLevel: LogLevel.warning,
      );
      return;
    }
    final record = SystemDnsRecord(service: service, servers: current);
    await store.write(record);
    _applied = record;
    final expected = _expectedServers(record);
    if (!_sameServers(current, expected)) {
      final ok = await port.writeDnsServers(service, expected);
      if (!ok) {
        await _release();
        return;
      }
      final written = await port.readDnsServers(service);
      if (written == null || !_sameServers(written, expected)) {
        await _release();
        return;
      }
    }
    _applied = record;
  }

  Future<void> _reapply(String service) async {
    final current = await port.readDnsServers(service);
    if (current == null) {
      commonPrint.log(
        'Skipped the system DNS resync, unable to read $service',
        logLevel: LogLevel.warning,
      );
      return;
    }
    final expected = _expectedServers(_applied!);
    if (_sameServers(current, expected)) {
      return;
    }
    // A different process changed the setting while retaining our fallback.
    // Drop ownership instead of overwriting that process on the next release.
    if (current.contains(fallbackDns)) {
      _applied = null;
      await store.clear();
      return;
    }
    final record = SystemDnsRecord(service: service, servers: current);
    await store.write(record);
    _applied = record;
    final next = _expectedServers(record);
    if (!await port.writeDnsServers(service, next)) {
      await _release();
      return;
    }
    final written = await port.readDnsServers(service);
    if (written == null || !_sameServers(written, next)) {
      await _release();
      return;
    }
    _applied = record;
  }

  Future<void> _release() async {
    final applied = _applied;
    if (applied == null) {
      return;
    }
    final current = await port.readDnsServers(applied.service);
    if (current == null) {
      commonPrint.log(
        'Failed to read the current system DNS of ${applied.service}',
        logLevel: LogLevel.warning,
      );
      return;
    }
    if (!_sameServers(current, _expectedServers(applied))) {
      // The value is no longer ours. Preserve the external owner and forget
      // the backup so a later shutdown cannot restore over it.
      _applied = null;
      await store.clear();
      return;
    }
    if (!await port.writeDnsServers(applied.service, applied.servers)) {
      commonPrint.log(
        'Failed to restore the system DNS of ${applied.service}',
        logLevel: LogLevel.warning,
      );
      return;
    }
    final restored = await port.readDnsServers(applied.service);
    if (restored == null || !_sameServers(restored, applied.servers)) {
      commonPrint.log(
        'Failed to verify the restored system DNS of ${applied.service}',
        logLevel: LogLevel.warning,
      );
      return;
    }
    _applied = null;
    await store.clear();
  }

  List<String> _expectedServers(SystemDnsRecord record) {
    if (record.servers.contains(fallbackDns)) {
      return List.of(record.servers);
    }
    return [...record.servers, fallbackDns];
  }

  bool _sameServers(List<String> left, List<String> right) {
    if (left.length != right.length) return false;
    for (var index = 0; index < left.length; index++) {
      if (left[index] != right[index]) return false;
    }
    return true;
  }
}

final systemDnsCoordinator = system.isMacOS
    ? SystemDnsCoordinator(
        port: macOS!,
        store: const PreferencesSystemDnsStore(),
      )
    : null;
