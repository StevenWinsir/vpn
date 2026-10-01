import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/pages/home.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/views/managed_preferences.dart';
import 'package:fl_clash/views/managed_proxies.dart';
import 'package:fl_clash/views/navigation.dart';
import 'package:fl_clash/state.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:material_ui/material_ui.dart';

import '../helpers/managed_fakes.dart';
import '../helpers/test_app.dart';

class _Account extends ManagedAccount {
  _Account({this.applied = true, this.busy = false});
  final bool applied;
  final bool busy;
  final selections = <(String, String)>[];
  @override
  ManagedAccountState build() => ManagedAccountState(
    ready: true,
    working: busy,
    account: ManagedAccountSnapshot.fromJson(
      managedSnapshot(
        phase: applied ? 'configuration_applied' : 'profile_required',
      ),
    ),
  );
  @override
  Future<void> selectProxy(String group, String proxy) async {
    selections.add((group, proxy));
  }
}

class _ExitRecorder extends SystemAction {
  int attempts = 0;
  @override
  Future<void> handleClose([bool exit = true]) async {
    attempts++;
  }

  @override
  Future<void> handleExit([bool needSave = true]) async {
    attempts++;
  }
}

void main() {
  for (final locale in [
    const Locale('en'),
    const Locale('zh', 'CN'),
    const Locale('ja'),
    const Locale('ru'),
  ]) {
    testWidgets(
      'managed nodes render and select at 360px in $locale without import controls',
      (tester) async {
        tester.view.physicalSize = const Size(360, 720);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final account = _Account();
        final container = ProviderContainer(
          overrides: [managedAccountProvider.overrideWith(() => account)],
        );
        addTearDown(container.dispose);
        await tester.pumpWidget(
          UncontrolledProviderScope(
            container: container,
            child: TestApp(locale: locale, child: const ManagedProxiesView()),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('Server-A'), findsOneWidget);
        expect(find.text('Server-B'), findsOneWidget);
        await tester.tap(
          find.byKey(const ValueKey('managed-node-VIP-Server-B')),
        );
        await tester.pump();
        expect(account.selections, [('VIP', 'Server-B')]);
        expect(find.byType(TextField), findsNothing);
        expect(find.byIcon(Icons.add), findsNothing);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      },
    );
  }

  testWidgets(
    'managed home back navigation returns to account without exiting',
    (tester) async {
      navigationPort = navigation;
      addTearDown(() => navigationPort = null);
      final exit = _ExitRecorder();
      final container = ProviderContainer(
        overrides: [
          managedAccountProvider.overrideWith(() => _Account()),
          systemActionProvider.overrideWith(() => exit),
        ],
      );
      addTearDown(container.dispose);
      globalState.container = container;
      container.read(viewSizeProvider.notifier).value = const Size(800, 600);
      container
          .read(currentPageLabelProvider.notifier)
          .toPage(PageLabel.proxies);
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: const TestApp(child: HomePage()),
        ),
      );
      await tester.pumpAndSettle();
      await globalState.navigatorKey.currentState!.maybePop();
      await tester.pumpAndSettle();
      expect(container.read(currentPageLabelProvider), PageLabel.dashboard);
      expect(exit.attempts, 0);
      expect(
        container.read(managedAccountProvider).account.configuration,
        isNotNull,
      );
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('busy account disables every server-node selection', (
    tester,
  ) async {
    final account = _Account(busy: true);
    await tester.pumpWidget(
      TestApp(
        overrides: [managedAccountProvider.overrideWith(() => account)],
        child: const ManagedProxiesView(),
      ),
    );
    await tester.pumpAndSettle();
    final node = find.byKey(const ValueKey('managed-node-VIP-Server-B'));
    expect(tester.widget<ListTile>(node).enabled, isFalse);
    await tester.tap(node);
    await tester.pump();
    expect(account.selections, isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('no applied configuration means no selectable nodes', (
    tester,
  ) async {
    await tester.pumpWidget(
      TestApp(
        overrides: [
          managedAccountProvider.overrideWith(() => _Account(applied: false)),
        ],
        child: const ManagedProxiesView(),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Server-A'), findsNothing);
    expect(find.byType(ListTile), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final width in [390.0, 1200.0]) {
    testWidgets(
      'only account, server nodes and safe preferences are reachable at $width',
      (tester) async {
        navigationPort = navigation;
        addTearDown(() => navigationPort = null);
        tester.view.physicalSize = Size(width, 800);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final container = ProviderContainer(
          overrides: [managedAccountProvider.overrideWith(() => _Account())],
        );
        addTearDown(container.dispose);
        globalState.container = container;
        container.read(viewSizeProvider.notifier).value = Size(width, 800);
        container.read(currentPageLabelProvider.notifier).value =
            PageLabel.profiles;
        await tester.pumpWidget(
          UncontrolledProviderScope(
            container: container,
            child: const TestApp(includeNavigatorKey: false, child: HomePage()),
          ),
        );
        await tester.pumpAndSettle();
        expect(container.read(currentPageLabelProvider), PageLabel.dashboard);
        expect(
          container
              .read(navigationStateProvider)
              .navigationItems
              .map((item) => item.label),
          [PageLabel.dashboard, PageLabel.proxies, PageLabel.tools],
        );
        container
            .read(currentPageLabelProvider.notifier)
            .toPage(PageLabel.tools);
        await tester.pumpAndSettle();
        expect(find.byType(ManagedPreferencesView), findsOneWidget);
        expect(find.text('Backup and restore'), findsNothing);
        expect(find.text('Basic configuration'), findsNothing);
        expect(find.text('Advanced configuration'), findsNothing);
        expect(find.text('Theme'), findsOneWidget);
        expect(find.byIcon(Icons.folder), findsNothing);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      },
    );
  }
}
