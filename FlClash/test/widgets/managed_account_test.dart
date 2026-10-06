import 'dart:async';

import 'package:fl_clash/l10n/l10n.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/pages/managed_account.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:material_ui/material_ui.dart';

import '../helpers/managed_fakes.dart';
import '../helpers/test_app.dart';

void main() {
  Future<void> nothing() async {}
  ManagedAccountView view({
    ManagedAccountState state = const ManagedAccountState(ready: true),
    Future<void> Function(String, String)? login,
    Future<void> Function()? refresh,
    Future<void> Function()? logout,
    Future<void> Function()? website,
    Future<void> Function()? exit,
    Future<void> Function()? authorizeTun,
  }) => ManagedAccountView(
    state: state,
    onLogin: login ?? (_, _) async {},
    onRefresh: refresh ?? nothing,
    onLogout: logout ?? nothing,
    onRetryCore: nothing,
    onExit: exit ?? nothing,
    onWebsite: website ?? nothing,
    onAuthorizeTun: authorizeTun,
  );

  testWidgets(
    'TUN permission stays explicit and duplicate authorization is blocked',
    (tester) async {
      final pending = Completer<void>();
      var calls = 0;
      final state = ManagedAccountState(
        ready: true,
        error: 'managed_tun_permission_required',
        account: ManagedAccountSnapshot.fromJson(
          managedSnapshot(phase: 'configuration_staged'),
        ),
      );
      await tester.pumpWidget(
        TestApp(
          child: view(
            state: state,
            authorizeTun: () {
              calls++;
              return pending.future;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      final strings = AppLocalizations.of(
        tester.element(find.byType(ManagedAccountView)),
      );
      expect(find.text(strings.managedTunPermission), findsOneWidget);
      expect(find.text(strings.managedStopped), findsOneWidget);
      expect(calls, 0);
      final button = find.byKey(const Key('managed-authorize-tun'));
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pump();
      await tester.tap(button);
      await tester.pump();
      expect(calls, 1);
      pending.completeError(StateError('synthetic authorization refusal'));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(tester.widget<OutlinedButton>(button).onPressed, isNotNull);
    },
  );

  testWidgets(
    'login form validates, suppresses duplicate submissions, and clears the password',
    (tester) async {
      var submissions = 0;
      final pending = Completer<void>();
      String? sentEmail;
      String? sentPassword;
      await tester.pumpWidget(
        TestApp(
          child: view(
            login: (email, password) {
              submissions++;
              sentEmail = email;
              sentPassword = password;
              return pending.future;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      final button = find.byKey(const Key('managed-login'));
      await tester.tap(button);
      await tester.pump();
      expect(submissions, 0);
      await tester.enterText(
        find.byKey(const Key('managed-email')),
        ' fixture@example.invalid ',
      );
      await tester.enterText(
        find.byKey(const Key('managed-password')),
        'do-not-store-this-fixture',
      );
      await tester.tap(button);
      await tester.pump();
      await tester.tap(button);
      await tester.pump();
      expect(submissions, 1);
      expect(sentEmail, 'fixture@example.invalid');
      expect(sentPassword, 'do-not-store-this-fixture');
      final password = tester.widget<TextFormField>(
        find.byKey(const Key('managed-password')),
      );
      expect(password.controller!.text, isEmpty);
      final editable = tester.widget<EditableText>(
        find.descendant(
          of: find.byKey(const Key('managed-password')),
          matching: find.byType(EditableText),
        ),
      );
      expect(editable.obscureText, isTrue);
      pending.complete();
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'a late request after disposing the page does not update a dead widget',
    (tester) async {
      final pending = Completer<void>();
      await tester.pumpWidget(
        TestApp(child: view(login: (_, _) => pending.future)),
      );
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('managed-email')),
        'fixture@example.invalid',
      );
      await tester.enterText(
        find.byKey(const Key('managed-password')),
        'fixture-password',
      );
      await tester.tap(find.byKey(const Key('managed-login')));
      await tester.pump();
      await tester.pumpWidget(const SizedBox());
      pending.complete();
      await tester.pump();
      expect(tester.takeException(), isNull);
    },
  );

  for (final reason in [
    'upgrade_required',
    'subscription_expired',
    'quota_exhausted',
    'device_limit',
  ]) {
    testWidgets(
      '$reason shows an account restriction and cannot pop into an old home route',
      (tester) async {
        final state = ManagedAccountState(
          ready: true,
          account: ManagedAccountSnapshot.fromJson(
            managedSnapshot(phase: 'restricted', reason: reason),
          ),
        );
        var refreshes = 0;
        var logouts = 0;
        await tester.pumpWidget(
          TestApp(
            child: view(
              state: state,
              refresh: () async {
                refreshes++;
              },
              logout: () async {
                logouts++;
              },
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('fixture@example.invalid'), findsOneWidget);
        expect(find.byKey(const Key('managed-login')), findsNothing);
        expect(tester.widget<PopScope>(find.byType(PopScope)).canPop, isFalse);
        final context = tester.element(find.byType(ManagedAccountView));
        final strings = AppLocalizations.of(context);
        await tester.ensureVisible(find.text(strings.managedCheck));
        await tester.tap(find.text(strings.managedCheck));
        await tester.pumpAndSettle();
        await tester.ensureVisible(find.byKey(const Key('managed-logout')));
        await tester.tap(find.byKey(const Key('managed-logout')));
        await tester.pumpAndSettle();
        expect(refreshes, 1);
        expect(logouts, 1);
        expect(state.account.canConnect, isFalse);
        expect(tester.takeException(), isNull);
      },
    );
  }

  for (final locale in [
    const Locale('en'),
    const Locale('zh', 'CN'),
    const Locale('ja'),
    const Locale('ru'),
  ]) {
    testWidgets('staged configuration fits a narrow screen in $locale', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(360, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final state = ManagedAccountState(
        ready: true,
        account: ManagedAccountSnapshot.fromJson(
          managedSnapshot(phase: 'configuration_staged'),
        ),
      );
      await tester.pumpWidget(
        TestApp(
          locale: locale,
          child: view(state: state),
        ),
      );
      await tester.pumpAndSettle();
      final strings = AppLocalizations.of(
        tester.element(find.byType(ManagedAccountView)),
      );
      expect(find.text(strings.managedStaged), findsOneWidget);
      expect(find.text(strings.managedStopped), findsOneWidget);
      expect(find.byKey(const Key('managed-login')), findsNothing);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'server error details are not rendered and website/exit actions stay available',
    (tester) async {
      var websites = 0;
      var exits = 0;
      await tester.pumpWidget(
        TestApp(
          child: view(
            state: const ManagedAccountState(
              ready: true,
              error: 'raw-private-token-error',
            ),
            website: () async {
              websites++;
            },
            exit: () async {
              exits++;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      final strings = AppLocalizations.of(
        tester.element(find.byType(ManagedAccountView)),
      );
      expect(find.text('raw-private-token-error'), findsNothing);
      expect(find.text(strings.managedRequestFailed), findsOneWidget);
      await tester.ensureVisible(find.text(strings.managedWebsite));
      await tester.tap(find.text(strings.managedWebsite));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text(strings.managedExitApp));
      await tester.tap(find.text(strings.managedExitApp));
      await tester.pumpAndSettle();
      expect(websites, 1);
      expect(exits, 1);
    },
  );
}
