import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/desktop/launcher.dart';
import 'package:fl_clash/core/desktop/lifecycle.dart';
import 'package:fl_clash/core/desktop/rpc_client.dart';
import 'package:fl_clash/core/desktop/transport.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/core/service.dart';
import 'package:fl_clash/database/database.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/main.dart' as application;
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/state.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:material_ui/material_ui.dart';
import 'package:proxy/proxy.dart' as system_proxy;
import 'package:shared_preferences/shared_preferences.dart';

class _IsolatedKeychain implements ManagedCredentialStore {
  _IsolatedKeychain(this.scope);
  final String scope;
  final _native = MacOSManagedCredentials();
  @override
  bool get supported => true;
  @override
  Future<RememberedManagedLogin?> read(String _) => _native.read(scope);
  @override
  Future<void> save(String _, RememberedManagedLogin login) =>
      _native.save(scope, login);
  @override
  Future<void> delete(String _) => _native.delete(scope);
}

class _DirectResolver implements DesktopCoreLauncherResolver {
  const _DirectResolver(this.launcher);
  final CoreProcessLauncher launcher;

  @override
  Future<CoreProcessLauncher> resolve() async => launcher;
}

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  const acceptanceRoot = String.fromEnvironment('ACCEPTANCE_DIR');
  VoidCallback? exitAction;
  if (kDebugMode && acceptanceRoot.startsWith('/tmp/flclash-acceptance-')) {
    Timer.periodic(const Duration(milliseconds: 100), (timer) {
      if (exitAction != null &&
          File('$acceptanceRoot/exit-request').existsSync()) {
        timer.cancel();
        exitAction!();
      }
    });
  }
  testWidgets('macOS application account and cold-start acceptance', (
    tester,
  ) async {
    const root = String.fromEnvironment('ACCEPTANCE_DIR');
    const corePath = String.fromEnvironment('ACCEPTANCE_CORE');
    expect(kDebugMode && Platform.isMacOS, isTrue);
    expect(root.startsWith('/tmp/flclash-acceptance-'), isTrue);
    expect(File('$root/fixture.json').existsSync(), isTrue);
    final fixture =
        jsonDecode(await File('$root/fixture.json').readAsString())
            as Map<String, dynamic>;
    final phase = (await File('$root/phase').readAsString()).trim();
    final keychain = _IsolatedKeychain('${fixture['api']}/api/v1/client');
    setManagedCredentialStoreForTesting(keychain);
    var preserveLoginForReopen = false;
    addTearDown(() async {
      if (!preserveLoginForReopen) await keychain.delete('');
      setManagedCredentialStoreForTesting(null);
    });
    await File('$root/app-pid.txt').writeAsString('$pid');
    final emails = Map<String, dynamic>.from(fixture['emails'] as Map);
    final password = fixture['password'] as String;
    final checks = <String>[];
    final proxyCommands = <List<String>>[];
    final home = Directory('$root/app');
    final temporary = Directory('$root/tmp');
    final cache = Directory('$root/cache');
    for (final directory in [home, temporary, cache]) {
      await directory.create(recursive: true);
    }
    AppPath.supportDirectory = () async => home;
    AppPath.temporaryDirectory = () async => temporary;
    AppPath.cacheDirectory = () async => cache;
    AppPath.downloadDirectory = () async => temporary;
    SharedPreferences.setPrefix('acceptance.${root.split('/').last}.');
    final stored = await SharedPreferences.getInstance();
    setProxyForTesting(
      system_proxy.Proxy(
        processRunner:
            (executable, arguments, {bool runInShell = false}) async {
              proxyCommands.add([executable, ...arguments]);
              return ProcessResult(0, 0, '', '');
            },
      ),
    );
    final launcher = DirectCoreLauncher(
      corePath: corePath,
      startProcess: (executable, arguments) async {
        final child = await Process.start(
          executable,
          arguments,
          environment: {'FLCLASH_ACCEPTANCE_API': fixture['api'] as String},
        );
        await File(
          '$root/core-pids.txt',
        ).writeAsString('${child.pid}\n', mode: FileMode.append, flush: true);
        return child;
      },
    );
    final lifecycle = phase == 'bundled'
        ? null
        : DesktopCoreLifecycle(
            transportFactory: () =>
                IPCCoreTransport(address: '$root/core.sock'),
            launcherResolver: _DirectResolver(launcher),
          );
    if (phase == 'bundled') {
      expect(File(unixSocketPath).existsSync(), isFalse);
    }
    final core = lifecycle == null
        ? CoreController()
        : CoreController.test(
            CoreService.forTesting(
              lifecycle: lifecycle,
              rpcClient: CoreRpcClient(lifecycle.transport),
            ),
          );
    const legacy =
        'mixed-port: 17993\nallow-lan: false\nrules: [MATCH,DIRECT]\n';
    if (phase != 'reopen') {
      final profile = Profile.normal(label: 'Old local acceptance profile');
      await preferences.setVersion(1);
      await preferences.saveConfig(
        Config(
          currentProfileId: profile.id,
          themeProps: defaultThemeProps,
          appSettingProps: const AppSettingProps(
            dashboardWidgets: [],
            autoRun: true,
            autoCheckUpdate: false,
            disclaimerAccepted: true,
            locale: 'en',
            minimizeOnExit: false,
          ),
        ),
      );
      await database.profilesDao.setAll([profile]);
      await Directory('$root/app/profiles').create(recursive: true);
      await File('$root/app/profiles/${profile.id}.yaml').writeAsString(legacy);
      await File('$root/app/config.yaml').writeAsString(legacy);
    }

    Future<void> pumpUntil(
      FutureOr<bool> Function() ready,
      String reason,
    ) async {
      final deadline = DateTime.now().add(const Duration(seconds: 30));
      while (!await ready()) {
        if (DateTime.now().isAfter(deadline)) {
          fail('Timed out: $reason');
        }
        await tester.pump(const Duration(milliseconds: 100));
      }
      await tester.pump(const Duration(milliseconds: 150));
    }

    ManagedAccountState state() =>
        globalState.container.read(managedAccountProvider);
    Future<Map<String, dynamic>> control(String command) async {
      final client = HttpClient();
      try {
        final uri = Uri.parse('${fixture['api']}/fixture/$command');
        final request = await client.getUrl(uri);
        request.headers.set(
          'X-Acceptance-Control',
          fixture['control'] as String,
        );
        final response = await request.close();
        final body = await utf8.decoder.bind(response).join();
        expect(response.statusCode, anyOf(200, 204));
        return body.isEmpty ? {} : jsonDecode(body) as Map<String, dynamic>;
      } finally {
        client.close(force: true);
      }
    }

    Future<void> screenshot(String name) async {
      await tester.pump(const Duration(milliseconds: 200));
      final boundaries = find.byType(RepaintBoundary).evaluate();
      for (final element in boundaries) {
        final render = element.renderObject;
        if (render is! RenderRepaintBoundary ||
            !render.hasSize ||
            render.size.width < 300 ||
            render.size.height < 300) {
          continue;
        }
        final image = await render.toImage(pixelRatio: 1);
        final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
        if (bytes != null) {
          await File(
            '$root/$name.png',
          ).writeAsBytes(bytes.buffer.asUint8List());
        }
        image.dispose();
        break;
      }
    }

    Future<void> closedGate(String name) async {
      expect(state().account.canConnect, isFalse);
      expect(await core.startListener(), isFalse);
      expect(await File('$root/app/config.yaml').readAsString(), legacy);
      expect(globalState.container.read(isStartProvider), isFalse);
      expect(await core.getConnections(), isEmpty);
      checks.add(name);
      await File('$root/progress-$phase.json').writeAsString(
        jsonEncode({
          'phase': phase,
          'checks': checks,
          'can_connect': state().account.canConnect,
        }),
      );
    }

    final managedFile = File('$root/app/asterlink-managed-v1/active.json');
    Future<void> managedStorage(bool present) async {
      expect(await managedFile.exists(), present);
      if (!present) {
        expect(state().account.configuration, isNull);
        return;
      }
      final record = jsonDecode(await managedFile.readAsString()) as Map;
      final account = state().account;
      expect(record['id'], account.configuration!.id);
      expect(record['version'], account.profileVersion);
      expect(record['owner'], {
        'generation': account.generation,
        'user_id': account.user!.id,
        'session_id': account.session!.id,
      });
      expect((await managedFile.stat()).mode & 0x1ff, 0x180);
      expect(record['yaml'], contains('Acceptance-Loopback'));
      for (final port in [17993, 17995, 17996]) {
        final probe = await ServerSocket.bind(
          InternetAddress.loopbackIPv4,
          port,
        );
        await probe.close();
      }
    }

    Future<void> submit(String email, String secret) async {
      await tester.enterText(find.byKey(const Key('managed-email')), email);
      await tester.enterText(find.byKey(const Key('managed-password')), secret);
      await tester.ensureVisible(find.byKey(const Key('managed-login')));
      await tester.tap(find.byKey(const Key('managed-login')));
      await tester.pump();
    }

    Future<void> logout() async {
      globalState.container
          .read(currentPageLabelProvider.notifier)
          .toPage(PageLabel.dashboard);
      await tester.pump(const Duration(milliseconds: 350));
      await tester.ensureVisible(find.byKey(const Key('managed-logout')));
      await tester.tap(find.byKey(const Key('managed-logout')));
      await pumpUntil(
        () => state().ready && !state().busy && state().account.user == null,
        'logout',
      );
    }

    final testErrorHandler = FlutterError.onError;
    application.main([]);
    FlutterError.onError = testErrorHandler;
    await pumpUntil(
      () => find.byKey(const Key('managed-email')).evaluate().isNotEmpty,
      'real Application bootstrap/login entry',
    );
    await pumpUntil(() => state().ready && !state().busy, 'Core reset');
    final running = await core.start();
    expect(running.session, isNotNull);
    await File(
      '$root/core-pids.txt',
    ).writeAsString('${running.session!.pid}\n', mode: FileMode.append);
    expect(globalState.container.read(appSettingProvider).autoRun, isTrue);
    await closedGate('cold start with saved profile and autoRun');
    await managedStorage(false);
    expect(globalState.container.read(currentProfileIdProvider), isNull);
    await screenshot('login-$phase');
    if (phase == 'bundled') {
      checks.add(
        'packaged production Core boots through the default controller and native IPC',
      );
    } else if (phase == 'first') {
      await submit(emails['free'] as String, 'wrong-password');
      await pumpUntil(() => !state().busy, 'wrong password');
      expect(state().errorCode, 'invalid_credentials');
      expect(state().account.user, isNull);
      expect(find.text(password), findsNothing);
      await closedGate('wrong password stays signed out');

      await control('delay');
      await submit(emails['vip'] as String, password);
      await pumpUntil(() => state().busy, 'delayed login');
      var before = await control('state');
      final requestDeadline = DateTime.now().add(const Duration(seconds: 2));
      while (((before['requests'] as Map)['/api/v1/client/login'] as int? ??
              0) <
          2) {
        expect(DateTime.now().isBefore(requestDeadline), isTrue);
        await tester.pump(const Duration(milliseconds: 50));
        before = await control('state');
      }
      await tester.tap(
        find.byKey(const Key('managed-login')),
        warnIfMissed: false,
      );
      await tester.pump();
      await logout();
      await tester.pump(const Duration(seconds: 4));
      expect(state().account.user, isNull);
      final after = await control('state');
      expect(
        (after['requests'] as Map)['/api/v1/client/login'],
        (before['requests'] as Map)['/api/v1/client/login'],
      );
      await closedGate('duplicate and cancelled late login rejected');

      for (final entry in {
        'free': 'upgrade_required',
        'expired': 'subscription_expired',
        'quota': 'quota_exhausted',
      }.entries) {
        await submit(emails[entry.key] as String, password);
        await pumpUntil(
          () => !state().busy && state().account.user != null,
          entry.key,
        );
        expect(state().errorCode, entry.value);
        await closedGate('${entry.key} account restricted');
        if (entry.key == 'free') await screenshot('free');
        await logout();
      }
      await submit(emails['vip'] as String, password);
      await pumpUntil(
        () =>
            !state().busy &&
            state().account.phase == ManagedPhase.configurationApplied,
        'VIP configuration applied',
      );
      await closedGate(
        'VIP applies server configuration to Mihomo but cannot connect',
      );
      await screenshot('vip');
      await managedStorage(true);
      final original = state().account;
      final accountAction = globalState.container.read(
        managedAccountProvider.notifier,
      );
      final labels = globalState.container
          .read(navigationStateProvider)
          .navigationItems
          .map((item) => item.label);
      expect(labels, [PageLabel.dashboard, PageLabel.proxies, PageLabel.tools]);
      globalState.container
          .read(currentPageLabelProvider.notifier)
          .toPage(PageLabel.proxies);
      await tester.pump(const Duration(milliseconds: 350));
      await tester.tap(
        find.byKey(const ValueKey('managed-node-VIP-Acceptance-Alternate')),
      );
      await pumpUntil(
        () =>
            !state().busy &&
            state().account.configuration!.groups.single.selected ==
                'Acceptance-Alternate',
        'server node selection',
      );
      await screenshot('managed-nodes');
      await managedStorage(true);
      await expectLater(
        core.managedSelectProxy(
          generation: original.generation,
          configurationId: original.configuration!.id,
          group: 'VIP',
          proxy: 'imported-node',
        ),
        throwsA(isA<CoreMethodException>()),
      );
      await expectLater(
        core.managedSelectProxy(
          generation: original.generation,
          configurationId: 'old-config-id',
          group: 'VIP',
          proxy: 'Acceptance-Loopback',
        ),
        throwsA(isA<CoreMethodException>()),
      );
      checks.add(
        'real node UI selects only server members with the current owner and configuration ID',
      );
      globalState.container
          .read(currentPageLabelProvider.notifier)
          .toPage(PageLabel.tools);
      await tester.pump(const Duration(milliseconds: 350));
      expect(find.text('Theme'), findsOneWidget);
      for (final text in [
        'Backup and restore',
        'Basic configuration',
        'Advanced configuration',
      ]) {
        expect(find.text(text), findsNothing);
      }
      await screenshot('managed-preferences');
      globalState.container
          .read(currentPageLabelProvider.notifier)
          .toPage(PageLabel.dashboard);
      await tester.pump(const Duration(milliseconds: 350));

      await control('config-invalid');
      await accountAction.reloadConfiguration();
      await pumpUntil(
        () => !state().busy && state().account.configuration == null,
        'semantic failure',
      );
      expect(state().errorCode, 'invalid_client_config');
      await managedStorage(false);
      await closedGate(
        'semantic failure clears disk and kernel ownership without legacy fallback',
      );
      await control('config-valid');
      await accountAction.reloadConfiguration();
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'recover valid server configuration',
      );
      expect(
        state().account.configuration!.id,
        isNot(original.configuration!.id),
      );
      await managedStorage(true);
      await control('config-missing');
      await accountAction.reloadConfiguration();
      await pumpUntil(
        () => !state().busy && state().account.configuration == null,
        'missing server configuration',
      );
      await managedStorage(false);
      await closedGate(
        'missing server configuration never falls back to a saved local profile',
      );
      await control('config-changed');
      await accountAction.reloadConfiguration();
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'changed server version',
      );
      expect(
        state().account.configuration!.version,
        isNot(original.configuration!.version),
      );
      expect(
        state().account.configuration!.groups.single.proxies,
        contains('Acceptance-Rotated'),
      );
      await managedStorage(true);
      checks.add(
        'configuration replacement rotates identity and exposes the new server version',
      );

      final beforeConfig = await control('state');
      final configCount =
          (beforeConfig['requests'] as Map)['/api/v1/client/config'] as int;
      await control('delay-config');
      final delayedLoad = accountAction.reloadConfiguration();
      await pumpUntil(() async {
        final current = await control('state');
        return ((current['requests'] as Map)['/api/v1/client/config'] as int) >
            configCount;
      }, 'configuration request reached the isolated backend');
      expect(state().account.configuration, isNull);
      await logout();
      await delayedLoad;
      await managedStorage(false);
      await control('config-valid');
      await submit(emails['vip2'] as String, password);
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'second VIP account',
      );
      expect(state().account.user!.id, isNot(original.user!.id));
      await managedStorage(true);
      await expectLater(
        core.managedSelectProxy(
          generation: state().account.generation,
          configurationId: original.configuration!.id,
          group: 'VIP',
          proxy: 'Acceptance-Loopback',
        ),
        throwsA(isA<CoreMethodException>()),
      );
      await closedGate(
        'cancelled configuration cannot cross logout or account ownership',
      );
      final beforeId = await globalState.container.read(
        managedInstallationIdProvider.future,
      );
      await File('$root/installation-id.txt').writeAsString(beforeId);
      await expectLater(
        core.setupConfig(
          params: const SetupParams(selectedMap: {}, testUrl: ''),
        ),
        throwsA(isA<CoreMethodException>()),
      );
      await globalState.navigatorKey.currentState!.maybePop();
      await tester.pump();
      await closedGate('back navigation and direct legacy setup remain denied');
      await control('expire');
      await globalState.container
          .read(managedAccountProvider.notifier)
          .refresh();
      await pumpUntil(
        () => state().account.user == null && !state().busy,
        'server expiration',
      );
      await closedGate('server-expired session cleared');
      await managedStorage(false);
      await submit(emails['vip'] as String, password);
      await pumpUntil(
        () =>
            state().account.phase == ManagedPhase.configurationApplied &&
            !state().busy,
        'login before Core restart',
      );
      final container = globalState.container;
      container
          .read(networkSettingProvider.notifier)
          .update((value) => value.copyWith(systemProxy: false));
      final reservation = await ServerSocket.bind(
        InternetAddress.loopbackIPv4,
        0,
      );
      final mixedPort = reservation.port;
      await reservation.close();
      container
          .read(patchClashConfigProvider.notifier)
          .update((value) => value.copyWith(mixedPort: mixedPort));
      await tester.pump(const Duration(milliseconds: 200));
      await tester.ensureVisible(find.byKey(const Key('managed-connect')));
      await tester.tap(find.byKey(const Key('managed-connect')));
      await pumpUntil(
        () =>
            !state().busy &&
            state().account.canConnect &&
            container.read(isStartProvider),
        'fresh metering authorization and real listener',
      );
      Future<void> transfer() async {
        final client = HttpClient()
          ..findProxy = (_) => 'PROXY 127.0.0.1:$mixedPort';
        try {
          final request = await client.postUrl(
            Uri.parse(fixture['traffic_target'] as String),
          );
          request.contentLength = 24576;
          request.add(List<int>.filled(24576, 65));
          final response = await request.close().timeout(
            const Duration(seconds: 5),
          );
          expect(response.statusCode, 200);
          expect(
            await response.fold<int>(0, (sum, bytes) => sum + bytes.length),
            32768,
          );
        } finally {
          client.close(force: true);
        }
      }

      await transfer();
      final minuteDeadline = DateTime.now().add(const Duration(seconds: 75));
      while ((state().account.metering?.sequence ?? 0) < 2) {
        expect(
          DateTime.now().isBefore(minuteDeadline),
          isTrue,
          reason: 'real minute report did not confirm',
        );
        await tester.pump(const Duration(milliseconds: 200));
      }
      var ledger = await control('state');
      var metering = state().account.metering!;
      expect(metering.acknowledgedUpload, greaterThanOrEqualTo(24576));
      expect(metering.acknowledgedDownload, greaterThanOrEqualTo(32768));
      expect(ledger['upload_bytes'], metering.acknowledgedUpload);
      expect(ledger['download_bytes'], metering.acknowledgedDownload);
      expect(
        ledger['charged_units'],
        (metering.acknowledgedUpload + metering.acknowledgedDownload) * 1000,
      );
      await screenshot('metered-connected');
      core.resetTraffic();
      await tester.pump(const Duration(milliseconds: 150));
      await transfer();
      await tester.ensureVisible(find.byKey(const Key('managed-disconnect')));
      await tester.tap(find.byKey(const Key('managed-disconnect')));
      await pumpUntil(
        () =>
            !state().busy &&
            !state().account.canConnect &&
            !container.read(isStartProvider),
        'disconnect and final settlement',
      );
      metering = state().account.metering!;
      ledger = await control('state');
      expect(metering.pending, isFalse);
      expect(ledger['upload_bytes'], metering.upload);
      expect(ledger['download_bytes'], metering.download);
      expect(
        ledger['charged_units'],
        (metering.upload + metering.download) * 1000,
      );
      expect(metering.upload, greaterThanOrEqualTo(2 * 24576));
      expect(metering.download, greaterThanOrEqualTo(2 * 32768));
      await closedGate(
        'real App explicit disconnect closes the listener and confirms PostgreSQL final traffic',
      );
      checks.add(
        'real App TCP transfer -> Mihomo cumulative counters -> actual 60-second report -> Gin/PostgreSQL exact byte and charge ledger',
      );
      checks.add(
        'display reset cannot reset billing; second real transfer is included in final settlement',
      );
      final oldSession = await lifecycle!.waitUntilRunning(
        const Duration(seconds: 5),
      );
      Future<void> reconnect() async {
        await tester.ensureVisible(find.byKey(const Key('managed-connect')));
        await tester.tap(find.byKey(const Key('managed-connect')));
        await pumpUntil(
          () =>
              !state().busy &&
              state().account.canConnect &&
              container.read(isStartProvider),
          'explicit reconnect with fresh server authorization',
        );
      }

      Future<void> stopped(String reason) async {
        await pumpUntil(
          () =>
              !state().busy &&
              !state().account.canConnect &&
              !container.read(isStartProvider),
          reason,
        );
        await expectLater(
          Socket.connect(
            InternetAddress.loopbackIPv4,
            mixedPort,
            timeout: const Duration(seconds: 1),
          ).then((socket) {
            socket.destroy();
            return socket;
          }),
          throwsA(isA<SocketException>()),
        );
        final disconnect = find.byKey(const Key('managed-disconnect'));
        if (disconnect.evaluate().isNotEmpty) {
          expect(tester.widget<OutlinedButton>(disconnect).onPressed, isNull);
        }
      }

      await reconnect();
      await transfer();
      await control('lose-traffic-response');
      await expectLater(
        core.managedFlush(state().account.generation),
        throwsA(isA<CoreMethodException>()),
      );
      await stopped('lost report response closes real listener and UI');
      final committed = await control('state');
      expect(committed['dropped_responses'], 1);
      await core.managedFlush(state().account.generation);
      await pumpUntil(
        () => state().account.metering?.pending == false,
        'lost response retry acknowledged',
      );
      final replayed = await control('state');
      expect(replayed['charged_units'], committed['charged_units']);
      expect(replayed['upload_bytes'], committed['upload_bytes']);
      expect(replayed['download_bytes'], committed['download_bytes']);
      await stopped('acknowledged replay must not reconnect automatically');
      checks.add(
        'committed report with lost TCP response closes App listener; retry acknowledges without double billing or automatic reconnect',
      );

      await reconnect();
      await transfer();
      final beforeOutage = await control('state');
      await control('unavailable');
      await expectLater(
        core.managedFlush(state().account.generation),
        throwsA(isA<CoreMethodException>()),
      );
      await stopped('backend connection loss fails closed');
      expect(
        (await control('state'))['charged_units'],
        beforeOutage['charged_units'],
      );
      await control('available');
      await core.managedFlush(state().account.generation);
      await pumpUntil(
        () => state().account.metering?.pending == false,
        'outage tail acknowledged',
      );
      expect(
        (await control('state'))['charged_units'] as int,
        greaterThan(beforeOutage['charged_units'] as int),
      );
      await stopped('backend recovery still requires explicit user connection');
      checks.add(
        'backend TCP outage stops actual listener and UI; pending cumulative traffic settles after recovery without implicit connection',
      );

      await reconnect();
      await transfer();
      await control('config-changed');
      await core.managedFlush(state().account.generation);
      await stopped('running configuration replacement closes connection');
      await accountAction.reloadConfiguration();
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'replacement configuration loaded',
      );
      expect(
        state().account.configuration!.groups.single.proxies,
        contains('Acceptance-Rotated'),
      );
      expect(container.read(isStartProvider), isFalse);
      await reconnect();
      await tester.tap(find.byKey(const Key('managed-disconnect')));
      await stopped('replacement configuration explicit disconnect');
      await control('config-valid');
      checks.add(
        'configuration change while carrying real traffic stops connection, exposes replacement nodes, and requires explicit reconnect',
      );

      for (final scenario in ['quota', 'subscription']) {
        if (state().account.user != null) await logout();
        await submit(emails['fault'] as String, password);
        await pumpUntil(
          () => !state().busy && state().account.configuration != null,
          'fault account configuration',
        );
        await reconnect();
        await transfer();
        await control(
          scenario == 'quota' ? 'quota-empty' : 'subscription-expire',
        );
        await core.managedFlush(state().account.generation);
        await stopped('running $scenario denial');
        final deniedLedger = await control('state');
        expect(
          deniedLedger['charged_units'],
          ((deniedLedger['upload_bytes'] as int) +
                  (deniedLedger['download_bytes'] as int)) *
              1000,
        );
        await screenshot('denied-$scenario');
        await control(
          scenario == 'quota' ? 'quota-renew' : 'subscription-renew',
        );
        await accountAction.refresh();
        await pumpUntil(() => !state().busy, 'renewed $scenario status');
        expect(container.read(isStartProvider), isFalse);
        checks.add(
          'running $scenario denial closes listener/UI and settles exact traffic; entitlement renewal alone never reconnects',
        );
      }

      if (state().account.user != null) await logout();
      await submit(emails['vip'] as String, password);
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'VIP before server refusal',
      );
      await reconnect();
      await transfer();
      await core.managedFlush(state().account.generation);
      await control('expire');
      await expectLater(
        core.managedFlush(state().account.generation),
        throwsA(isA<CoreMethodException>()),
      );
      await stopped('server rejects active session');
      checks.add(
        'actual server session refusal stops App listener and connected UI',
      );

      if (state().account.user != null) await logout();
      await submit(emails['vip'] as String, password);
      await pumpUntil(
        () => !state().busy && state().account.configuration != null,
        'VIP before abnormal Core exit',
      );
      await reconnect();
      await transfer();
      await core.managedFlush(state().account.generation);
      expect(Process.killPid(oldSession.pid, ProcessSignal.sigkill), isTrue);
      await pumpUntil(
        () => state().account.user == null && !container.read(isStartProvider),
        'actual Core SIGKILL clears connected state',
      );
      await control('expire');
      checks.add(
        'owned Core SIGKILL clears App account/connection; fixture expires orphan server session before controlled restart',
      );
      await globalState.container
          .read(coreActionProvider.notifier)
          .restartCore();
      await pumpUntil(
        () => state().ready && !state().busy && state().account.user == null,
        'Core restart forces login',
      );
      final newSession = await lifecycle.waitUntilRunning(
        const Duration(seconds: 5),
      );
      expect(newSession.pid, isNot(oldSession.pid));
      await closedGate('actual Core restart clears account');
      await managedStorage(false);
      await tester.ensureVisible(find.byKey(const Key('managed-remember')));
      await tester.tap(find.byKey(const Key('managed-remember')));
      await tester.pump();
      await submit(emails['free'] as String, password);
      await pumpUntil(
        () => !state().busy && state().account.user != null,
        'login before normal application exit',
      );
      await pumpUntil(
        () async => (await keychain.read(''))?.email == emails['free'],
        'native Keychain remember after successful authentication',
      );
      checks.add(
        'explicit remember stores credentials in the isolated macOS Keychain scope',
      );
    } else {
      expect(
        await globalState.container.read(managedInstallationIdProvider.future),
        await File('$root/installation-id.txt').readAsString(),
      );
      checks.add('new App process preserves installation UUID but not account');
      await pumpUntil(
        () =>
            tester
                .widget<TextFormField>(find.byKey(const Key('managed-email')))
                .controller!
                .text ==
            emails['free'],
        'Keychain prefill after a new application process',
      );
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('managed-password')))
            .controller!
            .text,
        password,
      );
      expect(state().account.user, isNull);
      expect(state().account.canConnect, isFalse);
      checks.add(
        'Keychain survives App reopen but cached credentials never authorize or auto-connect',
      );
      await submit(emails['vip'] as String, password);
      await pumpUntil(
        () =>
            state().account.phase == ManagedPhase.configurationApplied &&
            !state().busy,
        'VIP after App reopen',
      );
      await closedGate('reopened App requires successful new login');
      await logout();
      await closedGate('logout clears account and stays closed');
      expect(await keychain.read(''), isNull);
      checks.add('explicit logout removes the scoped Keychain entry');
      await managedStorage(false);
    }
    await stored.reload();
    final serialized = jsonEncode({
      for (final key in stored.getKeys()) key: stored.get(key),
    });
    expect(serialized.contains(password), isFalse);
    expect(serialized.contains('"token"'), isFalse);
    expect(serialized.contains('Acceptance-Loopback'), isFalse);
    final stats = await control('state');
    if (phase == 'bundled') {
      expect(stats['reports'], 0);
      expect((stats['requests'] as Map)['/api/v1/client/traffic'] ?? 0, 0);
    } else {
      expect(stats['reports'] as int, greaterThanOrEqualTo(3));
      expect(
        stats['charged_units'],
        ((stats['upload_bytes'] as int) + (stats['download_bytes'] as int)) *
            1000,
      );
    }
    expect(
      proxyCommands.every(
        (command) =>
            command.length == 2 && command.last == '-listallnetworkservices',
      ),
      isTrue,
    );
    checks.add(
      'no plaintext credentials in preferences or real system proxy mutation; only actual transfers are billed',
    );
    expect(tester.takeException(), isNull);
    await File('$root/result-$phase.json').writeAsString(
      jsonEncode({
        'phase': phase,
        'checks': checks,
        'backend': stats,
        'passed': true,
      }),
    );
    binding.reportData = {'phase': phase, 'checks': checks};
    await File('$root/app-pid.txt').writeAsString('$pid');
    final exitLabel = currentAppLocalizations.managedExitApp;
    await tester.ensureVisible(find.text(exitLabel));
    expect(
      tester
          .widget<TextButton>(find.widgetWithText(TextButton, exitLabel))
          .onPressed,
      isNotNull,
    );
    preserveLoginForReopen = phase == 'first';
    final exit = globalState.container.read(systemActionProvider.notifier);
    exitAction = () => unawaited(exit.handleExit());
  });
}
