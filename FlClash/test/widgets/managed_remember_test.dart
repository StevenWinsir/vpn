import 'dart:async';

import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/pages/managed_account.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:material_ui/material_ui.dart';

import '../helpers/test_app.dart';

class _Store implements ManagedCredentialStore {
  final result = Completer<RememberedManagedLogin?>();
  final deleted = <String>[];
  @override
  bool get supported => true;
  @override
  Future<RememberedManagedLogin?> read(String _) => result.future;
  @override
  Future<void> save(String _, RememberedManagedLogin _) async {}
  @override
  Future<void> delete(String scope) async {
    deleted.add(scope);
  }
}

void main() {
  const scope = 'https://login.example.invalid/api/v1/client';
  const login = RememberedManagedLogin(
    'fixture@example.invalid',
    'fixture-password',
  );
  ManagedAccountView view(
    _Store store,
    Future<void> Function(String, String, bool) submit,
  ) => ManagedAccountView(
    state: const ManagedAccountState(
      ready: true,
      account: ManagedAccountSnapshot(apiBase: scope),
    ),
    credentialStore: store,
    onLoginRemembered: submit,
    onLogin: (_, _) async {},
    onRefresh: () async {},
    onLogout: () async {},
    onRetryCore: () async {},
    onExit: () async {},
    onWebsite: () async {},
  );

  testWidgets(
    'remembered credentials prefill an obscured field but require an explicit login',
    (tester) async {
      final store = _Store()..result.complete(login);
      final submissions = <(String, String, bool)>[];
      await tester.pumpWidget(
        TestApp(
          child: view(store, (e, p, r) async {
            submissions.add((e, p, r));
          }),
        ),
      );
      await tester.pumpAndSettle();
      expect(submissions, isEmpty);
      final field = tester.widget<TextFormField>(
        find.byKey(const Key('managed-password')),
      );
      expect(field.controller!.text, login.password);
      final editable = tester.widget<EditableText>(
        find.descendant(
          of: find.byKey(const Key('managed-password')),
          matching: find.byType(EditableText),
        ),
      );
      expect(editable.obscureText, isTrue);
      await tester.ensureVisible(find.byKey(const Key('managed-login')));
      await tester.tap(find.byKey(const Key('managed-login')));
      await tester.pumpAndSettle();
      expect(submissions, [(login.email, login.password, true)]);
      expect(field.controller!.text, isEmpty);
    },
  );

  testWidgets('late Keychain data cannot overwrite typing', (tester) async {
    final store = _Store();
    await tester.pumpWidget(TestApp(child: view(store, (_, _, _) async {})));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const Key('managed-email')),
      'new@example.invalid',
    );
    store.result.complete(login);
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextFormField>(find.byKey(const Key('managed-email')))
          .controller!
          .text,
      'new@example.invalid',
    );
    expect(
      tester
          .widget<TextFormField>(find.byKey(const Key('managed-password')))
          .controller!
          .text,
      isEmpty,
    );
  });

  testWidgets(
    'unchecking remember immediately deletes the scoped stored login',
    (tester) async {
      final store = _Store()..result.complete(login);
      await tester.pumpWidget(TestApp(child: view(store, (_, _, _) async {})));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.byKey(const Key('managed-remember')));
      await tester.tap(find.byKey(const Key('managed-remember')));
      await tester.pumpAndSettle();
      expect(store.deleted, [scope]);
    },
  );
}
