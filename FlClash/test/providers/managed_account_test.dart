import 'dart:async';
import 'dart:io';

import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../helpers/managed_fakes.dart';
import '../helpers/test_profiles.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late ManagedCoreFake core;
  late ProviderContainer container;
  late ManagedAccount action;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    core = ManagedCoreFake();
    container = ProviderContainer(
      overrides: [
        coreHandlerProvider.overrideWithValue(CoreController.scoped(core)),
        managedAppVersionProvider.overrideWithValue('test'),
        profilesProvider.overrideWith(TestProfiles.new),
      ],
    );
    action = container.read(managedAccountProvider.notifier);
  });

  tearDown(() => container.dispose());

  test(
    'cold start stops old listeners and resets the Core account before login',
    () async {
      core.account = managedSnapshot(phase: 'configuration_staged');
      expect(container.read(managedAccountProvider).ready, isFalse);
      await action.coldStart();
      expect(core.calls.map((call) => call.method), [
        CoreMethod.stopListener,
        CoreMethod.managedReset,
      ]);
      expect(container.read(managedAccountProvider).ready, isTrue);
      expect(container.read(managedAccountProvider).account.user, isNull);
      expect(container.read(managedConnectionAllowedProvider), isFalse);
    },
  );

  test(
    'VIP login applies server configuration without authorizing a connection',
    () async {
      await action.coldStart();
      await action.login(' fixture@example.invalid ', 'fixture-password');
      final state = container.read(managedAccountProvider);
      expect(state.account.phase, ManagedPhase.configurationApplied);
      expect(state.account.canConnect, isFalse);
      final login = core.calls.singleWhere(
        (call) => call.method == CoreMethod.managedLogin,
      );
      expect((login.arguments as Map)['email'], 'fixture@example.invalid');
      expect((login.arguments as Map)['platform'], Platform.operatingSystem);
      expect(
        core.calls.where((call) => call.method == CoreMethod.managedLoadConfig),
        hasLength(1),
      );
      final saved = await SharedPreferences.getInstance();
      expect(saved.getKeys(), {'managed_installation_id'});
      expect(
        saved.getString('managed_installation_id'),
        matches(
          RegExp(
            r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
          ),
        ),
      );
    },
  );

  for (final reason in [
    'upgrade_required',
    'paid_vip_required',
    'subscription_expired',
    'quota_exhausted',
    'device_limit',
  ]) {
    test(
      '$reason retains account but never loads a profile or starts listeners',
      () async {
        await action.coldStart();
        core.loginReason = reason;
        await action.login('fixture@example.invalid', 'fixture-password');
        expect(container.read(managedAccountProvider).errorCode, reason);
        expect(
          container.read(managedAccountProvider).account.phase,
          ManagedPhase.restricted,
        );
        expect(
          core.calls.any(
            (call) =>
                call.method == CoreMethod.managedLoadConfig ||
                call.method == CoreMethod.startListener,
          ),
          isFalse,
        );
      },
    );
  }

  test(
    'duplicate submission and late login after logout cannot install an account',
    () async {
      await action.coldStart();
      await container.read(managedInstallationIdProvider.future);
      final response = Completer<Object?>();
      final entered = Completer<void>();
      core.respond = (method, arguments) {
        if (method == CoreMethod.managedLogin) {
          entered.complete();
          return response.future;
        }
        return core.defaultResponse(method, arguments);
      };
      final pending = action.login(
        'fixture@example.invalid',
        'fixture-password',
      );
      await entered.future;
      await action.login('duplicate@example.invalid', 'ignored-password');
      await action.logout();
      response.complete(
        managedSnapshot(phase: 'profile_required', generation: 2),
      );
      await pending;
      expect(
        core.calls.where((call) => call.method == CoreMethod.managedLogin),
        hasLength(1),
      );
      expect(
        core.calls.where((call) => call.method == CoreMethod.managedLoadConfig),
        isEmpty,
      );
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );

  test(
    'cancel while installation identity loads prevents any later login RPC',
    () async {
      container.dispose();
      final identity = Completer<String>();
      container = ProviderContainer(
        overrides: [
          coreHandlerProvider.overrideWithValue(CoreController.scoped(core)),
          managedAppVersionProvider.overrideWithValue('test'),
          managedInstallationIdProvider.overrideWith((ref) => identity.future),
        ],
      );
      action = container.read(managedAccountProvider.notifier);
      await action.coldStart();
      final pending = action.login(
        'fixture@example.invalid',
        'fixture-password',
      );
      await action.logout();
      identity.complete(managedFixtureDevice);
      await pending;
      expect(
        core.calls.where((call) => call.method == CoreMethod.managedLogin),
        isEmpty,
      );
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );

  test('cached UI polling does not request a backend heartbeat', () async {
    await action.coldStart();
    await action.poll();
    final call = core.calls.last;
    expect(call.method, CoreMethod.managedStatus);
    expect((call.arguments as Map)['refresh'], isFalse);
    core.loginReason = 'upgrade_required';
    await action.login('fixture@example.invalid', 'fixture-password');
    await action.refresh();
    expect(
      core.calls
          .where((call) => call.method == CoreMethod.managedStatus)
          .last
          .arguments,
      containsPair('refresh', true),
    );
  });

  test('late cached poll cannot overwrite a new login', () async {
    await action.coldStart();
    final response = Completer<Object?>();
    core.respond = (method, arguments) => method == CoreMethod.managedStatus
        ? response.future
        : core.defaultResponse(method, arguments);
    final pending = action.poll();
    await action.login('fixture@example.invalid', 'fixture-password');
    response.complete(managedSnapshot());
    await pending;
    expect(
      container.read(managedAccountProvider).account.phase,
      ManagedPhase.configurationApplied,
    );
  });

  test('Core loss clears identity and reconnection requires reset', () async {
    await action.coldStart();
    await action.login('fixture@example.invalid', 'fixture-password');
    container.read(coreStatusProvider.notifier).value = CoreStatus.connecting;
    expect(container.read(managedAccountProvider).ready, isFalse);
    expect(container.read(managedAccountProvider).account.user, isNull);
    expect(container.read(runTimeProvider), isNull);
    container.read(coreStatusProvider.notifier).value = CoreStatus.connected;
    await Future<void>.delayed(Duration.zero);
    expect(container.read(managedAccountProvider).ready, isTrue);
    expect(container.read(managedAccountProvider).account.user, isNull);
  });

  test(
    'old autoRun and a saved local profile cannot start or be applied',
    () async {
      container
          .read(appSettingProvider.notifier)
          .update((value) => value.copyWith(autoRun: true));
      container
          .read(profilesProvider.notifier)
          .put(Profile.normal(label: 'legacy'));
      final setup = container.read(setupActionProvider.notifier);
      await setup.initStatus();
      expect(await setup.setRunning(true), isFalse);
      expect(await setup.applyProfile(), isFalse);
      expect(
        core.calls.any(
          (call) =>
              call.method == CoreMethod.startListener ||
              call.method == CoreMethod.setupConfig,
        ),
        isFalse,
      );
      expect(container.read(runTimeProvider), isNull);
    },
  );

  test(
    'malformed RPC response fails closed without exposing server details',
    () async {
      await action.coldStart();
      core.respond = (method, _) => {
        'token': 'private-server-data',
        'can_connect': true,
      };
      await action.login('fixture@example.invalid', 'fixture-password');
      expect(
        container.read(managedAccountProvider).errorCode,
        'invalid_server_response',
      );
      expect(container.read(managedConnectionAllowedProvider), isFalse);
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );

  test(
    'installation ID is stable but the account does not survive a new container',
    () async {
      final first = await container.read(managedInstallationIdProvider.future);
      await action.coldStart();
      await action.login('fixture@example.invalid', 'fixture-password');
      final other = ProviderContainer();
      addTearDown(other.dispose);
      expect(await other.read(managedInstallationIdProvider.future), first);
      expect(other.read(managedAccountProvider).account.user, isNull);
      expect(other.read(managedAccountProvider).ready, isFalse);
    },
  );
}
