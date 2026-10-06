import 'dart:io';

import 'package:fl_clash/common/path.dart';
import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/common/system.dart';
import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/desktop/launcher.dart';
import 'package:fl_clash/core/desktop/lifecycle.dart';
import 'package:fl_clash/core/desktop/model.dart';
import 'package:fl_clash/core/desktop/rpc_client.dart';
import 'package:fl_clash/core/desktop/transport.dart';
import 'package:fl_clash/core/service.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/manager/core_manager.dart';
import 'package:fl_clash/pages/managed_account.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:material_ui/material_ui.dart';
import 'package:rust_api/rust_api.dart';
import 'package:window_manager/window_manager.dart';

import '../test/helpers/test_app.dart';

class _DirectResolver implements DesktopCoreLauncherResolver {
  const _DirectResolver(this.launcher);
  final CoreProcessLauncher launcher;

  @override
  Future<CoreProcessLauncher> resolve() async => launcher;
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('native managed Core survives repeated in-app restarts', (
    tester,
  ) async {
    await windowManager.ensureInitialized();
    await windowManager.show();
    await tester.pumpWidget(const SizedBox());
    expect(Platform.isMacOS, isTrue);
    final root = await Directory('/tmp').createTemp('flclash-restart-');
    AppPath.supportDirectory = () async => root;
    AppPath.temporaryDirectory = () async => root;
    AppPath.cacheDirectory = () async => root;
    await RustLib.init();
    final lifecycle = DesktopCoreLifecycle(
      transportFactory: () =>
          IPCCoreTransport(address: '${root.path}/core.sock'),
      launcherResolver: _DirectResolver(DirectCoreLauncher()),
    );
    final core = CoreController.scoped(
      CoreService.forTesting(
        lifecycle: lifecycle,
        rpcClient: CoreRpcClient(lifecycle.transport),
      ),
    );
    final container = ProviderContainer(
      overrides: [
        coreHandlerProvider.overrideWithValue(core),
        managedCredentialStoreProvider.overrideWithValue(
          MacOSManagedCredentials(platformSupported: false),
        ),
      ],
    );
    const authorize = bool.fromEnvironment('RESTART_AUTHORIZE_TEST');
    const localAuthorize = bool.fromEnvironment('RESTART_LOCAL_AUTHORIZE_TEST');
    var expectedRoot = false;
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      container.dispose();
      await core.close();
      await root.delete(recursive: true);
      if (localAuthorize && expectedRoot) {
        await File(appPath.corePath).delete();
      }
    });
    container.read(managedAccountProvider);
    final action = container.read(coreActionProvider.notifier);
    final failures = <String>[];
    container.listen(managedAccountProvider, (_, next) {
      if (next.errorCode.isNotEmpty) failures.add(next.errorCode);
    });
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const TestApp(child: CoreManager(child: ManagedAccountPanel())),
      ),
    );

    Future<void> assertReady(String phase) async {
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!container.read(managedAccountProvider).ready &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      final account = container.read(managedAccountProvider);
      debugPrint(
        '$phase: lifecycle=${lifecycle.state.phase} '
        'status=${container.read(coreStatusProvider)} '
        'ready=${account.ready} error=${account.errorCode}',
      );
      expect(container.read(coreStatusProvider), CoreStatus.connected);
      expect(account.ready, isTrue, reason: phase);
      expect(account.errorCode, isEmpty, reason: phase);
      expect(account.account.user, isNull);
      expect(account.account.canConnect, isFalse);
      expect(await core.isInit, isTrue);
      final session = (lifecycle.state as DesktopCoreRunning).session;
      final identity = await Process.run('/bin/ps', [
        '-p',
        '${session.pid}',
        '-o',
        'uid=,ruid=',
      ]);
      expect(identity.exitCode, 0);
      final uids = identity.stdout.toString().trim().split(RegExp(r'\s+'));
      expect(uids, hasLength(2));
      expect(uids[0] == '0', expectedRoot);
      expect(uids[1], isNot('0'));
      debugPrint('$phase: effective_uid=${uids[0]} real_uid=${uids[1]}');
    }

    await action.startCore();
    await assertReady('cold start');
    if (localAuthorize) {
      expect(authorize, isFalse);
      expect(await system.authorizeCore(), AuthorizeCode.success);
      expectedRoot = true;
    }
    if (authorize) {
      expect(Platform.environment['GITHUB_ACTIONS'], 'true');
      for (final command in [
        ['/usr/sbin/chown', 'root:admin', appPath.corePath],
        ['/bin/chmod', '6755', appPath.corePath],
      ]) {
        final result = await Process.run('/usr/bin/sudo', ['-n', ...command]);
        expect(result.exitCode, 0, reason: result.stderr.toString());
      }
      expectedRoot = true;
    }
    for (var attempt = 1; attempt <= 4; attempt++) {
      if (expectedRoot) {
        final previous = (lifecycle.state as DesktopCoreRunning).session.pid;
        final button = find.byKey(const Key('managed-authorize-tun'));
        await tester.pump();
        await tester.ensureVisible(button);
        await tester.tap(button);
        final deadline = DateTime.now().add(const Duration(seconds: 30));
        while (DateTime.now().isBefore(deadline)) {
          if (lifecycle.state case DesktopCoreRunning(:final session)
              when session.pid != previous &&
                  container.read(managedAccountProvider).ready) {
            break;
          }
          await Future<void>.delayed(const Duration(milliseconds: 50));
        }
        expect(
          (lifecycle.state as DesktopCoreRunning).session.pid,
          isNot(previous),
        );
      } else {
        final restart = action.restartCore();
        expect(container.read(managedAccountProvider).busy, isTrue);
        expect(await restart, isTrue);
      }
      await assertReady('restart $attempt');
    }
    expect(
      failures,
      isEmpty,
      reason: 'normal restart must not publish a failure',
    );
  });
}
