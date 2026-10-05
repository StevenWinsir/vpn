import 'dart:async';

import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../helpers/managed_fakes.dart';
import '../helpers/test_profiles.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test/managed-remember');
  const scope = 'https://login.example.invalid/api/v1/client';
  const email = 'fixture@example.invalid';
  const password = 'fixture-password';
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late ManagedCoreFake core;
  late ProviderContainer container;
  late ManagedAccount action;
  late List<MethodCall> calls;
  late Future<Object?> Function(MethodCall) respond;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    core = ManagedCoreFake();
    core.respond = (method, arguments) {
      final response = core.defaultResponse(method, arguments);
      if (response is Map<String, dynamic>) response['api_base'] = scope;
      return response;
    };
    calls = [];
    respond = (call) async => call.method == 'read' ? null : true;
    messenger.setMockMethodCallHandler(channel, (call) {
      calls.add(call);
      return respond(call);
    });
    container = ProviderContainer(
      overrides: [
        coreHandlerProvider.overrideWithValue(CoreController.scoped(core)),
        managedAppVersionProvider.overrideWithValue('test'),
        profilesProvider.overrideWith(TestProfiles.new),
        managedCredentialStoreProvider.overrideWithValue(
          MacOSManagedCredentials(channel: channel, platformSupported: true),
        ),
      ],
    );
    action = container.read(managedAccountProvider.notifier);
  });
  tearDown(() {
    container.dispose();
    messenger.setMockMethodCallHandler(channel, null);
  });

  test(
    'opt-in saves only after authentication and logout removes the saved login',
    () async {
      await action.coldStart();
      await action.login(email, password, remember: true);
      expect(calls.map((call) => call.method), ['save']);
      expect((calls.single.arguments as Map)['scope'], scope);
      expect((calls.single.arguments as Map)['password'], password);
      final preferences = await SharedPreferences.getInstance();
      expect(preferences.getKeys(), {'managed_installation_id'});
      await action.logout();
      expect(calls.map((call) => call.method), ['save', 'delete']);
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );

  test(
    'manual login without remember removes any previous saved credentials',
    () async {
      await action.coldStart();
      await action.login(email, password);
      expect(calls.map((call) => call.method), ['delete']);
    },
  );

  test('rejected authentication never stores the attempted password', () async {
    await action.coldStart();
    final original = core.respond!;
    core.respond = (method, arguments) => method == CoreMethod.managedLogin
        ? {...managedSnapshot(reason: 'invalid_credentials'), 'api_base': scope}
        : original(method, arguments);
    await action.login(email, password, remember: true);
    expect(calls, isEmpty);
    expect(container.read(managedAccountProvider).account.user, isNull);
  });

  test(
    'Keychain failure is visible without exposing password or losing the authenticated account',
    () async {
      await action.coldStart();
      respond = (_) async =>
          throw PlatformException(code: 'denied', message: password);
      await action.login(email, password, remember: true);
      final state = container.read(managedAccountProvider);
      expect(state.account.user?.email, email);
      expect(state.errorCode, 'credential_store_unavailable');
      expect(state.errorCode.contains(password), isFalse);
    },
  );

  test(
    'logout during an in-flight Keychain write queues deletion and prevents account resurrection',
    () async {
      await action.coldStart();
      final entered = Completer<void>();
      final writing = Completer<Object?>();
      respond = (call) async {
        if (call.method == 'save') {
          entered.complete();
          return writing.future;
        }
        return true;
      };
      final loggingIn = action.login(email, password, remember: true);
      await entered.future;
      final loggingOut = action.logout();
      await Future<void>.delayed(Duration.zero);
      expect(container.read(managedAccountProvider).account.user, isNull);
      writing.complete(true);
      await Future.wait([loggingIn, loggingOut]);
      expect(calls.map((call) => call.method), ['save', 'delete']);
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );
}
