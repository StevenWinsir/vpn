import 'package:fl_clash/models/managed_account.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/managed_fakes.dart';

void main() {
  test('managed configuration metadata is immutable and contains no YAML', () {
    final state = ManagedAccountSnapshot.fromJson(
      managedSnapshot(phase: 'configuration_applied'),
    );
    final configuration = state.configuration!;
    expect(state.canConnect, isFalse);
    expect(configuration.groups.single.selected, 'Server-A');
    expect(() => configuration.groups.clear(), throwsUnsupportedError);
    expect(
      () => configuration.groups.single.proxies.add('imported'),
      throwsUnsupportedError,
    );
  });

  final mutations = <String, void Function(Map<String, dynamic>)>{
    'extra YAML': (value) =>
        (value['configuration'] as Map)['yaml'] = 'private',
    'extra token': (value) =>
        (value['configuration'] as Map)['token'] = 'private',
    'old generation': (value) =>
        (value['configuration']['owner'] as Map)['generation'] = 0,
    'other user': (value) =>
        (value['configuration']['owner'] as Map)['user_id'] = 'other',
    'other session': (value) =>
        (value['configuration']['owner'] as Map)['session_id'] = 'other',
    'unowned ID': (value) =>
        (value['configuration'] as Map)['id'] = '../config.yaml',
    'mismatched version': (value) =>
        (value['configuration'] as Map)['version'] = '0' * 64,
    'empty groups': (value) => (value['configuration'] as Map)['groups'] = [],
    'extra member secret': (value) =>
        (value['configuration']['groups'][0] as Map)['password'] = 'private',
    'unregistered selection': (value) =>
        (value['configuration']['groups'][0] as Map)['selected'] = 'imported',
    'background group': (value) =>
        (value['configuration']['groups'][0] as Map)['type'] = 'url-test',
    'malformed member': (value) =>
        (value['configuration']['groups'][0] as Map)['proxies'] = [1],
    'control character name': (value) =>
        (value['configuration']['groups'][0] as Map)['name'] = 'VIP\nsecret',
    'duplicate group': (value) => (value['configuration']['groups'] as List)
        .add(value['configuration']['groups'][0]),
    'configuration without applied phase': (value) =>
        value['phase'] = 'profile_required',
    'applied without configuration': (value) => value.remove('configuration'),
    'configuration without account': (value) => value['user'] = null,
  };
  for (final mutation in mutations.entries) {
    test('rejects ${mutation.key}', () {
      final value = managedSnapshot(phase: 'configuration_applied');
      mutation.value(value);
      expect(() => ManagedAccountSnapshot.fromJson(value), throwsA(anything));
    });
  }
}
