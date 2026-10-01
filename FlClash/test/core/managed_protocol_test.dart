import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/managed_fakes.dart';

void main() {
  test('all managed method names are registered on both sides', () {
    final constants = File('core/constant.go').readAsStringSync();
    final handlers = File('core/managed.go').readAsStringSync();
    for (final method in CoreMethod.values.where(
      (value) => value.name.startsWith('managed'),
    )) {
      expect(constants, contains('"${method.name}"'));
      expect(handlers, contains('${method.name}Method'));
    }
  });

  test('structured managed responses work without string-encoded results', () {
    final snapshot = managedSnapshot(phase: 'configuration_staged');
    final response = CoreMethodResponse.fromJson(
      jsonDecode(jsonEncode({'id': 'managed-1', 'result': snapshot}))
          as Map<String, dynamic>,
    );
    final state = ManagedAccountSnapshot.fromJson(
      response.result as Map<String, dynamic>,
    );
    expect(state.phase, ManagedPhase.configurationStaged);
    expect(state.canConnect, isFalse);
  });

  test('secrets and premature authorization are rejected by the Dart DTO', () {
    for (final key in ['token', 'yaml', 'password']) {
      final snapshot = managedSnapshot()..[key] = 'private-test-data';
      expect(
        () => ManagedAccountSnapshot.fromJson(snapshot),
        throwsFormatException,
      );
    }
    final snapshot = managedSnapshot()..['can_connect'] = true;
    expect(
      () => ManagedAccountSnapshot.fromJson(snapshot),
      throwsFormatException,
    );
    const request = ManagedLoginRequest(
      email: 'fixture@example.invalid',
      password: 'never-log-this',
      deviceId: managedFixtureDevice,
      platform: 'macos',
      appVersion: 'test',
    );
    expect(request.toString(), isNot(contains('never-log-this')));
  });

  test('login arguments are absent from generic Core logs', () {
    final interface = File('lib/core/interface.dart').readAsStringSync();
    expect(interface, isNot(contains(r'$arguments')));
    expect(interface, isNot(contains(r'${arguments}')));
  });

  test('boot and deep links do not apply saved subscriptions before login', () {
    final application = File('lib/application.dart').readAsStringSync();
    final bootstrap = File('lib/bootstrap.dart').readAsStringSync();
    expect(application, contains('child: const ManagedAccountPage()'));
    expect(application, isNot(contains('addProfileFormURL')));
    expect(application, isNot(contains('autoUpdateProfiles')));
    expect(bootstrap, isNot(contains('.initStatus()')));
    expect(bootstrap, isNot(contains('autoUpdateProfiles')));
  });
}
