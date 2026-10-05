import 'dart:async';

import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test/managed-keychain');
  const scope = 'https://login.example.invalid/api/v1/client';
  const login = RememberedManagedLogin(
    'fixture@example.invalid',
    'fixture-password',
  );
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late MacOSManagedCredentials store;
  late List<MethodCall> calls;
  late Future<Object?> Function(MethodCall) respond;

  setUp(() {
    calls = [];
    store = MacOSManagedCredentials(channel: channel, platformSupported: true);
    respond = (call) async => call.method == 'read'
        ? {'email': login.email, 'password': login.password}
        : true;
    messenger.setMockMethodCallHandler(channel, (call) {
      calls.add(call);
      return respond(call);
    });
  });
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test('read save delete use endpoint-scoped native Keychain only', () async {
    expect((await store.read(scope))?.email, login.email);
    await store.save(scope, login);
    await store.delete(scope);
    expect(calls.map((call) => call.method), ['read', 'save', 'delete']);
    expect(
      calls.every((call) => (call.arguments as Map)['scope'] == scope),
      isTrue,
    );
    expect(login.toString(), isNot(contains(login.password)));
    expect(login.toString(), isNot(contains(login.email)));
  });

  test('saved login is optional', () async {
    respond = (_) async => null;
    expect(await store.read(scope), isNull);
  });

  test('invalid or insecure scopes never reach the platform', () async {
    for (final invalid in [
      '',
      'http://remote.example.invalid/api/v1/client',
      'https://user:secret@example.invalid/api/v1/client',
      '$scope?token=secret',
      '$scope#fragment',
      '$scope/',
      'https://example.invalid:0/api/v1/client',
    ]) {
      expect(isManagedCredentialScope(invalid), isFalse);
      await expectLater(
        store.read(invalid),
        throwsA(isA<ManagedCredentialException>()),
      );
    }
    expect(calls, isEmpty);
    expect(
      isManagedCredentialScope('http://127.0.0.1:8080/api/v1/client'),
      isTrue,
    );
  });

  test('unsupported platform never falls back to plaintext', () async {
    final unsupported = MacOSManagedCredentials(
      channel: channel,
      platformSupported: false,
    );
    await expectLater(
      unsupported.save(scope, login),
      throwsA(isA<ManagedCredentialException>()),
    );
    expect(calls, isEmpty);
  });

  test(
    'malformed native records and denied writes are controlled errors',
    () async {
      for (final value in [
        <String, Object?>{},
        {'email': login.email, 'password': 42},
        {'email': login.email, 'password': ''},
        {'email': login.email, 'password': login.password, 'unexpected': true},
      ]) {
        respond = (_) async => value;
        await expectLater(
          store.read(scope),
          throwsA(isA<ManagedCredentialException>()),
        );
      }
      respond = (_) async => false;
      await expectLater(
        store.save(scope, login),
        throwsA(isA<ManagedCredentialException>()),
      );
      await expectLater(
        store.delete(scope),
        throwsA(isA<ManagedCredentialException>()),
      );
      respond = (_) async =>
          throw PlatformException(code: 'secret', message: login.password);
      try {
        await store.read(scope);
        fail('Expected platform denial');
      } catch (error) {
        expect(error.toString(), isNot(contains(login.password)));
      }
    },
  );

  test(
    'logout deletion waits for pending save and cannot resurrect credentials',
    () async {
      final writing = Completer<Object?>();
      final entered = Completer<void>();
      respond = (call) async {
        if (call.method == 'save') {
          entered.complete();
          return writing.future;
        }
        return true;
      };
      final saved = store.save(scope, login);
      await entered.future;
      final deleted = store.delete(scope);
      await Future<void>.delayed(Duration.zero);
      expect(calls.map((call) => call.method), ['save']);
      writing.complete(true);
      await Future.wait([saved, deleted]);
      expect(calls.map((call) => call.method), ['save', 'delete']);
    },
  );

  test('failed native operation does not poison later deletion', () async {
    respond = (_) async => false;
    await expectLater(
      store.save(scope, login),
      throwsA(isA<ManagedCredentialException>()),
    );
    respond = (_) async => true;
    await store.delete(scope);
    expect(calls.last.method, 'delete');
    await expectLater(
      store.save(scope, const RememberedManagedLogin('bad', '')),
      throwsA(isA<ManagedCredentialException>()),
    );
  });
}
