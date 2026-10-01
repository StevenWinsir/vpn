import 'dart:async';

import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/enum/enum.dart';
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
  late ProviderContainer container;
  late ManagedCoreFake core;
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
  Future<void> login() async {
    await action.coldStart();
    await action.login('fixture@example.invalid', 'fixture-password');
  }

  test(
    'only current configuration identity is sent to the selection RPC',
    () async {
      await login();
      final before = container.read(managedAccountProvider).account;
      await action.selectProxy('VIP', 'Server-B');
      final call = core.calls.last;
      expect(call.method, CoreMethod.managedSelectProxy);
      expect(call.arguments, {
        'generation': before.generation,
        'configuration_id': before.configuration!.id,
        'group': 'VIP',
        'proxy': 'Server-B',
      });
      expect(
        container
            .read(managedAccountProvider)
            .account
            .configuration!
            .groups
            .single
            .selected,
        'Server-B',
      );
      expect(container.read(managedConnectionAllowedProvider), isFalse);
      expect(
        core.calls.any(
          (call) =>
              call.method == CoreMethod.changeProxy ||
              call.method == CoreMethod.startListener,
        ),
        isFalse,
      );
      await action.selectProxy('VIP', 'imported-node');
      expect(
        container.read(managedAccountProvider).errorCode,
        'invalid_managed_selection',
      );
      expect(
        container
            .read(managedAccountProvider)
            .account
            .configuration!
            .groups
            .single
            .selected,
        'Server-B',
      );
    },
  );

  test(
    'reload hides old nodes immediately and ignores a late response after logout',
    () async {
      await login();
      final previousGeneration = container
          .read(managedAccountProvider)
          .account
          .generation;
      final response = Completer<Object?>();
      core.respond = (method, arguments) =>
          method == CoreMethod.managedLoadConfig
          ? response.future
          : core.defaultResponse(method, arguments);
      final pending = action.reloadConfiguration();
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNull,
      );
      expect(container.read(managedAccountProvider).busy, isTrue);
      await action.logout();
      response.complete(
        managedSnapshot(
          phase: 'configuration_applied',
          generation: previousGeneration,
        ),
      );
      await pending;
      expect(container.read(managedAccountProvider).account.user, isNull);
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNull,
      );
    },
  );

  test(
    'failed reload never restores previous configuration or permits selection',
    () async {
      await login();
      core.respond = (method, arguments) =>
          method == CoreMethod.managedLoadConfig
          ? managedSnapshot(
              phase: 'unavailable',
              reason: 'invalid_client_config',
              generation: core.account['generation'] as int,
            )
          : core.defaultResponse(method, arguments);
      await action.reloadConfiguration();
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNull,
      );
      expect(
        container.read(managedAccountProvider).errorCode,
        'invalid_client_config',
      );
      final count = core.calls.length;
      await action.selectProxy('VIP', 'Server-A');
      expect(core.calls.length, count);
    },
  );

  test(
    'cold start and logout clear active legacy references but preserve stored rows',
    () async {
      final legacy = Profile.normal(label: 'Legacy');
      container.read(profilesProvider.notifier).put(legacy);
      container.read(currentProfileIdProvider.notifier).value = legacy.id;
      container.read(currentPageLabelProvider.notifier).value =
          PageLabel.profiles;
      container.read(groupsProvider.notifier).value = [
        const Group(name: 'Old', type: GroupType.Selector),
      ];
      await login();
      expect(container.read(currentProfileIdProvider), isNull);
      expect(container.read(currentPageLabelProvider), PageLabel.dashboard);
      expect(container.read(groupsProvider), isEmpty);
      expect(container.read(profilesProvider).single.label, 'Legacy');
      await action.logout();
      expect(container.read(profilesProvider).single.label, 'Legacy');
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNull,
      );
    },
  );

  test(
    'every local import or export action rejects before invoking Core or a picker',
    () async {
      await action.coldStart();
      final profiles = container.read(profilesActionProvider.notifier);
      final backup = container.read(backupActionProvider.notifier);
      final setup = container.read(setupActionProvider.notifier);
      final calls = core.calls.length;
      for (final operation in <Future<dynamic> Function()>[
        profiles.addProfileFormFile,
        () => profiles.addProfileFormURL(
          'https://not-requested.invalid/config.yaml',
        ),
        profiles.addProfileFormQrCode,
        () => profiles.validateConfigWithData('proxies: []'),
        () => profiles.deleteProfile(123),
        () => profiles.clearEffect(123),
        profiles.updateProfiles,
        () => setup.getProfileWithId(123),
        backup.backup,
        () => backup.restore(RestoreOption.all),
      ]) {
        await expectLater(operation(), throwsStateError);
      }
      await profiles.autoUpdateProfiles();
      expect(core.calls.length, calls);
      expect(container.read(profilesProvider), isEmpty);
    },
  );

  test(
    'late selection cannot restore configuration after account removal',
    () async {
      await login();
      final generation = container
          .read(managedAccountProvider)
          .account
          .generation;
      final response = Completer<Object?>();
      core.respond = (method, arguments) =>
          method == CoreMethod.managedSelectProxy
          ? response.future
          : core.defaultResponse(method, arguments);
      final pending = action.selectProxy('VIP', 'Server-B');
      await action.selectProxy('VIP', 'Server-A');
      expect(
        core.calls.where(
          (call) => call.method == CoreMethod.managedSelectProxy,
        ),
        hasLength(1),
      );
      await action.logout();
      response.complete(
        managedSnapshot(phase: 'configuration_applied', generation: generation),
      );
      await pending;
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNull,
      );
      expect(container.read(managedAccountProvider).account.user, isNull);
    },
  );
}
